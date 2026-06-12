# Proposal: MIT-licensed Postgres SQLStore for SigNoz Community Edition

**Author:** adicks@synamedia.com
**Date:** 2026-06-12
**Status:** Proposed
**Target repo:** `github.com/synamedia-itdo/signoz` (the existing Synamedia fork)
**Related:** Builds on the same pattern as the OIDC callback work
(`feat/community-oidc` branch)

---

## 1. Goal

Add a Postgres SQLStore provider to the SigNoz **community edition** so that the
deployment can use a dedicated Postgres container for metadata (users, orgs,
dashboards, alerts, saved views) instead of an SQLite file. Stock community
edition only supports SQLite -- the Postgres provider lives in
`ee/sqlstore/postgressqlstore/` and is enterprise-licensed.

This is the second of two SigNoz patches that together make the demo stack
enterprise-ready without a paid licence (the first was native Entra OIDC SSO).

---

## 2. Why this is well-scoped

The SQLStore abstraction is clean. Each provider (SQLite, Postgres) implements
the same three-file pattern:

| File | Responsibility | SQLite lines (MIT) | Postgres lines (Enterprise) |
|---|---|---|---|
| `provider.go` | Open DB connection, wire bun ORM, register factory | 112 | 126 |
| `dialect.go` | Schema migration helpers (add/drop/rename columns, FK toggling, etc.) | 511 | 458 |
| `formatter.go` | Provider-specific SQL formatting | 107 | 154 |
| **Total** | | **730** | **738** |

The MIT-licensed SQLite implementation at `pkg/sqlstore/sqlitesqlstore/` is the
template. **Do not copy from `ee/sqlstore/postgressqlstore/`.** That is enterprise
code; we are writing a fresh MIT-licensed implementation that happens to do the
same job. Read the SQLite version, understand the interface, write equivalent
Postgres logic.

The good news: bun ORM already provides `pgdialect` (Postgres dialect) and bun's
schema operations work natively. Most of our work is the SigNoz-specific
`SQLDialect` interface methods, which are schema migration helpers wrapping bun
operations.

---

## 3. Implementation plan

### 3.1 New package: `pkg/sqlstore/postgressqlstore/`

Create three new files following the SQLite template structure:

#### `provider.go` (~120 lines)

Reference: `pkg/sqlstore/sqlitesqlstore/provider.go`. Use `pgxpool` instead of
the `modernc.org/sqlite` driver. Pattern (sketch, not a copy of `ee/`):

```go
package postgressqlstore

import (
    "context"
    "database/sql"

    "github.com/jackc/pgx/v5/pgconn"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/jackc/pgx/v5/stdlib"
    "github.com/uptrace/bun"
    "github.com/uptrace/bun/dialect/pgdialect"

    "github.com/SigNoz/signoz/pkg/errors"
    "github.com/SigNoz/signoz/pkg/factory"
    "github.com/SigNoz/signoz/pkg/sqlstore"
)

type provider struct {
    settings  factory.ScopedProviderSettings
    sqldb     *sql.DB
    bundb     *sqlstore.BunDB
    pgxPool   *pgxpool.Pool
    dialect   *dialect
    formatter sqlstore.SQLFormatter
}

func NewFactory(hookFactories ...factory.ProviderFactory[sqlstore.SQLStoreHook, sqlstore.Config]) factory.ProviderFactory[sqlstore.SQLStore, sqlstore.Config] {
    return factory.NewProviderFactory(factory.MustNewName("postgres"), func(ctx context.Context, providerSettings factory.ProviderSettings, config sqlstore.Config) (sqlstore.SQLStore, error) { /* ... */ })
}

func New(ctx context.Context, providerSettings factory.ProviderSettings, config sqlstore.Config, hooks ...sqlstore.SQLStoreHook) (sqlstore.SQLStore, error) {
    // 1. Parse DSN -> pgxpool.Config
    // 2. Apply MaxOpenConns from config.Connection
    // 3. Build pgxpool.Pool
    // 4. Wrap as *sql.DB via stdlib.OpenDBFromPool
    // 5. Wire bun with pgdialect.New()
    // 6. Return provider with dialect + formatter
}

// Implement the same interface methods as the SQLite provider:
// BunDB, SQLDB, Dialect, Formatter, BunDBCtx, RunInTxCtx,
// WrapNotFoundErrf, WrapAlreadyExistsErrf.
```

Postgres error wrapping uses `pgconn.PgError` codes:
- `23505` = unique violation
- `23503` = foreign key violation

Use these in `WrapAlreadyExistsErrf` instead of SQLite's constraint codes.

#### `dialect.go` (~400-500 lines)

Reference: `pkg/sqlstore/sqlitesqlstore/dialect.go`. Implement these
`sqlstore.SQLDialect` interface methods, swapping SQLite SQL for Postgres SQL:

