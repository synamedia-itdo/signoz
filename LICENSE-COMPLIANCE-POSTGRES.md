# License-Compliance Evidence: Community Postgres SQLStore

**Author:** adicks@synamedia.com
**Date:** 2026-06-12
**Branch:** `feat/community-postgres-sqlstore`
**Purpose:** Record the verification done before implementing a community-edition
Postgres metadata store, demonstrating the new code is entirely MIT-licensed and
clean-room (not derived from `ee/`), and documenting a **material scope
correction** to the proposal discovered during verification.

> Same auditable-record discipline as `LICENSE-COMPLIANCE-OIDC.md`. Every claim
> was verified against the source tree on the date above with line references.

---

## 1. License boundary (unchanged from the OIDC record)

The root `LICENSE` (`LICENSE:1-25`) splits the repo by directory:

- `ee/` and `cmd/enterprise/` → **SigNoz Enterprise License** (`ee/LICENSE`).
- Everything else → **MIT ("MIT Expat")**.
- Licensing is **directory-based**; there are **no per-file license headers**.

Our new packages live under `pkg/` → MIT by location.

---

## 2. Component inventory (verified)

| Component | Location | License |
|---|---|---|
| `SQLStore` / `SQLDialect` / `SQLFormatter` interfaces | `pkg/sqlstore/sqlstore.go:13-122` | MIT |
| `sqlstore.Config` incl. `PostgresConfig{DSN}` | `pkg/sqlstore/config.go:9-23` | MIT |
| `BunDB` tx/ctx wrapper | `pkg/sqlstore/bun.go` | MIT |
| **SQLite SQLStore impl (our template)** | `pkg/sqlstore/sqlitesqlstore/` | **MIT** |
| `sqlschema.SQLSchema` / `SQLOperator` / `SQLFormatter` | `pkg/sqlschema/sqlschema.go:9-82` | MIT |
| Generic `Operator` (shared by all dialects) | `pkg/sqlschema/operator.go` | MIT |
| Schema model (`Table`/`Column`/`Constraint`/`Index`) | `pkg/sqlschema/{table,column,constraint,index}.go` | MIT |
| Base `Formatter` | `pkg/sqlschema/formatter.go` | MIT |
| **SQLite SQLSchema impl (our template)** | `pkg/sqlschema/sqlitesqlschema/` | **MIT** |
| All schema migrations | `pkg/sqlmigration/*.go` | MIT |
| Community provider registration | `cmd/community/metastore.go` | MIT |
| Postgres SQLStore impl (Enterprise — *not used*) | `ee/sqlstore/postgressqlstore/` | **Enterprise** |
| Postgres SQLSchema impl (Enterprise — *not used*) | `ee/sqlschema/postgressqlschema/` | **Enterprise** |

The community server registers only the SQLite factories
(`pkg/signoz/provider.go:104-114`); `cmd/community/metastore.go` returns those
unchanged. The enterprise server adds the two Postgres factories on top
(`cmd/enterprise/metastore.go:13-29`). `pgx/v5`, `bun`, and `bun/dialect/pgdialect`
are already direct dependencies (`go.mod`), so no new external dependency is
introduced.

---

## 3. Clean-room statement

The new packages are authored from MIT sources only:

- **Templates:** `pkg/sqlstore/sqlitesqlstore/` and `pkg/sqlschema/sqlitesqlschema/`
  (both MIT), the MIT interface/model definitions, and the public `bun` /
  `pgx` libraries (third-party, permissive).
- **The Enterprise Postgres packages were NOT used as implementation
  references.** To establish the license boundary for this record, only the
  *first ~30 lines* (package clause + import block + the exported struct/factory
  signatures) of `ee/sqlstore/postgressqlstore/provider.go` and
  `ee/sqlschema/postgressqlschema/provider.go` were viewed — enough to confirm
  they reside under `ee/` and to match the factory **signatures** the wiring must
  call. Their method bodies (the copyrightable SQL/introspection logic) were
  **not read** and are **not** the basis for the new code. The Postgres SQL and
  catalog queries were written from Postgres' own documented `information_schema`
  / `pg_catalog` and standard DDL.
- **Location = license:** new files under `pkg/` are MIT. No header added
  (matching repo convention).

---

## 4. MATERIAL SCOPE CORRECTION vs. PROPOSAL-POSTGRES-SQLSTORE.md

Verification found the proposal **understates the work by roughly 2×** and gets
two structural facts wrong. These corrections drive the implementation:

