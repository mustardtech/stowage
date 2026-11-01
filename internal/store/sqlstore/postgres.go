// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"

	"github.com/stowage-dev/stowage/internal/config"
)

// DefaultPostgresMaxOpenConns bounds the pool when the config leaves
// max_open_conns unset. Sixteen covers a busy dashboard plus the audit
// drainer with room to spare; raise it for many proxy replicas sharing
// one database only if the server's max_connections allows.
const DefaultPostgresMaxOpenConns = 16

// OpenPostgres connects to a PostgreSQL database (libpq URL or key=value
// DSN), applies migrations, and returns a Store. Unlike SQLite there is no
// writer/reader split: both fields point at one pool, and any number of
// stowage processes may open the same database concurrently.
func OpenPostgres(ctx context.Context, dsn string, maxOpenConns int) (*Store, error) {
	if dsn == "" {
		return nil, fmt.Errorf("postgres dsn is empty")
	}
	if maxOpenConns <= 0 {
		maxOpenConns = DefaultPostgresMaxOpenConns
	}
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	pool.SetMaxOpenConns(maxOpenConns)
	pool.SetMaxIdleConns(maxOpenConns / 2)
	// Recycle connections so a failover or a pooler-side restart is picked
	// up within minutes instead of lingering on a dead socket.
	pool.SetConnMaxLifetime(30 * time.Minute)
	pool.SetConnMaxIdleTime(5 * time.Minute)

	if err := pool.PingContext(ctx); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	db := &DB{DB: pool, d: DialectPostgres}
	s := &Store{DB: db, R: db, dialect: DialectPostgres}
	if err := s.migrate(ctx); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// OpenConfig opens whichever store db.driver names. Callers that only ever
// want SQLite can keep using Open.
func OpenConfig(ctx context.Context, cfg config.DBConfig) (*Store, error) {
	switch cfg.Driver {
	case "", "sqlite":
		return Open(ctx, cfg.SQLite.Path)
	case "postgres":
		return OpenPostgres(ctx, cfg.Postgres.DSN, cfg.Postgres.MaxOpenConns)
	default:
		return nil, fmt.Errorf("db.driver %q unknown", cfg.Driver)
	}
}
