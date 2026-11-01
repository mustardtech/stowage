// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stowage-dev/stowage/internal/metrics"
)

func TestOpsHandler(t *testing.T) {
	mtx := metrics.New()
	mtx.Prom = metrics.NewProm("")
	h := NewOpsHandler(mtx)
	for path, want := range map[string]int{"/healthz": 200, "/readyz": 200, "/metrics": 200, "/api/me": 404, "/": 404} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
}