1. **Two providers are required, not one.** The metadata layer has *two*
   abstractions, and a migration run uses both:
   - `sqlstore.SQLDialect` (legacy schema helpers) — used by **26** migrations.
   - `sqlschema.SQLSchema`/`SQLOperator` (newer) — used by **40** migrations.
   The proposal only covers `postgressqlstore`. We **also** need
   `pkg/sqlschema/postgressqlschema/`. `cmd/metastore.go:138` selects the
   sqlschema provider by **`config.SQLStore.Provider`**, so
   `SIGNOZ_SQLSTORE_PROVIDER=postgres` requires *both* a `postgres` sqlstore
   factory and a `postgres` sqlschema factory.

2. **Wiring location is wrong.** The proposal edits `cmd/community/server.go`.
   The factory map is actually built in `cmd/community/metastore.go`
   (`signoz.NewSQLStoreProviderFactories()` / `NewSQLSchemaProviderFactories()`).
   We must register the Postgres factories there, mirroring
   `cmd/enterprise/metastore.go`. We must **NOT** add them to the shared
   `pkg/signoz/provider.go`, because the enterprise build calls `.Add(ee postgres)`
   on top of that shared map — a shared `postgres` entry would cause a
   duplicate-name panic in the enterprise build.

3. **No migration edits needed (proposal §3.3 is both unnecessary and a
   clean-room hazard).** Migrations already branch on the backend via bun's
   `Dialect().Name()` (`dialect.PG` vs `dialect.SQLite`; e.g.
   `054_update_authz.go:41`, `060`, `061`, `078`, `081`, `011`, `012`). Wiring
   bun with `pgdialect` activates those branches automatically. §3.3's
   suggestion to use the enterprise migration logic "as reference" is rejected —
   it contradicts the clean-room stance in §6 and is not needed.

4. **Risk is lower than the size suggests, in one specific way.** Audit of all
   migrations shows the `sqlschema` operator methods actually invoked are only:
   `CreateTable`, `CreateIndex`, `DropTable`, `AddColumn`, `DropColumn`,
   `DropIndex`, `DropConstraint`. **No migration calls `AlterTable` / `AlterColumn`
   / `RecreateTable`.** Therefore the most fragile part of a Postgres provider —
   the column-level introspection round-trip where `SQLDataTypeOf` must exactly
   reverse `DataTypeOf` or every column appears "changed" — is **not exercised**.
   `GetTable` still needs accurate **names** (columns, primary-key name, unique/
   foreign-key constraint names) because `DropConstraint`/`AddColumn` depend on
   them, but exact type/default fidelity is not on the critical path.

5. **Capability flags.** Unlike SQLite (`OperatorSupport{false,false,false}`,
   forcing table-recreate dances), Postgres supports native `ALTER TABLE`, so the
   Postgres sqlschema provider uses `OperatorSupport{true,true,true}`.

### Resulting implementation surface

- `pkg/sqlstore/postgressqlstore/`: `provider.go` (pgx stdlib + bun pgdialect, PG
  error codes `23505`/`23503`), `dialect.go` (14 `SQLDialect` methods in Postgres
  DDL), `formatter.go` (+ `formatter_test.go`).
- `pkg/sqlschema/postgressqlschema/`: `provider.go` (`GetTable`/`GetIndices` via
  `information_schema`/`pg_catalog`, `ToggleFKEnforcement` via
  `session_replication_role`, generic `Operator` with PG capabilities),
  `formatter.go` (PG datatype mapping).
- `cmd/community/metastore.go`: register both Postgres factories.

---

## 5. Verification of record

The correctness test is running the **full `pkg/sqlmigration` chain against a
fresh Postgres** (ephemeral container) with `SIGNOZ_SQLSTORE_PROVIDER=postgres`,
then confirming the app's tables are created and it boots. Build must also pass
with the repo-pinned Go toolchain (`go 1.25.7`; the host's newer Go breaks a
transitive dep — see `LICENSE-COMPLIANCE-OIDC.md` §5 note).

### Results (run 2026-06-12)

- **Build:** `GOTOOLCHAIN=go1.25.7 go build ./cmd/community/` → exit 0.
- **Unit:** `go test ./pkg/sqlstore/postgressqlstore/` (formatter) → ok.
- **Migration chain:** `./community metastore migrate sync up` against
  `postgres:16` →
  `migrated to group #1 (93 migrations (000 ... 093))`, exit 0. All 93
  migrations applied — covering both the `SQLDialect` path (`postgressqlstore`)
  and the `sqlschema` path (`postgressqlschema`).
