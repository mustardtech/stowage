// Copyright (C) 2026 Damian van der Merwe
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/stowage-dev/stowage/internal/store/sqlstore"
)

// runMigrate copies a SQLite store into a PostgreSQL database so a
// deployment can switch db.driver without losing users, shares, audit
// history or dashboard-managed backends. Stop stowage first: the copy
// refuses a non-empty destination and does not lock the source.
func runMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	sqlitePath := fs.String("sqlite", "", "path to the SQLite database to copy from (required)")
	pgDSN := fs.String("postgres", os.Getenv("STOWAGE_POSTGRES_DSN"), "PostgreSQL DSN to copy into (default: $STOWAGE_POSTGRES_DSN)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: stowage migrate --sqlite /data/stowage.db --postgres postgres://...")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Copies every table from the SQLite store into an empty PostgreSQL")
		fmt.Fprintln(os.Stderr, "database (migrations run there first). Stop stowage before running.")
		fmt.Fprintln(os.Stderr, "")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sqlitePath == "" || *pgDSN == "" {
		fs.Usage()
		return errors.New("--sqlite and --postgres (or STOWAGE_POSTGRES_DSN) are required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	src, err := sqlstore.Open(ctx, *sqlitePath)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer src.Close()
	dst, err := sqlstore.OpenPostgres(ctx, *pgDSN, 2)
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	defer dst.Close()

	report, err := sqlstore.CopyTo(ctx, src, dst)
	if err != nil {
		return err
	}
	var total int64
	for _, table := range []string{"users", "sessions", "pw_reset_tokens", "shares", "bucket_quotas", "audit_events", "bucket_pins", "backends", "bucket_size_tracking", "s3_credentials", "s3_anonymous_bindings", "s3_bucket_cors"} {
		fmt.Printf("%-24s %d\n", table, report.Rows[table])
		total += report.Rows[table]
	}
	fmt.Printf("%-24s %d\ncopied; set db.driver: postgres and restart stowage\n", "total", total)
	return nil
}
