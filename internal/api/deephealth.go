// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// DeepHealth answers /healthz/deep: can THIS process actually serve, not
// just accept connections. It is meant for whatever fronts several stowage
// deployments (a load balancer failing a site over, a Traefik health
// check), where /healthz alone would keep sending traffic to a process
// whose store or backend is gone.
//
// Each check is optional (nil = skipped) so every role wires only what it
// has. Results are cached for TTL: pollers hit this every few seconds and
// the backend probe is a real round trip to the object store.
type DeepHealth struct {
	// Store pings the shared database.
	Store func(ctx context.Context) error
	// Sources reports whether the credential sources are primed (the
	// Kubernetes informer has synced); detail explains a false.
	Sources func() (ok bool, detail string)
	// Backends probes the object-store backends this process must reach.
	Backends func(ctx context.Context) []BackendHealth
	// TTL bounds how often the checks run. Zero = 10 s.
	TTL time.Duration
	// Timeout bounds one full check. Zero = 5 s.
	Timeout time.Duration

	mu   sync.Mutex
	last time.Time
	rep  DeepHealthReport
}

// BackendHealth is one backend's probe result.
type BackendHealth struct {
	ID        string `json:"id"`
	Healthy   bool   `json:"healthy"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

// DeepHealthReport is the JSON body. Status is "ok" or "degraded".
type DeepHealthReport struct {
	Status    string          `json:"status"`
	Store     string          `json:"store,omitempty"`
	Sources   string          `json:"sources,omitempty"`
	Backends  []BackendHealth `json:"backends,omitempty"`
	CheckedAt time.Time       `json:"checked_at"`
}

// Check runs (or returns the cached) report.
func (d *DeepHealth) Check(ctx context.Context) DeepHealthReport {
	ttl := d.TTL
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.last.IsZero() && time.Since(d.last) < ttl {
		return d.rep
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rep := DeepHealthReport{Status: "ok", CheckedAt: time.Now().UTC()}
	if d.Store != nil {
		if err := d.Store(ctx); err != nil {
			rep.Store = err.Error()
			rep.Status = "degraded"
		} else {
			rep.Store = "ok"
		}
	}
	if d.Sources != nil {
		if ok, detail := d.Sources(); ok {
			rep.Sources = "synced"
		} else {
			rep.Sources = detail
			rep.Status = "degraded"
		}
	}
	if d.Backends != nil {
		rep.Backends = d.Backends(ctx)
		for _, b := range rep.Backends {
			if !b.Healthy {
				rep.Status = "degraded"
			}
		}
	}
	d.last, d.rep = time.Now(), rep
	return rep
}

// Handler serves the report: 200 when every check passed, 503 otherwise,
// always with the JSON body so the caller can see which check failed.
func (d *DeepHealth) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rep := d.Check(r.Context())
		code := http.StatusOK
		if rep.Status != "ok" {
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, code, rep)
	}
}
