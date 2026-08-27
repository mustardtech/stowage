// Copyright (C) 2026 Mustard Technologies
// SPDX-License-Identifier: AGPL-3.0-or-later

package s3proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type scriptedRT struct {
	calls   int
	replies []func() (*http.Response, error)
}

func (f *scriptedRT) RoundTrip(*http.Request) (*http.Response, error) {
	i := f.calls
	f.calls++
	if i >= len(f.replies) {
		i = len(f.replies) - 1
	}
	return f.replies[i]()
}

func status(code int) func() (*http.Response, error) {
	return func() (*http.Response, error) {
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
}

func TestIdempotentGetRetriesOn503(t *testing.T) {
	rt := &scriptedRT{replies: []func() (*http.Response, error){status(503), status(503), status(200)}}
	s := &Server{transport: rt}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://backend/b/k", nil)
	resp, err := s.doUpstream(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("want 200 after retries, got %v %v", resp, err)
	}
	if rt.calls != 3 {
		t.Fatalf("want 3 attempts, got %d", rt.calls)
	}
}

func TestIdempotentGetGivesUpAfterAttempts(t *testing.T) {
	rt := &scriptedRT{replies: []func() (*http.Response, error){status(502)}}
	s := &Server{transport: rt}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodHead, "http://backend/b/k", nil)
	resp, err := s.doUpstream(req)
	if err != nil || resp.StatusCode != 502 {
		t.Fatalf("want final 502, got %v %v", resp, err)
	}
	if rt.calls != upstreamIdempotentAttempts {
		t.Fatalf("want %d attempts, got %d", upstreamIdempotentAttempts, rt.calls)
	}
}

func TestPutIsNotRetriedOn503(t *testing.T) {
	rt := &scriptedRT{replies: []func() (*http.Response, error){status(503), status(200)}}
	s := &Server{transport: rt}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, "http://backend/b/k", strings.NewReader("x"))
	resp, err := s.doUpstream(req)
	if err != nil || resp.StatusCode != 503 {
		t.Fatalf("want 503 passed through, got %v %v", resp, err)
	}
	if rt.calls != 1 {
		t.Fatalf("want 1 attempt, got %d", rt.calls)
	}
}

func TestIdempotentRetryStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rt := &scriptedRT{replies: []func() (*http.Response, error){func() (*http.Response, error) { cancel(); return nil, errors.New("reset") }}}
	s := &Server{transport: rt}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://backend/b/k", nil)
	if _, err := s.doUpstream(req); err == nil {
		t.Fatal("want error on cancelled context")
	}
	if rt.calls != 1 {
		t.Fatalf("want 1 attempt, got %d", rt.calls)
	}
}
