// Package datastore builds the Postgres connection pool shared by the
// message store and, via its DSN, whatsmeow's own session store.
package datastore

import (
	"context"
	"fmt"
	"net"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds the discrete connection parameters. Built from DB_* env vars
// rather than one URL, matching how the rest of this deployment's services
// are configured.
type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	DBName   string
	SSLMode  string
}

// DSN returns the postgres connection string for both pgxpool and
// whatsmeow's sqlstore, which parses this same "postgres://" form.
func (cfg Config) DSN() string {
	sslMode := cfg.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=%s",
		cfg.Username, cfg.Password, net.JoinHostPort(cfg.Host, cfg.Port), cfg.DBName, sslMode)
}

// Connect builds a connection pool for the app's own tables.
//
// whatsmeow's sqlstore.Container does not expose the *sql.DB/pool it wraps,
// and mixing this pool into whatsmeow's own connection would tie its
// lifecycle to code whatsmeow does not know about, so this is intentionally
// a second, independent pool against the same database.
func Connect(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("create pg pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	return pool, nil
}