| Method | SQLite approach | Postgres approach |
|---|---|---|
| `GetColumnType` | Query `pragma_table_info` | Query `information_schema.columns` |
| `IntToTimestamp` | Cast via temp table swap | `ALTER COLUMN ... TYPE TIMESTAMPTZ USING to_timestamp(...)` |
| `IntToBoolean` | Cast via temp table swap | `ALTER COLUMN ... TYPE BOOLEAN USING (... <> 0)` |
| `ColumnExists` | Query `pragma_table_info` | Query `information_schema.columns` |
| `AddColumn` | `ALTER TABLE ... ADD COLUMN` | Same syntax |
| `RenameColumn` | `ALTER TABLE ... RENAME COLUMN` | Same syntax |
| `DropColumn` | `ALTER TABLE ... DROP COLUMN` | Same syntax |
| `TableExists` | Query `sqlite_master` | Query `information_schema.tables` |
| `RenameTableAndModifyModel` | Table-swap dance (SQLite is limited) | `ALTER TABLE ... RENAME TO` + native column ops |
| `AddNotNullDefaultToColumn` | Add column with default, then NOT NULL | Same approach |
| `UpdatePrimaryKey` | Table-swap dance | `ALTER TABLE ... DROP CONSTRAINT ... ADD PRIMARY KEY ...` |
| `AddPrimaryKey` | Table-swap dance | `ALTER TABLE ... ADD PRIMARY KEY (...)` |
| `DropColumnWithForeignKeyConstraint` | Disable FK pragma, drop column | Find constraint via `information_schema`, drop it, then drop column |
| `ToggleForeignKeyConstraint` | `PRAGMA foreign_keys = ON/OFF` | `SET session_replication_role = ...` (use with care) |

The Postgres versions are generally **simpler** than SQLite versions because
Postgres supports full `ALTER TABLE` -- SQLite has historically required
table-swap dances.

#### `formatter.go` (~120 lines)

Reference: `pkg/sqlstore/sqlitesqlstore/formatter.go`. Implement
`sqlstore.SQLFormatter` for Postgres-specific quirks (identifier quoting with
double quotes, `LIMIT/OFFSET` ordering, etc.). The bun `pgdialect` handles most
of this -- thin wrapper.

### 3.2 Wire into the community server: `cmd/community/server.go`

Find the block that registers SQLStore provider factories. Currently it only
registers SQLite. Add Postgres:

```go
import (
    "github.com/SigNoz/signoz/pkg/sqlstore/sqlitesqlstore"
    "github.com/SigNoz/signoz/pkg/sqlstore/postgressqlstore"  // NEW
)

// ... in registerServer or wherever sqlstoreFactories is built:
sqlstoreFactories := factory.MustNewNamedMap(
    sqlitesqlstore.NewFactory(sqlstorehook.NewLoggingFactory(), sqlstorehook.NewInstrumentationFactory()),
    postgressqlstore.NewFactory(sqlstorehook.NewLoggingFactory(), sqlstorehook.NewInstrumentationFactory()),  // NEW
)
```

(The exact location depends on the current `server.go` structure -- look for
where the `SQLStoreProviderFactories` named map is built.)

The enterprise server already does this same registration. After our change, the
community build can use `SIGNOZ_SQLSTORE_PROVIDER=postgres`.

### 3.3 Migration code paths

The SQL migrations in `pkg/sqlmigration/` are mostly schema operations that go
through the `SQLDialect` interface. They should work transparently against
Postgres once the dialect is implemented correctly. But some migrations have
provider-specific paths (you'll see `switch dialect.(type)` or similar in some
files). Audit:

```bash
grep -rn "sqlitedialect\|pgdialect\|switch.*dialect" pkg/sqlmigration/
```

For each match, ensure the Postgres branch is correct or add one. The existing
enterprise build is the test of record -- their migrations definitely work
against Postgres -- so use the **migration logic** there as reference (without
copying enterprise code; the migration files themselves are mostly in `pkg/`
already).

---

## 4. Testing strategy

### 4.1 Unit tests

Mirror `pkg/sqlstore/sqlitesqlstore/formatter_test.go` for the new
`pkg/sqlstore/postgressqlstore/formatter_test.go`. Same test cases, expect
Postgres-quoted output.

For dialect tests, use the existing pattern -- spin up an in-test Postgres
(testcontainers-go) or use a `pgtest` library.

### 4.2 Integration test: fresh schema

From a clean Postgres database, start SigNoz with
`SIGNOZ_SQLSTORE_PROVIDER=postgres`. All migrations must run cleanly. Verify:

- `users`, `organizations`, `dashboards`, `rules`, `auth_domains`, `factor_password`,
  `reset_password_token`, `user_invite` and all other tables get created
- Login as the root user (`SIGNOZ_USER_ROOT_*`) works
- Create a dashboard, save it, restart container, dashboard persists

### 4.3 Integration test: existing SQLite -> Postgres

Not required for the demo, but ideal. If a user has a `signoz.db` SQLite file
and wants to migrate to Postgres, document the path (likely: export schema +
data via pgloader or a custom script). **Out of scope for the initial PR.**

### 4.4 OIDC compatibility

Once Postgres works, configure OIDC via the UI. Auth domain config is stored in
the `auth_domains` table -- ensure it round-trips through Postgres unchanged.
Verify Entra sign-in works.

---

## 5. Maintaining the fork

