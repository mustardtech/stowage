// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlstore_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/stowage-dev/stowage/internal/store/sqlstore"
)

// openTestPostgres runs the test against its own freshly created schema in
// the database named by STOWAGE_TEST_POSTGRES_DSN (a libpq URL), so every
// store test in this package exercises the Postgres dialect unchanged:
//
//	STOWAGE_TEST_POSTGRES_DSN='postgres://stowage:stowage@localhost:55432/stowage_test?sslmode=disable' \
//	  go test ./internal/store/sqlstore/
//
// Unset, openTestStore uses SQLite as before.
func openTestPostgres(t *testing.T, dsn string) *sqlstore.Store {
	t.Helper()
	ctx := context.Background()

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("STOWAGE_TEST_POSTGRES_DSN must be a URL: %v", err)
	}
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	schema := "t_" + hex.EncodeToString(raw[:])

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	// citext lives in public so every per-test schema sees it; the store's
	// own CREATE EXTENSION IF NOT EXISTS is then a no-op.
	if _, err := admin.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS citext WITH SCHEMA public`); err != nil {
		_ = admin.Close()
		t.Fatalf("create extension citext: %v", err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		_ = admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(ctx, `DROP SCHEMA "`+schema+`" CASCADE`)
		_ = admin.Close()
	})

	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()

	store, err := sqlstore.OpenPostgres(ctx, u.String(), 4)
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if store.Dialect() != sqlstore.DialectPostgres {
		t.Fatalf("dialect = %v, want postgres", store.Dialect())
	}
	return store
}
