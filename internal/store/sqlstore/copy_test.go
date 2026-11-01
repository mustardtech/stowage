// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlstore_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stowage-dev/stowage/internal/store/sqlstore"
)

// TestCopyToPostgres is the `stowage migrate` path: a populated SQLite
// store lands row-for-row in an empty Postgres schema, and the identity
// sequence is moved past the copied audit ids. Needs
// STOWAGE_TEST_POSTGRES_DSN; skipped otherwise.
func TestCopyToPostgres(t *testing.T) {
	dsn := os.Getenv("STOWAGE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("STOWAGE_TEST_POSTGRES_DSN not set")
	}
	ctx := context.Background()
	src, err := sqlstore.Open(ctx, filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })
	dst := openTestPostgres(t, dsn)

	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := src.CreateUser(ctx, &sqlstore.User{
		ID: "u1", Username: "Alice", Email: sql.NullString{String: "Alice@Example.com", Valid: true},
		PasswordHash: "x", Role: "admin", IdentitySource: "local", Enabled: true,
		CreatedAt: now, PWChangedAt: now,
	}); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := src.CreateBackend(ctx, &sqlstore.Backend{
		ID: "b1", Name: "Backend", Type: "s3v4", Endpoint: "http://s3.local", PathStyle: true,
		AccessKey: "AK", SecretKeyEnc: []byte{0, 1, 2, 255}, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("backend: %v", err)
	}
	if err := src.InsertPin(ctx, &sqlstore.BucketPin{UserID: "u1", BackendID: "b1", Bucket: "photos", CreatedAt: now}); err != nil {
		t.Fatalf("pin: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := src.InsertAuditEvent(ctx, &sqlstore.AuditEvent{Timestamp: now, Action: "copy.test", Status: "ok"}); err != nil {
			t.Fatalf("audit: %v", err)
		}
	}
	// A pre-sanitiser row with raw invalid UTF-8, exactly as older SQLite
	// files hold them; the copier must carry it across.
	if _, err := src.DB.ExecContext(ctx, `INSERT INTO audit_events (ts, action, status, object_key) VALUES (?, ?, ?, ?)`,
		now, "copy.raw", "ok", "k/\xc0\xaf\x00"); err != nil {
		t.Fatalf("raw audit row: %v", err)
	}

	report, err := sqlstore.CopyTo(ctx, src, dst)
	if err != nil {
		t.Fatalf("CopyTo: %v", err)
	}
	for table, want := range map[string]int64{"users": 1, "backends": 1, "bucket_pins": 1, "audit_events": 4, "sessions": 0} {
		if got := report.Rows[table]; got != want {
			t.Errorf("%s: copied %d rows, want %d", table, got, want)
		}
	}

	u, err := dst.GetUserByUsername(ctx, "alice") // CITEXT: case-insensitive like NOCASE
	if err != nil {
		t.Fatalf("user after copy: %v", err)
	}
	if u.ID != "u1" || !u.Enabled || !u.CreatedAt.Equal(now) {
		t.Errorf("user row mangled: %+v", u)
	}
	b, err := dst.GetBackend(ctx, "b1")
	if err != nil {
		t.Fatalf("backend after copy: %v", err)
	}
	if string(b.SecretKeyEnc) != string([]byte{0, 1, 2, 255}) || !b.PathStyle {
		t.Errorf("backend row mangled: %+v", b)
	}

	// The identity sequence must continue past the copied ids.
	if err := dst.InsertAuditEvent(ctx, &sqlstore.AuditEvent{Timestamp: now, Action: "copy.after", Status: "ok"}); err != nil {
		t.Fatalf("insert after copy (sequence not advanced?): %v", err)
	}
	n, err := dst.CountAuditEvents(ctx, sqlstore.AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("audit rows after copy = %d, want 5", n)
	}

	// A second copy into the now-populated destination must refuse.
	if _, err := sqlstore.CopyTo(ctx, src, dst); err == nil {
		t.Error("CopyTo into a non-empty destination should fail")
	}
}
