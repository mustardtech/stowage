# PostgreSQL store

Stowage's default store is a single SQLite file, which ties it to one
process. Point `db.driver` at PostgreSQL and any number of stowage
processes can share one database: the dashboard, several S3 proxy
replicas, and the operator all read and write the same users, sessions,
shares, audit trail, pins, dashboard-managed backends and credentials.

## Configuration

```yaml
db:
  driver: postgres
  postgres:
    # Prefer STOWAGE_POSTGRES_DSN so the password stays out of the file.
    dsn: postgres://stowage:secret@db.example:5432/stowage?sslmode=require
    max_open_conns: 16   # 0 = 16
```

Environment overrides: `STOWAGE_DB_DRIVER` and `STOWAGE_POSTGRES_DSN`.
The DSN is a libpq URL or `key=value` string; anything pgx accepts.

Helm: set `db.driver: postgres` and `db.postgres.existingSecret` to a
Secret whose `dsn` key (configurable via `db.postgres.dsnKey`) holds the
URL. The chart then renders no PersistentVolumeClaim.

## What the database needs

- PostgreSQL 14 or newer.
- A database owned by the stowage role. Migrations run at startup and
  create the `citext` extension (trusted, so the owner may create it);
  it gives `users.username` / `users.email` the case-insensitive
  equality and uniqueness SQLite provides with `COLLATE NOCASE`.
- `max_connections` comfortably above `max_open_conns` × the number of
  stowage processes.

## How the dialect works

There is one set of queries, written in SQLite's spelling. The store
rewrites `?` placeholders to `$n` on the way out and translates the
migration DDL (`AUTOINCREMENT`, `BLOB`, `TIMESTAMP`, `COLLATE NOCASE`)
when it runs against Postgres; the two semantic differences
(case-insensitive `LIKE`, case-insensitive username lookup) are
selected per dialect. `internal/store/sqlstore/dialect.go` is the whole
of it, and the store test suite runs against both engines in CI.

## Moving an existing SQLite database

Keep running SQLite until the Postgres database is reachable, then copy
the data with the `stowage migrate` subcommand (see its `--help`) while
stowage is stopped, switch `db.driver`, and start. The SQLite file is
not modified, so reverting is a config change.
