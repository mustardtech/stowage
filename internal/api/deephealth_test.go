// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func deepGet(t *testing.T, d *DeepHealth) (int, DeepHealthReport) {
	t.Helper()
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz/deep", nil))
	var rep DeepHealthReport
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("body not JSON: %v: %s", err, rec.Body.String())
	}
	return rec.Code, rep
}

func TestDeepHealthAllGood(t *testing.T) {
	d := &DeepHealth{
		Store:   func(context.Context) error { return nil },
		Sources: func() (bool, string) { return true, "" },
		Backends: func(context.Context) []BackendHealth {
			return []BackendHealth{{ID: "mustard", Healthy: true, LatencyMS: 3}}
		},
	}
	code, rep := deepGet(t, d)
	if code != 200 || rep.Status != "ok" || rep.Store != "ok" || rep.Sources != "synced" || len(rep.Backends) != 1 {
		t.Fatalf("code=%d rep=%+v", code, rep)
	}
}

func TestDeepHealthDegradedOnAnyCheck(t *testing.T) {
	cases := map[string]*DeepHealth{
		"store":    {Store: func(context.Context) error { return errors.New("dial tcp: refused") }},
		"sources":  {Sources: func() (bool, string) { return false, "informer not synced" }},
		"backends": {Backends: func(context.Context) []BackendHealth { return []BackendHealth{{ID: "b", Healthy: false, Error: "503"}} }},
	}
	for name, d := range cases {
		code, rep := deepGet(t, d)
		if code != http.StatusServiceUnavailable || rep.Status != "degraded" {
			t.Errorf("%s: code=%d status=%s", name, code, rep.Status)
		}
	}
}

func TestDeepHealthCachesWithinTTL(t *testing.T) {
	calls := 0
	d := &DeepHealth{TTL: time.Minute, Backends: func(context.Context) []BackendHealth { calls++; return nil }}
	deepGet(t, d)
	deepGet(t, d)
	if calls != 1 {
		t.Fatalf("backend probe ran %d times within the TTL, want 1", calls)
	}
	d.last = time.Now().Add(-2 * time.Minute)
	deepGet(t, d)
	if calls != 2 {
		t.Fatalf("backend probe did not rerun after the TTL (calls=%d)", calls)
	}
}

func TestDeepHealthNoChecksIsOK(t *testing.T) {
	code, rep := deepGet(t, &DeepHealth{})
	if code != 200 || rep.Status != "ok" {
		t.Fatalf("code=%d status=%s", code, rep.Status)
	}
}
