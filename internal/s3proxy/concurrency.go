// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package s3proxy

import (
	"context"
	"sync"
	"time"
)

// ConcurrencyLimiter bounds the number of requests the proxy holds in
// flight, globally and per access key. Unlike the RPS Limiter, which
// shapes arrival rate, this caps standing memory: every in-flight
// request can pin up to maxReplayableBody of buffered body, so when a
// backend stalls, admission — not arrival — is what has to stop.
//
// The per-key slot is acquired before the global slot. This ordering is
// the fairness guarantee: a key that has exhausted its own allowance
// queues on its key semaphore without holding anything global, so one
// greedy client stalled against a slow backend can never occupy slots
// that other credentials need.
//
// Zero values mean unlimited, matching Limiter's convention.
type ConcurrencyLimiter struct {
	global  chan struct{}
	perMax  int
	timeout time.Duration

	mu     sync.Mutex
	perKey map[string]chan struct{}
}

// NewConcurrencyLimiter constructs a ConcurrencyLimiter. globalMax and
// perKeyMax of 0 mean unlimited; timeout of 0 means waiters block until
// the request context is done.
func NewConcurrencyLimiter(globalMax, perKeyMax int, timeout time.Duration) *ConcurrencyLimiter {
	l := &ConcurrencyLimiter{
		perMax:  perKeyMax,
		timeout: timeout,
		perKey:  map[string]chan struct{}{},
	}
	if globalMax > 0 {
		l.global = make(chan struct{}, globalMax)
	}
	return l
}

// Acquire reserves one in-flight slot for key, waiting up to the
// configured timeout (bounded additionally by ctx). On success it
// returns a release func that must be called exactly once when the
// request finishes, and scope == "". On failure release is nil and
// scope names the tier that refused ("per-key" or "global") for
// metrics attribution.
func (l *ConcurrencyLimiter) Acquire(ctx context.Context, key string) (release func(), scope string) {
	if l == nil || (l.global == nil && l.perMax <= 0) {
		return func() {}, ""
	}
	if l.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, l.timeout)
		defer cancel()
	}

	var keyCh chan struct{}
	if l.perMax > 0 {
		l.mu.Lock()
		keyCh = l.perKey[key]
		if keyCh == nil {
			keyCh = make(chan struct{}, l.perMax)
			l.perKey[key] = keyCh
		}
		l.mu.Unlock()
		select {
		case keyCh <- struct{}{}:
		case <-ctx.Done():
			return nil, "per-key"
		}
	}
	if l.global != nil {
		select {
		case l.global <- struct{}{}:
		case <-ctx.Done():
			if keyCh != nil {
				<-keyCh
			}
			return nil, "global"
		}
	}
	return func() {
		if l.global != nil {
			<-l.global
		}
		if keyCh != nil {
			<-keyCh
		}
	}, ""
}
