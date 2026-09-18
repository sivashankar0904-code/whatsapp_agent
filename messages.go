// Message persistence and the read API. whatsmeow's own tables hold session
// and crypto state, not message content — see the schema comment on
// ensureMessagesTable — so this is a second, independent table in the same
// database, populated as messages arrive and served over a small HTTP API.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// ensureMessagesTable creates the inbound message log if it does not already
// exist. It lives in the same database as the whatsmeow_* tables but is not
// one of them — whatsmeow owns and migrates those, so this uses its own name
// and its own lightweight migration to avoid any collision with a future
// whatsmeow schema change.
func ensureMessagesTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
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

// messageStore writes inbound messages and serves them back over HTTP.
type messageStore struct {
	db     *sql.DB
	logger waLog.Logger
}

// extractText pulls a readable body out of a WhatsApp message.
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
func extractText(msg *waE2E.Message) string {
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

// record inserts one message. Called from the whatsmeow event handler, so it
// must not block long or panic — a failed insert is logged and dropped rather
// than crashing the connection.
func (s *messageStore) record(ctx context.Context, evt *events.Message) {
	text := extractText(evt.Message)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_messages (chat_jid, sender_jid, from_me, text, "timestamp")
		VALUES ($1, $2, $3, $4, $5)`,
		evt.Info.Chat.String(), evt.Info.Sender.String(), evt.Info.IsFromMe, text, evt.Info.Timestamp)
	if err != nil {
		s.logger.Errorf("store message: %v", err)
	}
}

// storedMessage is the JSON shape returned by the API.
type storedMessage struct {
	ID         int64     `json:"id"`
	ChatJID    string    `json:"chat_jid"`
	SenderJID  string    `json:"sender_jid"`
	FromMe     bool      `json:"from_me"`
	Text       string    `json:"text"`
	Timestamp  time.Time `json:"timestamp"`
	ReceivedAt time.Time `json:"received_at"`
}

// handleList serves GET /messages.
//
// Query parameters:
//
//	chat   filter to one chat JID (e.g. 9199...@s.whatsapp.net or ...@g.us)
//	since  return only rows with id > since, for polling without duplicates
//	limit  max rows to return, default 50, capped at 500
//
// Rows come back oldest-first, matching a chat's natural reading order.
func (s *messageStore) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit := 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			http.Error(w, `"limit" must be a positive integer`, http.StatusBadRequest)
			return
		}
		limit = min(n, 500)
	}

	since := int64(0)
	if v := q.Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			http.Error(w, `"since" must be an integer message id`, http.StatusBadRequest)
			return
		}
		since = n
	}

	var rows *sql.Rows
	var err error
	if chat := q.Get("chat"); chat != "" {
		rows, err = s.db.QueryContext(r.Context(), `
			SELECT id, chat_jid, sender_jid, from_me, text, "timestamp", received_at
			FROM agent_messages WHERE chat_jid = $1 AND id > $2
			ORDER BY id ASC LIMIT $3`, chat, since, limit)
	} else {
		rows, err = s.db.QueryContext(r.Context(), `
			SELECT id, chat_jid, sender_jid, from_me, text, "timestamp", received_at
			FROM agent_messages WHERE id > $1
			ORDER BY id ASC LIMIT $2`, since, limit)
	}
	if err != nil {
		s.logger.Errorf("query messages: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []storedMessage{}
	for rows.Next() {
		var m storedMessage
		if err := rows.Scan(&m.ID, &m.ChatJID, &m.SenderJID, &m.FromMe, &m.Text, &m.Timestamp, &m.ReceivedAt); err != nil {
			s.logger.Errorf("scan message: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		s.logger.Errorf("iterate messages: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		s.logger.Errorf("encode response: %v", err)
	}
}

// handleHealth serves GET /health — a plain liveness check that does not touch
// the database, for a container orchestrator's health probe.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
