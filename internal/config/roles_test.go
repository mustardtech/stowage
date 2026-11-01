// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stowage-dev/stowage/internal/config"
)

func loadYAML(t *testing.T, body string) (config.Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stowage.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Load(p)
}

const (
	sqliteDB   = "db: {driver: sqlite, sqlite: {path: /tmp/x.db}}\n"
	postgresDB = "db: {driver: postgres, postgres: {dsn: \"postgres://u:p@h/d\"}}\n"
	allEnabled = "s3_proxy: {enabled: true, listen: \":8090\"}\noperator: {enabled: true}\n"
)

func serverWith(roles string) string {
	if roles == "" {
		return "server: {listen: \":8080\"}\n"
	}
	return "server: {listen: \":8080\", roles: [" + roles + "]}\n"
}

func TestRolesDefaultIsEverythingEnabled(t *testing.T) {
	cfg, err := loadYAML(t, serverWith("")+allEnabled+sqliteDB)
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.Roles()
	if !r.Dashboard || !r.Proxy || !r.Operator || cfg.Split() {
		t.Fatalf("default roles = %+v split=%v", r, cfg.Split())
	}
}

func TestRolesSplitNeedsPostgres(t *testing.T) {
	_, err := loadYAML(t, serverWith("proxy")+allEnabled+sqliteDB)
	if err == nil || !strings.Contains(err.Error(), "db.driver: postgres") {
		t.Fatalf("expected the postgres requirement, got %v", err)
	}
	cfg, err := loadYAML(t, serverWith("proxy")+allEnabled+postgresDB)
	if err != nil {
		t.Fatalf("postgres split should load: %v", err)
	}
	if r := cfg.Roles(); r.Dashboard || !r.Proxy || r.Operator || !cfg.Split() {
		t.Fatalf("roles = %+v split=%v", r, cfg.Split())
	}
	// Naming every enabled role is not a split, so SQLite is fine.
	if _, err := loadYAML(t, serverWith("dashboard, proxy, operator")+allEnabled+sqliteDB); err != nil {
		t.Fatalf("full role list on sqlite should load: %v", err)
	}
}

func TestRolesFromEnv(t *testing.T) {
	t.Setenv("STOWAGE_ROLES", "proxy,operator")
	cfg, err := loadYAML(t, serverWith("")+allEnabled+postgresDB)
	if err != nil {
		t.Fatal(err)
	}
	if r := cfg.Roles(); r.Dashboard || !r.Proxy || !r.Operator {
		t.Fatalf("roles from env = %+v", r)
	}
}

func TestRolesRejectUnknownAndDisabled(t *testing.T) {
	if _, err := loadYAML(t, serverWith("dashboard, scheduler")+allEnabled+postgresDB); err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Fatalf("unknown role: %v", err)
	}
	if _, err := loadYAML(t, serverWith("proxy")+"s3_proxy: {enabled: false}\n"+postgresDB); err == nil || !strings.Contains(err.Error(), "s3_proxy.enabled") {
		t.Fatalf("proxy role with proxy disabled: %v", err)
	}
	if _, err := loadYAML(t, serverWith("operator")+"operator: {enabled: false}\n"+postgresDB); err == nil || !strings.Contains(err.Error(), "operator.enabled") {
		t.Fatalf("operator role with operator disabled: %v", err)
	}
}
