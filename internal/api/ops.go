// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/stowage-dev/stowage/internal/metrics"
)

// NewOpsHandler is the listener a process without the dashboard role
// exposes on server.listen: liveness, readiness and Prometheus metrics,
// nothing else. The full router serves the same three paths, so probes
// and scrape configs are identical across roles.
func NewOpsHandler(mtx *metrics.Service) http.Handler {
	r := chi.NewRouter()
	r.Use(securityHeaders)
	r.Get("/healthz", healthz)
	r.Get("/readyz", readyz)
	if mtx != nil && mtx.Prom != nil {
		r.Method("GET", "/metrics", mtx.Prom.Handler())
	}
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "this stowage process does not serve the dashboard", http.StatusNotFound)
	})
	return r
}
