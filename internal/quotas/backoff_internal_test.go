// Copyright (C) 2026 Mustard Technologies
// SPDX-License-Identifier: AGPL-3.0-or-later

package quotas

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stowage-dev/stowage/internal/backend"
)

// A limit for a backend that isn't registered makes Scan fail fast, which
// stands in for "backend cannot be listed within the inline cap".
type oneLimit struct{ key LimitKey }

func (o oneLimit) Get(backendID, bucket string) (*Limit, bool) {
	if backendID == o.key.BackendID && bucket == o.key.Bucket {
		return &Limit{HardBytes: 1}, true
	}
	return nil, false
}
func (o oneLimit) List() []LimitKey                      { return []LimitKey{o.key} }
func (o oneLimit) Subscribe(func()) (unsubscribe func()) { return func() {} }

func TestCheckUploadBacksOffAfterFailedSyncScan(t *testing.T) {
	s := New(oneLimit{LimitKey{BackendID: "missing", Bucket: "b"}}, nil, backend.NewRegistry(), slog.Default())
	ctx := context.Background()

	if err := s.CheckUpload(ctx, "missing", "b", 1); err != nil {
		t.Fatalf("first upload should pass on scan failure, got %v", err)
	}
	if _, ok := s.failedScans["missing/b"]; !ok {
		t.Fatal("failed scan was not recorded")
	}
	if !s.inSyncScanBackoff("missing", "b") {
		t.Fatal("expected bucket to be in backoff")
	}
	// Expire the backoff and confirm the next upload scans again.
	s.mu.Lock()
	s.failedScans["missing/b"] = time.Now().Add(-syncScanBackoff - time.Second)
	s.mu.Unlock()
	if s.inSyncScanBackoff("missing", "b") {
		t.Fatal("expected backoff to have expired")
	}
}
