// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package config_test

import (
	"strings"
	"testing"

	"github.com/stowage-dev/stowage/internal/config"
)

func TestPostgresDriverNeedsDSN(t *testing.T) {
	t.Setenv("STOWAGE_DB_DRIVER", "postgres")
	t.Setenv("STOWAGE_POSTGRES_DSN", "")
	if _, err := config.Load(""); err == nil || !strings.Contains(err.Error(), "db.postgres.dsn") {
		t.Fatalf("expected a dsn-required error, got %v", err)
	}
}

func TestPostgresDriverFromEnv(t *testing.T) {
	t.Setenv("STOWAGE_DB_DRIVER", "postgres")
	t.Setenv("STOWAGE_POSTGRES_DSN", "postgres://u:p@db:5432/stowage?sslmode=require")
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DB.Driver != "postgres" || cfg.DB.Postgres.DSN == "" {
		t.Fatalf("env override not applied: %+v", cfg.DB)
	}
}