- **Schema:** 50 tables created (`users`, `organizations`, `dashboard`, `rule`,
  `auth_domain`, `factor_password`, `reset_password_token`, `saved_views`,
  `pipelines`, `notification_channel`, …); 50 primary keys, 45 foreign keys,
  9 unique constraints. The `users` table verified with `id text` PK, correct
  column types, a **partial unique index**
  (`... WHERE status <> 'deleted'`), and an FK to `organizations` — exercising
  the constraint/partial-index introspection in `GetTable`/`GetIndices`.
- **Idempotency:** a second `migrate sync up` exited 0 with no further changes.
- **Non-fatal noise:** migrations `046`/`066` log WARN/ERROR while probing the
  telemetry store (ClickHouse, port 9000) which was not running for this test;
  they are written to warn-and-continue and do not affect the Postgres metadata
  schema. Unrelated to this change.

**Conclusion: verified.** A fresh Postgres database migrates cleanly end-to-end
under the community build with `SIGNOZ_SQLSTORE_PROVIDER=postgres`.

---

## 6. Follow-up: Postgres support for the community OpenFGA datastore

Migrating the metadata schema was necessary but not sufficient to *start* the
community server on Postgres. On startup, after the SQLStore comes up, SigNoz
initialises the **OpenFGA authorization datastore** via the MIT wrapper
`pkg/authz/openfgaserver/sqlstore.go`, which only handled the `sqlite` dialect
and returned `invalid store type: pg` for anything else. This follow-up adds the
Postgres case.

**Change (2 files, ~20 lines):**

- `pkg/sqlstore/postgressqlstore/provider.go` — add a `Pooler` interface
  (`Pool() *pgxpool.Pool`), implement it on the provider, and a compile-time
  `var _ Pooler = (*provider)(nil)` assertion. Needed because OpenFGA's Postgres
  datastore is built on a `*pgxpool.Pool` (`postgres.NewWithDB(primary, secondary
  *pgxpool.Pool, cfg)`), whereas the SQLite datastore takes a `*sql.DB`
  (`sqlite.NewWithDB(*sql.DB, cfg)`) — verified against `openfga v1.14.1`.
- `pkg/authz/openfgaserver/sqlstore.go` — add `case "pg"` that type-asserts the
  SQLStore to `postgressqlstore.Pooler` and calls
  `postgres.NewWithDB(pooler.Pool(), nil, cfg)`, sharing the metadata pool (no
  second pool; the read-replica arg is `nil`).

**License/clean-room:** both files are MIT (under `pkg/`). The enterprise
equivalent `ee/authz/openfgaserver/sqlstore.go` was **not opened**. The
implementation was written from the existing MIT SQLite case in the same file,
the public OpenFGA library API (`postgres.NewWithDB`), and bun's documented
dialect name (`"pg"`). No new dependencies (OpenFGA and pgxpool were already
present). The new case carries a comment marking it as the Synamedia clean-room
addition.

**OpenFGA schema:** the OpenFGA tables (`store`, `authorization_model`, `tuple`,
`changelog`) are created by SigNoz's own migrations — they were already produced
by the §5 migration run — so no separate OpenFGA migration step is required.

**Verification (run 2026-06-12):**

- **Build/vet:** `go build`/`go vet` of `pkg/sqlstore/postgressqlstore`,
  `pkg/authz/openfgaserver`, and `./cmd/community/` → exit 0 (incl. the
  compile-time `Pooler` assertion).
- **Datastore smoke test:** against the migrated `postgres:16`, a temporary gated
  test built a real `postgressqlstore` and called `openfgaserver.NewSQLStore` (the
  new `pg` branch) → returned a non-nil datastore; a real `ListStores` query
  against the migrated `store` table succeeded. (The throwaway test was removed
  after running; it is not committed.)
- **No regression:** the existing `openfgaserver` SQLite tests still pass.
- **Note on `IsReady`:** OpenFGA's `IsReady` reports `false` here because it
  checks OpenFGA's *own* migration-version bookkeeping, which SigNoz bypasses by
  owning the schema. This is benign — `IsReady` is not referenced anywhere in
  SigNoz's authz code, so server startup does not gate on it.

**Still unverified (environment limit):** a full server boot on Postgres
(listening on :8080 with all dependencies incl. ClickHouse) was not run here; the
datastore-construction + live-query smoke test is the closest proxy. This is the
final code piece — the remaining check is the deployment-side end-to-end boot.

---

## 6. Conclusion

The implementation adds two new MIT packages under `pkg/` plus an additive edit
to `cmd/community/metastore.go`. No Enterprise file is copied, modified, or
required at build or run time. The work is consistent with the repository's
dual-license structure. The scope is ~2× the proposal (two providers, not one),
which is recorded here so the effort and the corrected plan are auditable.
