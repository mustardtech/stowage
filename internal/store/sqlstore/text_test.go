// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlstore_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/stowage-dev/stowage/internal/store/sqlstore"
)

// Proxy audit fields come off the wire; a client once sent an object key
// with a lone 0xC0 byte. SQLite stored it, Postgres refused the whole
// batch (SQLSTATE 22021). Inserts must sanitise on both engines.
func TestAuditInsertInvalidUTF8(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	bad := "photos/\xc0\xafname.jpg"
	if err := store.InsertAuditEvent(ctx, &sqlstore.AuditEvent{
		Timestamp: time.Now().UTC(), Action: "s3.put", Status: "ok",
		ObjectKey: sql.NullString{String: bad, Valid: true},
		UserAgent: sql.NullString{String: "agent\xff", Valid: true},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.InsertAuditEvents(ctx, []*sqlstore.AuditEvent{{
		Timestamp: time.Now().UTC(), Action: "s3.put", Status: "ok",
		ObjectKey: sql.NullString{String: bad, Valid: true},
	}}); err != nil {
		t.Fatalf("batch insert: %v", err)
	}
	events, err := store.ListAuditEvents(ctx, sqlstore.AuditFilter{Action: "s3.put"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	for _, e := range events {
		if !strings.HasPrefix(e.ObjectKey.String, "photos/") || !strings.Contains(e.ObjectKey.String, "�") || strings.Contains(e.ObjectKey.String, "\xc0") {
			t.Errorf("object key not sanitised: %q", e.ObjectKey.String)
		}
	}
}
