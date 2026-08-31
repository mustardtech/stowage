// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package s3proxy

import (
	"context"
	"sync"
	"testing"
	"time"
)

func mustAcquire(t *testing.T, l *ConcurrencyLimiter, key string) func() {
	t.Helper()
	release, scope := l.Acquire(context.Background(), key)
	if release == nil {
		t.Fatalf("acquire for %q refused at scope %q, want success", key, scope)
	}
	return release
}

func TestConcurrencyUnlimited(t *testing.T) {
	for _, l := range []*ConcurrencyLimiter{nil, NewConcurrencyLimiter(0, 0, time.Millisecond)} {
		release, scope := l.Acquire(context.Background(), "k")
		if release == nil {
			t.Fatalf("unlimited limiter refused at scope %q", scope)
		}
		release()
	}
}

func TestConcurrencyPerKeyCapAndRelease(t *testing.T) {
	l := NewConcurrencyLimiter(0, 2, 20*time.Millisecond)
	r1 := mustAcquire(t, l, "a")
	r2 := mustAcquire(t, l, "a")

	release, scope := l.Acquire(context.Background(), "a")
	if release != nil {
		t.Fatal("third acquire for full key succeeded, want per-key refusal")
	}
	if scope != "per-key" {
		t.Fatalf("scope = %q, want per-key", scope)
	}

	// A different key is unaffected by "a" being full.
	mustAcquire(t, l, "b")()

	r1()
	mustAcquire(t, l, "a")()
	r2()
}

func TestConcurrencyGlobalCap(t *testing.T) {
	l := NewConcurrencyLimiter(2, 5, 20*time.Millisecond)
	mustAcquire(t, l, "a")
	mustAcquire(t, l, "b")

	release, scope := l.Acquire(context.Background(), "c")
	if release != nil {
		t.Fatal("acquire past global cap succeeded, want refusal")
	}
	if scope != "global" {
		t.Fatalf("scope = %q, want global", scope)
	}
}

// TestConcurrencyGreedyKeyDoesNotHoldGlobal is the fairness guarantee:
// waiters queued on a full per-key semaphore must not occupy global
// slots, so a stalled greedy client leaves the global pool available to
// every other key.
func TestConcurrencyGreedyKeyDoesNotHoldGlobal(t *testing.T) {
	l := NewConcurrencyLimiter(4, 2, time.Second)
	mustAcquire(t, l, "greedy")
	mustAcquire(t, l, "greedy")

	// Two more greedy requests queue on the per-key semaphore.
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if release, _ := l.Acquire(ctx, "greedy"); release != nil {
				release()
			}
		}()
	}

	// The other key must still get the remaining global slots promptly.
	// If the greedy waiters held global slots, these would block ~1s.
	done := make(chan struct{})
	go func() {
		mustAcquire(t, l, "other")
		mustAcquire(t, l, "other")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("other key blocked behind greedy key's queued waiters")
	}

	cancel()
	wg.Wait()
}

func TestConcurrencyContextCancelAbortsWait(t *testing.T) {
	l := NewConcurrencyLimiter(1, 0, time.Minute)
	mustAcquire(t, l, "a")

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	start := time.Now()
	release, _ := l.Acquire(ctx, "b")
	if release != nil {
		t.Fatal("acquire succeeded past global cap")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancelled acquire did not return promptly")
	}
}

// TestConcurrencyGlobalTimeoutReleasesKeySlot verifies that a request
// refused at the global tier hands back the per-key slot it already
// held, so failed admissions never leak per-key capacity.
func TestConcurrencyGlobalTimeoutReleasesKeySlot(t *testing.T) {
	l := NewConcurrencyLimiter(1, 1, 20*time.Millisecond)
	rel := mustAcquire(t, l, "a")

	if release, scope := l.Acquire(context.Background(), "b"); release != nil {
		t.Fatal("acquire past global cap succeeded")
	} else if scope != "global" {
		t.Fatalf("scope = %q, want global", scope)
	}

	rel()
	// Key "b" must have its per-key slot back after the global refusal.
	mustAcquire(t, l, "b")()
}
