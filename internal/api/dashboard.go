// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"time"

	"github.com/stowage-dev/stowage/internal/metrics"
	"github.com/stowage-dev/stowage/internal/quotas"
)

// DashboardDeps owns the inputs to the admin dashboard endpoint. Both
// fields are optional; missing pieces just blank out the corresponding
// section of the response so the page degrades gracefully.
type DashboardDeps struct {
	Metrics *metrics.Service
	Quotas  *quotas.Service
}

type dashboardResponse struct {
	Requests metrics.Snapshot        `json:"requests"`
	Storage  storageDashboardSection `json:"storage"`
}

type storageDashboardSection struct {
	ByBackend  []backendStorageDTO  `json:"by_backend"`
	TopBuckets []topBucketDTO       `json:"top_buckets"`
	CacheNote  string               `json:"cache_note,omitempty"`
	Unscanned  []unscannedBucketDTO `json:"unscanned"`
}

type backendStorageDTO struct {
	BackendID string `json:"backend_id"`
	Bytes     int64  `json:"bytes"`
	Objects   int64  `json:"objects"`
	Buckets   int    `json:"buckets"`
}

type topBucketDTO struct {
	BackendID  string `json:"backend_id"`
	Bucket     string `json:"bucket"`
	Bytes      int64  `json:"bytes"`
	Objects    int64  `json:"objects"`
	ComputedAt string `json:"computed_at,omitempty"`
}

type unscannedBucketDTO struct {
	BackendID string `json:"backend_id"`
	Bucket    string `json:"bucket"`
	FailedAt  string `json:"failed_at"`
}

// handleDashboard serves /api/admin/dashboard. Admin-only at the router
// layer.
func (d *DashboardDeps) handleDashboard(w http.ResponseWriter, _ *http.Request) {
	resp := dashboardResponse{}

	if d.Metrics != nil {
		resp.Requests = d.Metrics.Snapshot()
	} else {
		resp.Requests = metrics.Snapshot{Hourly: []metrics.HourlyPoint{}}
	}

	if d.Quotas != nil {
		totals := d.Quotas.BackendTotals()
		resp.Storage.ByBackend = make([]backendStorageDTO, 0, len(totals))
		for _, t := range totals {
			resp.Storage.ByBackend = append(resp.Storage.ByBackend, backendStorageDTO{
				BackendID: t.BackendID,
				Bytes:     t.Bytes,
				Objects:   t.Objects,
				Buckets:   t.Buckets,
			})
		}

		top := d.Quotas.TopBuckets(10)
		resp.Storage.TopBuckets = make([]topBucketDTO, 0, len(top))
		for _, b := range top {
			row := topBucketDTO{
				BackendID: b.BackendID,
				Bucket:    b.Bucket,
				Bytes:     b.Bytes,
				Objects:   b.Objects,
			}
			if !b.ComputedAt.IsZero() {
				row.ComputedAt = b.ComputedAt.UTC().Format(time.RFC3339)
			}
			resp.Storage.TopBuckets = append(resp.Storage.TopBuckets, row)
		}
		unscanned := d.Quotas.Unscanned()
		resp.Storage.Unscanned = make([]unscannedBucketDTO, 0, len(unscanned))
		for _, u := range unscanned {
			resp.Storage.Unscanned = append(resp.Storage.Unscanned, unscannedBucketDTO{
				BackendID: u.BackendID,
				Bucket:    u.Bucket,
				FailedAt:  u.FailedAt.UTC().Format(time.RFC3339),
			})
		}
		// The dashboard reads from the quota cache, which today only holds
		// rows for buckets with a configured quota. Surface that limitation
		// so admins know totals exclude untracked buckets.
		resp.Storage.CacheNote = "Storage stats reflect only buckets with configured quotas whose last scan succeeded; buckets whose scan failed are listed separately. Set a quota or click recompute to populate."
	} else {
		resp.Storage.ByBackend = []backendStorageDTO{}
		resp.Storage.TopBuckets = []topBucketDTO{}
		resp.Storage.Unscanned = []unscannedBucketDTO{}
	}

	writeJSON(w, http.StatusOK, resp)
}