Same approach as the OIDC patch. The Postgres provider lives entirely in a new
package (`pkg/sqlstore/postgressqlstore/`), so rebasing against upstream won't
conflict with existing files. The only file we modify is `cmd/community/server.go`
(or wherever the factory map is built) -- one or two lines.

If upstream adds Postgres to community edition themselves (possible -- it's a
clear gap), we'd remove our package and use theirs.

---

## 6. License compliance

This is a **clean-room implementation**:

1. Read the SigNoz `SQLStore` interface (`pkg/sqlstore/`) -- MIT.
2. Read the **SQLite** implementation as a template -- MIT.
3. Read the **bun ORM** docs and Postgres dialect helpers -- third-party MIT.
4. Do not open files under `ee/sqlstore/postgressqlstore/`. Do not consult them.
   Do not paste from them. (The interface definition tells you what to
   implement; the SQL is documented in Postgres' own docs.)
5. Add a file header noting: `// Synamedia MIT implementation of the Postgres
   SQLStore provider for SigNoz community edition.`

A reasonable defence-in-depth measure: include `LICENSE-COMPLIANCE-POSTGRES.md`
in the PR (similar to the OIDC one) noting the clean-room approach and the
SQLite template.

---

## 7. Integration with the `signoz-stack` deployment repo

Currently the stack runs the SigNoz community image with
`SIGNOZ_SQLSTORE_PROVIDER=sqlite` (the only thing the community build supports
today). The Postgres container is still defined in `docker-compose.yaml` and
running, just unused.

After this work lands and a new image is built:

```yaml
# .env
SIGNOZ_VERSION=local
SIGNOZ_SQLSTORE_PROVIDER=postgres   # <-- flip this
```

```yaml
# docker-compose.yaml -- uncomment the postgres env block:
- SIGNOZ_SQLSTORE_PROVIDER=${SIGNOZ_SQLSTORE_PROVIDER:-sqlite}
- SIGNOZ_SQLSTORE_POSTGRES_DSN=postgres://${POSTGRES_USER:-signoz}:${POSTGRES_PASSWORD}@signoz-postgres:5432/${POSTGRES_DB:-signoz}?sslmode=disable
```

`make build-signoz && make restart-signoz` rolls it out. The Postgres container
is already running and waiting.

---

## 8. Checklist for the implementation session

- [ ] Branch from current head of `feat/community-oidc` (or `main` after merge):
      `feat/community-postgres-sqlstore`
- [ ] Read `pkg/sqlstore/sqlitesqlstore/provider.go` end to end
- [ ] Read `pkg/sqlstore/sqlitesqlstore/dialect.go` end to end
- [ ] Read `pkg/sqlstore/sqlitesqlstore/formatter.go` and its test
- [ ] Read the `SQLStore`, `SQLDialect`, `SQLFormatter` interface definitions
      in `pkg/sqlstore/`
- [ ] Create `pkg/sqlstore/postgressqlstore/` (do NOT consult `ee/` equivalent)
- [ ] Implement `provider.go`
- [ ] Implement `formatter.go` + tests
- [ ] Implement `dialect.go` (the heavy bit)
- [ ] Add Postgres registration in `cmd/community/server.go`
- [ ] `go build ./cmd/community/` -- must compile
- [ ] Audit `pkg/sqlmigration/` for provider-specific code paths
- [ ] Run all SigNoz migrations against a fresh Postgres -- all must apply
- [ ] Add `LICENSE-COMPLIANCE-POSTGRES.md`
- [ ] Build new image: from `signoz-stack/`, `make build-signoz`
- [ ] Flip `SIGNOZ_SQLSTORE_PROVIDER=postgres` in `.env` and restart signoz
- [ ] Verify the stack comes up healthy, dashboards persist across restarts
- [ ] Open PR against the Synamedia fork

---

## 9. Estimated effort

Several focused hours for a Go-experienced engineer. The shape of the work:

- `provider.go`: ~1 hour (mostly bun/pgxpool boilerplate, well-documented)
- `formatter.go`: ~1 hour (small surface, thin wrapper over `pgdialect`)
- `dialect.go`: ~3-4 hours (the SQL is the work; Postgres is well-documented
  but there are 14 interface methods)
- Testing + migration audit + image build + verify: ~2 hours

Total: roughly half a day to a day of focused work, similar to the OIDC
implementation.

---

## 10. Open questions

- **Connection pooling:** SigNoz uses `pgxpool` (not `database/sql`) for its
  pool. The `stdlib.OpenDBFromPool` adapter gives us a `*sql.DB` from a pool.
  Confirm this is the right approach by checking how community SQLite handles
  `MaxConnLifetime` -- it sets `sqldb.SetConnMaxLifetime`. We'd set the
  equivalent on the pool config.
- **TLS:** the DSN passed in the compose file uses `sslmode=disable` because
  Postgres and SigNoz share the same Docker network. For a future external
  Postgres (RDS, Aiven), document the `sslmode=verify-full` path.
- **Migration of existing demo data:** none. The Postgres container is fresh
  every time it's recreated in our setup. Production deployments would need
  thought, but this is out of scope.
