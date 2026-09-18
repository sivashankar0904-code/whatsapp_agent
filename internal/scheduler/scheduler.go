// Package scheduler runs a periodic job over every registered chat_jid, on
// an interval read from the environment, and exposes the same job to be
// triggered on demand (e.g. from an API call). The job prints every YouTube
// link found in each chat's unprocessed messages, then marks them processed.
package scheduler

import (
	"context"
	"sync"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/sivashankar/whatsapp_agent/internal/messages"
	"github.com/sivashankar/whatsapp_agent/internal/registrations"
)

// messagesPerRun caps how many new messages are scanned per chat per run.
// The scheduler advances each chat's high-water mark every run, so normal
// traffic never approaches this; it only bounds a single run's work if a
// chat has an unusually large backlog (e.g. its first run after being
// registered against a chat with existing history).
const messagesPerRun = 1000

// Scheduler runs Job for every registered chat_jid, either on its own
// ticker or on demand via RunOnce. Concurrent runs are serialized: a tick
// that lands while an on-demand run (or another tick) is still in flight is
// skipped rather than queued, since these runs are idempotent status
// reports, not work that needs to happen exactly once per interval.
type Scheduler struct {
	regStore *registrations.Store
	msgStore *messages.Store
	logger   waLog.Logger
	interval time.Duration

	mu      sync.Mutex
	running bool
}

// New returns a Scheduler that runs its job over regStore's registered
// chats every interval.
func New(regStore *registrations.Store, msgStore *messages.Store, logger waLog.Logger, interval time.Duration) *Scheduler {
	return &Scheduler{regStore: regStore, msgStore: msgStore, logger: logger, interval: interval}
}

// Run blocks, firing RunOnce every interval until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	s.logger.Infof("scheduler running every %s", s.interval)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RunOnce(ctx)
		}
	}
}

// RunOnce runs the job immediately, once, over every registered chat_jid.
// Safe to call concurrently with the ticker loop or with itself (e.g. from
// an API handler) — a call that arrives while one is already in flight is
// skipped and reports so via the returned bool.
func (s *Scheduler) RunOnce(ctx context.Context) bool {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		s.logger.Warnf("scheduler run already in progress, skipping")
		return false
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	regs, err := s.regStore.List(ctx)
	if err != nil {
		s.logger.Errorf("scheduler: list registrations: %v", err)
		return true
	}

	s.logger.Infof("scheduler run: %d registered chat(s)", len(regs))
	for _, r := range regs {
		s.runJob(ctx, r)
	}
	return true
}

// runJob prints every YouTube link found in chatJID's unprocessed messages,
// then marks all of them processed — whether or not they contained a link —
// so this run's messages are never rescanned. Marking is per-message
// (agent_messages.processed), not a per-chat cursor, so a message being
// read through any other means (e.g. GET /messages) never marks it seen.
func (s *Scheduler) runJob(ctx context.Context, reg registrations.Registration) {
	msgs, err := s.msgStore.ListUnprocessed(ctx, reg.ChatJID, messagesPerRun)
	if err != nil {
		s.logger.Errorf("scheduler: list messages for %s: %v", reg.ChatJID, err)
		return
	}
	if len(msgs) == 0 {
		return
	}

	ids := make([]int64, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
		for _, link := range messages.ExtractYouTubeLinks(m.Text) {
			s.logger.Infof("scheduler: chat_jid=%s youtube_link=%s", reg.ChatJID, link)
		}
	}

	if err := s.msgStore.MarkProcessed(ctx, ids); err != nil {
		s.logger.Errorf("scheduler: mark processed for %s: %v", reg.ChatJID, err)
	}
}
