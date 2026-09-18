// Package chats persists known WhatsApp chats (1:1 and groups), addressed
// by chat_jid the same way internal/messages addresses agent_messages.
package chats

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a chat does not exist.
var ErrNotFound = errors.New("chat not found")

// EnsureTable creates the chats table if it does not already exist.
// Mirrored in schemas/02_chats.sql — keep both in sync.
func EnsureTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS chats (
			chat_jid     TEXT PRIMARY KEY,
			display_name TEXT,
			is_group     BOOLEAN NOT NULL DEFAULT false,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS chats_display_name_idx
			ON chats (display_name);
	`)
	return err
}

// Chat is one row of the chats table.
type Chat struct {
	ChatJID     string    `json:"chat_jid"`
	DisplayName *string   `json:"display_name"`
	IsGroup     bool      `json:"is_group"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Store is the CRUD interface over the chats table.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Upsert creates the chat if it does not exist, or updates its display_name
// and is_group if it does. displayName of "" is stored as NULL, so an
// unknown name is never confused with an actual empty string.
func (s *Store) Upsert(ctx context.Context, chatJID, displayName string, isGroup bool) (Chat, error) {
	var name *string
	if displayName != "" {
		name = &displayName
	}

	var c Chat
	err := s.pool.QueryRow(ctx, `
		INSERT INTO chats (chat_jid, display_name, is_group)
		VALUES ($1, $2, $3)
		ON CONFLICT (chat_jid) DO UPDATE SET
			display_name = COALESCE(EXCLUDED.display_name, chats.display_name),
			is_group = EXCLUDED.is_group,
			updated_at = now()
		RETURNING chat_jid, display_name, is_group, created_at, updated_at`,
		chatJID, name, isGroup).Scan(&c.ChatJID, &c.DisplayName, &c.IsGroup, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Chat{}, err
	}
	return c, nil
}

// Get returns the chat with the given JID.
func (s *Store) Get(ctx context.Context, chatJID string) (Chat, error) {
	var c Chat
	err := s.pool.QueryRow(ctx, `
		SELECT chat_jid, display_name, is_group, created_at, updated_at
		FROM chats WHERE chat_jid = $1`, chatJID).
		Scan(&c.ChatJID, &c.DisplayName, &c.IsGroup, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Chat{}, ErrNotFound
	}
	return c, err
}

// GetIDByName returns the chat_jid of the chat whose display_name matches
// name exactly. If more than one chat shares the name, the most recently
// updated one wins — WhatsApp does not enforce unique group names.
func (s *Store) GetIDByName(ctx context.Context, name string) (string, error) {
	var chatJID string
	err := s.pool.QueryRow(ctx, `
		SELECT chat_jid FROM chats
		WHERE display_name = $1
		ORDER BY updated_at DESC
		LIMIT 1`, name).Scan(&chatJID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return chatJID, err
}

// List returns every known chat, most recently updated first.
func (s *Store) List(ctx context.Context) ([]Chat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT chat_jid, display_name, is_group, created_at, updated_at
		FROM chats ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Chat{}
	for rows.Next() {
		var c Chat
		if err := rows.Scan(&c.ChatJID, &c.DisplayName, &c.IsGroup, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Delete removes the chat with the given JID. Returns ErrNotFound if no
// such chat exists.
func (s *Store) Delete(ctx context.Context, chatJID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM chats WHERE chat_jid = $1`, chatJID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
