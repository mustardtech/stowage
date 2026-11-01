// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlstore

import (
	"strings"
	"testing"
)

func TestRewritePlaceholders(t *testing.T) {
	cases := map[string]string{
		"SELECT 1":                              "SELECT 1",
		"SELECT * FROM t WHERE a = ? AND b = ?": "SELECT * FROM t WHERE a = $1 AND b = $2",
		"INSERT INTO t(a) VALUES (?)":           "INSERT INTO t(a) VALUES ($1)",
		"x IN (?, ?, ?) LIMIT ? OFFSET ?":       "x IN ($1, $2, $3) LIMIT $4 OFFSET $5",
	}
	for in, want := range cases {
		if got := DialectPostgres.rewrite(in); got != want {
			t.Errorf("postgres rewrite(%q) = %q, want %q", in, got, want)
		}
		if got := DialectSQLite.rewrite(in); got != in {
			t.Errorf("sqlite rewrite(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestPostgresDDLTranslation(t *testing.T) {
	for _, m := range migrations {
		out := DialectPostgres.ddl(m.SQL)
		for _, bad := range []string{"NOCASE", "AUTOINCREMENT", "BLOB", "TIMESTAMPTZTZ"} {
			if strings.Contains(out, bad) {
				t.Errorf("migration %d (%s): %q survived translation", m.Version, m.Name, bad)
			}
		}
		if strings.Contains(m.SQL, "TIMESTAMP") && !strings.Contains(out, "TIMESTAMPTZ") {
			t.Errorf("migration %d (%s): TIMESTAMP not widened to TIMESTAMPTZ", m.Version, m.Name)
		}
		if DialectSQLite.ddl(m.SQL) != m.SQL {
			t.Errorf("migration %d (%s): sqlite ddl must be untouched", m.Version, m.Name)
		}
	}
	if got := DialectPostgres.ddl("applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP"); got != "applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP" {
		t.Errorf("CURRENT_TIMESTAMP mangled: %q", got)
	}
	if got := DialectPostgres.ddl("username TEXT NOT NULL COLLATE NOCASE,\n  email TEXT COLLATE NOCASE,"); got != "username CITEXT NOT NULL,\n  email CITEXT," {
		t.Errorf("citext translation: %q", got)
	}
}

func TestDialectOperators(t *testing.T) {
	if DialectSQLite.like() != "LIKE" || DialectPostgres.like() != "ILIKE" {
		t.Error("like operator")
	}
	if DialectSQLite.nocase() != " COLLATE NOCASE" || DialectPostgres.nocase() != "" {
		t.Error("nocase suffix")
	}
	if DialectSQLite.String() != "sqlite" || DialectPostgres.String() != "postgres" {
		t.Error("String")
	}
}
