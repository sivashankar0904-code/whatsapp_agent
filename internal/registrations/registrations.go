// Package registrations tracks which chat_jids are opted into the
// scheduled job (internal/scheduler).
package registrations

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a chat_jid is not registered.
var ErrNotFound = errors.New("registration not found")

// EnsureTable creates the registrations table if it does not already
// exist. Mirrored in schemas/03_registrations.sql — keep both in sync.
func EnsureTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS registrations (
			chat_jid   TEXT PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`)
	return err
}

// Registration is one row of the registrations table.
type Registration struct {
	ChatJID   string    `json:"chat_jid"`
	CreatedAt time.Time `json:"created_at"`
}

// Store is the CRUD interface over the registrations table.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Register adds chatJID to the registration list. Registering an
// already-registered chat_jid is a no-op that returns its existing row.
func (s *Store) Register(ctx context.Context, chatJID string) (Registration, error) {
	var r Registration
	err := s.pool.QueryRow(ctx, `
		INSERT INTO registrations (chat_jid)
		VALUES ($1)
		ON CONFLICT (chat_jid) DO UPDATE SET chat_jid = EXCLUDED.chat_jid
		RETURNING chat_jid, created_at`, chatJID).
		Scan(&r.ChatJID, &r.CreatedAt)
	return r, err
}

// Get returns the registration for chatJID.
func (s *Store) Get(ctx context.Context, chatJID string) (Registration, error) {
	var r Registration
	err := s.pool.QueryRow(ctx, `
		SELECT chat_jid, created_at FROM registrations WHERE chat_jid = $1`, chatJID).
		Scan(&r.ChatJID, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Registration{}, ErrNotFound
	}
	return r, err
}

// List returns every registered chat_jid, oldest first.
func (s *Store) List(ctx context.Context) ([]Registration, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT chat_jid, created_at FROM registrations ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Registration{}
	for rows.Next() {
		var r Registration
		if err := rows.Scan(&r.ChatJID, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Unregister removes chatJID from the registration list. Returns
// ErrNotFound if it was not registered.
func (s *Store) Unregister(ctx context.Context, chatJID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM registrations WHERE chat_jid = $1`, chatJID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
