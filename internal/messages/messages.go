// Package messages persists inbound WhatsApp messages. whatsmeow's own
// tables hold session and crypto state, not message content — see the
// schema comment on EnsureTable — so this is a second, independent table in
// the same database, populated as messages arrive.
package messages

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// EnsureTable creates the inbound message log if it does not already exist.
// It lives in the same database as the whatsmeow_* tables but is not one of
// them — whatsmeow owns and migrates those, so this uses its own name and
// its own lightweight migration to avoid any collision with a future
// whatsmeow schema change.
func EnsureTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS agent_messages (
			id         BIGSERIAL PRIMARY KEY,
			chat_jid   TEXT NOT NULL,
			sender_jid TEXT NOT NULL,
			from_me    BOOLEAN NOT NULL,
			text       TEXT NOT NULL,
			"timestamp" TIMESTAMPTZ NOT NULL,
			received_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS agent_messages_chat_jid_id_idx
			ON agent_messages (chat_jid, id);
	`)
	return err
}

// Store writes inbound messages and serves them back over HTTP.
type Store struct {
	pool   *pgxpool.Pool
	logger waLog.Logger
}

// NewStore returns a Store backed by pool.
func NewStore(pool *pgxpool.Pool, logger waLog.Logger) *Store {
	return &Store{pool: pool, logger: logger}
}

// ExtractText pulls a readable body out of a WhatsApp message.
//
// waE2E.Message is a tagged union of ~50 possible payloads (protobuf field
// numbers 1 through 76+), not just plain text — reading only Conversation
// silently drops everything else as empty, which is what happened here
// originally. This covers the cases with a natural text form; other types
// (locations, contacts, polls, payments, receipts, key-distribution messages)
// intentionally store as empty for now rather than guess at a representation.
//
// Messages the account itself sent from another linked device (fromMe=true)
// arrive wrapped in DeviceSentMessage, one level deeper than everything else —
// whatsmeow does the same unwrap internally in processProtocolParts before its
// own protocol handling, but events.Message is not unwrapped for handlers, so
// every case below must be checked on the unwrapped message.
func ExtractText(msg *waE2E.Message) string {
	if inner := msg.GetDeviceSentMessage().GetMessage(); inner != nil {
		msg = inner
	}
	switch {
	case msg.GetConversation() != "":
		return msg.GetConversation()
	case msg.GetExtendedTextMessage().GetText() != "":
		// Links, replies, and formatted text.
		return msg.GetExtendedTextMessage().GetText()
	case msg.GetImageMessage().GetCaption() != "":
		return msg.GetImageMessage().GetCaption()
	case msg.GetVideoMessage().GetCaption() != "":
		return msg.GetVideoMessage().GetCaption()
	case msg.GetDocumentMessage().GetCaption() != "":
		return msg.GetDocumentMessage().GetCaption()
	default:
		return ""
	}
}

// Record inserts one message. Called from the whatsmeow event handler, so it
// must not block long or panic — a failed insert is logged and dropped rather
// than crashing the connection.
func (s *Store) Record(ctx context.Context, evt *events.Message) {
	text := ExtractText(evt.Message)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_messages (chat_jid, sender_jid, from_me, text, "timestamp")
		VALUES ($1, $2, $3, $4, $5)`,
		evt.Info.Chat.String(), evt.Info.Sender.String(), evt.Info.IsFromMe, text, evt.Info.Timestamp)
	if err != nil {
		s.logger.Errorf("store message: %v", err)
	}
}

// Message is the JSON shape returned by the API.
type Message struct {
	ID         int64     `json:"id"`
	ChatJID    string    `json:"chat_jid"`
	SenderJID  string    `json:"sender_jid"`
	FromMe     bool      `json:"from_me"`
	Text       string    `json:"text"`
	Timestamp  time.Time `json:"timestamp"`
	ReceivedAt time.Time `json:"received_at"`
}

// List returns stored messages, oldest first, matching a chat's natural
// reading order.
//
//   - chat  filter to one chat JID (e.g. 9199...@s.whatsapp.net or ...@g.us);
//     empty means no filter
//   - since return only rows with id > since, for polling without duplicates
//   - limit max rows to return
func (s *Store) List(ctx context.Context, chat string, since int64, limit int) ([]Message, error) {
	var rows pgx.Rows
	var err error
	if chat != "" {
		rows, err = s.pool.Query(ctx, `
			SELECT id, chat_jid, sender_jid, from_me, text, "timestamp", received_at
			FROM agent_messages WHERE chat_jid = $1 AND id > $2
			ORDER BY id ASC LIMIT $3`, chat, since, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT id, chat_jid, sender_jid, from_me, text, "timestamp", received_at
			FROM agent_messages WHERE id > $1
			ORDER BY id ASC LIMIT $2`, since, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ChatJID, &m.SenderJID, &m.FromMe, &m.Text, &m.Timestamp, &m.ReceivedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
