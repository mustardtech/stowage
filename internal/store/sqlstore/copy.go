// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlstore

import (
	"context"
	"fmt"
	"strings"
)

// copyTables is every table a Store owns, parents before children so
// foreign keys (pw_reset_tokens → users) hold while copying. Append here
// when a migration adds a table.
var copyTables = []string{
	"users",
	"sessions",
	"pw_reset_tokens",
	"shares",
	"bucket_quotas",
	"audit_events",
	"bucket_pins",
	"backends",
	"bucket_size_tracking",
	"s3_credentials",
	"s3_anonymous_bindings",
	"s3_bucket_cors",
}

// CopyReport is what CopyTo moved, per table.
type CopyReport struct {
	Rows map[string]int64
}

// CopyTo copies every row of every table from src into dst. Both stores
// must be at the same schema version and every dst table must be empty;
// the copy runs in one dst transaction so either all of it lands or none
// of it does. src is only read. This is the SQLite → Postgres move behind
// `stowage migrate`, but it is dialect-agnostic in both directions.
func CopyTo(ctx context.Context, src, dst *Store) (*CopyReport, error) {
	if src == dst {
		return nil, fmt.Errorf("copy: source and destination are the same store")
	}
	srcVer, err := src.schemaVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("copy: source schema version: %w", err)
	}
	dstVer, err := dst.schemaVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("copy: destination schema version: %w", err)
	}
	if srcVer != dstVer {
		return nil, fmt.Errorf("copy: schema version mismatch: source %d, destination %d", srcVer, dstVer)
	}

	tx, err := dst.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	report := &CopyReport{Rows: make(map[string]int64, len(copyTables))}
	for _, table := range copyTables {
		var existing int64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(1) FROM "+table).Scan(&existing); err != nil {
			return nil, fmt.Errorf("copy: count %s: %w", table, err)
		}
		if existing > 0 {
			return nil, fmt.Errorf("copy: destination table %s is not empty (%d rows)", table, existing)
		}
		n, err := copyTable(ctx, src, tx, table)
		if err != nil {
			return nil, fmt.Errorf("copy: %s: %w", table, err)
		}
		report.Rows[table] = n
	}

	if dst.dialect == DialectPostgres {
		// audit_events.id is an identity column; explicit ids were copied,
		// so move the sequence past them or the next insert collides.
		if _, err := tx.ExecContext(ctx,
			`SELECT setval(pg_get_serial_sequence('audit_events', 'id'), COALESCE((SELECT MAX(id) FROM audit_events), 0) + 1, false)`,
		); err != nil {
			return nil, fmt.Errorf("copy: reset audit_events sequence: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return report, nil
}

func copyTable(ctx context.Context, src *Store, tx *Tx, table string) (int64, error) {
	rows, err := src.R.QueryContext(ctx, "SELECT * FROM "+table)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(cols)), ",")
	stmt, err := tx.PrepareContext(ctx,
		"INSERT INTO "+table+" ("+strings.Join(cols, ", ")+") VALUES ("+marks+")")
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var n int64
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		if _, err := stmt.ExecContext(ctx, vals...); err != nil {
			return n, fmt.Errorf("row %d: %w", n+1, err)
		}
		n++
	}
	return n, rows.Err()
}

// schemaVersion is the highest applied migration.
func (s *Store) schemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.R.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&v)
	return v, err
}
