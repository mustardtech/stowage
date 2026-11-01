// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/stowage-dev/stowage/internal/store/sqlstore"
)

// Byte counts exceed int32 in any real deployment: an 80 GiB hard quota
// overflowed Postgres INTEGER on the first production copy. SQLite's
// INTEGER is 64-bit, so the Postgres DDL must be BIGINT.
func TestQuotaBytesAreSixtyFourBit(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	const hard = int64(85899345920) // 80 GiB
	if err := store.UpsertQuota(ctx, &sqlstore.BucketQuota{
		BackendID: "b", Bucket: "big", SoftBytes: hard - 1, HardBytes: hard,
		UpdatedAt: time.Now().UTC(), UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	q, err := store.GetQuota(ctx, "b", "big")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if q.HardBytes != hard || q.SoftBytes != hard-1 {
		t.Fatalf("round trip = soft %d hard %d, want %d / %d", q.SoftBytes, q.HardBytes, hard-1, hard)
	}
}
