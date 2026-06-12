# Proposal: Postgres support for the community OpenFGA authz layer

**Author:** adicks@synamedia.com
**Date:** 2026-06-12
**Status:** Proposed
**Target repo:** `github.com/synamedia-itdo/signoz` (the existing Synamedia fork)
**Branch:** `feat/community-postgres-sqlstore` (continue here -- this is the
natural follow-up to commit `81d081dab`)
**Related:** Builds directly on the MIT Postgres SQLStore (commit `81d081dab`)
and the OIDC callback (`83ba90075`). Same clean-room approach.

---

## 1. Goal

Make the SigNoz community build actually startable with
`SIGNOZ_SQLSTORE_PROVIDER=postgres`.

The MIT Postgres SQLStore provider already added to `pkg/sqlstore/postgressqlstore/`
works correctly for the **metadata layer** -- migrations apply, connections
succeed, the bun dialect resolves cleanly. But on the very next step of server
startup, SigNoz also tries to bring up the **OpenFGA authorization layer**,
which needs its own SQL datastore. The community OpenFGA wrapper at
`pkg/authz/openfgaserver/sqlstore.go` only knows how to build a SQLite store --
it falls through to an error for any other dialect:

```
invalid store type: pg
```

(stack trace traced to `pkg/authz/openfgaserver/sqlstore.go:21`)

This proposal adds the Postgres case so the OpenFGA layer can also use the
shared Postgres connection.

---

## 2. Why this is small

OpenFGA is a third-party MIT-licensed library
(`github.com/openfga/openfga`). It already ships a Postgres datastore at
`github.com/openfga/openfga/pkg/storage/postgres`, with a `NewWithDB(...)`
constructor that takes a `*pgxpool.Pool`. The work is purely "plumb the pool
through":

| Component | Current state | Change needed |
|---|---|---|
| MIT Postgres SQLStore provider | Holds a private `*pgxpool.Pool` in the `provider` struct | Add a `Pool()` getter and a `Pooler` interface |
| MIT OpenFGA wrapper | `switch` covers only `sqlite` | Add a `case "pg"` branch that calls `openfga/postgres.NewWithDB` |

Total diff: ~15 lines across 2 files. No new dependencies (OpenFGA library is
already imported; pgxpool is already a transitive dep via our SQLStore).

---

## 3. Implementation plan

### 3.1 Modify `pkg/sqlstore/postgressqlstore/provider.go`

Expose the pool. Two additions:

```go
// At the package level (top of provider.go, alongside other types):
type Pooler interface {
    Pool() *pgxpool.Pool
}

// As a new method on *provider, alongside BunDB()/SQLDB()/etc:
func (p *provider) Pool() *pgxpool.Pool {
    return p.pool
}
```

Notes:
- The field name in the struct is whatever the current implementation calls it
  (likely `pool` -- verify in the file).
- The `Pooler` interface lives in this same package so consumers can do
  `store.(postgressqlstore.Pooler)`.
- Keep it minimal -- one method on the interface.

### 3.2 Modify `pkg/authz/openfgaserver/sqlstore.go`

Add the Postgres case. Read the current SQLite case as the template -- it's
right there at line ~15. The Postgres case has the same shape, just uses
OpenFGA's `postgres` package instead of `sqlite`.

```go
import (
    // ... existing imports
    "github.com/SigNoz/signoz/pkg/sqlstore/postgressqlstore"
    "github.com/openfga/openfga/pkg/storage/postgres"
)

func NewSQLStore(store sqlstore.SQLStore, config authz.Config) (storage.OpenFGADatastore, error) {
    switch store.BunDB().Dialect().Name().String() {
    case "sqlite":
        // ... existing sqlite case unchanged
    case "pg":
        pgStore, ok := store.(postgressqlstore.Pooler)
        if !ok {
            return nil, errors.Newf(errors.TypeInternal, errors.CodeInternal, "postgres sqlstore must implement Pooler")
        }
        return postgres.NewWithDB(pgStore.Pool(), nil, &sqlcommon.Config{
            MaxTuplesPerWriteField: config.OpenFGA.MaxTuplesPerWrite,
            MaxTypesPerModelField:  100,
        })
    }
    return nil, errors.Newf(...)  // existing fallthrough
}
```

The arguments to `postgres.NewWithDB`:

1. **`primaryDB *pgxpool.Pool`** -- from our `Pooler` getter. Sharing the same
   pool as the SQLStore means one connection pool for the whole process.
2. **`secondaryDB *pgxpool.Pool`** -- pass `nil`. This is OpenFGA's optional
   read replica; we don't have one.
3. **`cfg *sqlcommon.Config`** -- same shape as the SQLite case uses; mirror it.

### 3.3 Verify in `cmd/community/server.go`

The community server should already wire `openfgaserver.NewSQLStore` regardless
of dialect (the failure happens *inside* the function, not at the dispatch
site). No change needed here, but verify the file is unchanged after this work.

---

## 4. Clean-room compliance

The same rules from the previous two proposals apply:

1. **Do not open `ee/authz/openfgaserver/sqlstore.go`.** That file is
   enterprise-licensed and solves the same problem -- exactly the kind of
   tempting reference that would invalidate the clean-room claim.
2. **Public sources allowed:**
   - The existing SQLite case in `pkg/authz/openfgaserver/sqlstore.go` (MIT,
     same file you're editing).
   - The OpenFGA library's own public Go docs and source at
     `github.com/openfga/openfga/pkg/storage/postgres`. Read its
     `NewWithDB` signature, copy the call shape.
   - The bun dialect names (`"pg"` for Postgres) are documented in bun's docs.
3. **Add a brief comment** in the new case noting it's the Synamedia clean-room
   addition -- aids future code archaeology.

A reasonable defence-in-depth measure: extend the existing
`LICENSE-COMPLIANCE-POSTGRES.md` (added with `81d081dab`) with a section noting
this follow-up patch, or add a one-paragraph note to the commit message.

---

## 5. Testing strategy

### 5.1 Unit / build

```bash
go build ./cmd/community/   # must compile
go vet ./pkg/authz/openfgaserver/ ./pkg/sqlstore/postgressqlstore/
```

### 5.2 Type-assertion sanity check

The new code does `store.(postgressqlstore.Pooler)`. Add a single test or just
a compile-time `var _ Pooler = (*provider)(nil)` line in the postgressqlstore
package to guarantee the type assertion will succeed at runtime.

### 5.3 Integration -- the real test

This is the test the deployment will run, but worth scripting locally too:

1. Wipe the Postgres database (delete all tables) to test a fresh schema build
2. Start a SigNoz community container against Postgres
3. Verify it gets past **both** the SQLStore migration phase **and** the
   OpenFGA datastore initialisation (i.e., the server actually starts, healthy,
   listening on :8080)
4. Log in as the root user, create a dashboard, restart container, dashboard
   persists
5. (Optional) Verify OpenFGA tuples are being written to Postgres tables
   (table names start with `openfga_`)

---

## 6. Integration with the `signoz-stack` deployment repo

The deployment repo already has the env vars pre-wired and commented out, with
a note explaining exactly this blocker (see `services.signoz.environment` in
`docker-compose.yaml`). After this work lands and a new image is built, the
deploy-side flip is two lines uncommented:

```yaml
- SIGNOZ_SQLSTORE_PROVIDER=postgres
- SIGNOZ_SQLSTORE_POSTGRES_DSN=postgres://...
```

Then `make build-signoz && docker compose up -d --force-recreate signoz` and
we're on Postgres end-to-end.

---

## 7. Checklist for the implementation session

- [ ] Confirm working tree clean, branch is `feat/community-postgres-sqlstore`
- [ ] Read `pkg/sqlstore/postgressqlstore/provider.go` end to end
- [ ] Read `pkg/authz/openfgaserver/sqlstore.go` end to end (the **`pkg/`** one)
- [ ] Read OpenFGA library: `vendor/github.com/openfga/openfga/pkg/storage/postgres/postgres.go`
      (or wherever your module cache resolves it) -- look at the `NewWithDB`
      function signature
- [ ] **Do NOT open `ee/authz/openfgaserver/sqlstore.go`**
- [ ] Modify `pkg/sqlstore/postgressqlstore/provider.go`: add `Pooler`
      interface, add `Pool()` method
- [ ] Modify `pkg/authz/openfgaserver/sqlstore.go`: add `case "pg"` branch
- [ ] `go build ./cmd/community/` -- compiles
- [ ] Smoke test locally: start fresh Postgres, point a SigNoz binary at it,
      see it start cleanly
- [ ] Commit: `feat(authz): support Postgres in community OpenFGA datastore`
      with a body explaining the why
- [ ] (Optional) Update `LICENSE-COMPLIANCE-POSTGRES.md` to mention this patch
- [ ] Push branch -- ready for the deployment session to rebuild

---

## 8. Open questions

- **Pool sharing semantics:** OpenFGA and the metadata SQLStore will share the
  same `*pgxpool.Pool`. This is intentional and standard practice -- pgxpool
  handles concurrent use safely. If we wanted to isolate them in future, we
  could split into two pools, but there's no reason to today.
- **MaxConns:** the existing `sqlstore.Config.Connection.MaxOpenConns` already
  controls the pool size. OpenFGA uses the same pool, so its concurrency is
  bounded by the same setting. Default 100 -- generous for a demo.
- **OpenFGA-side migrations:** OpenFGA's Postgres adapter handles its own table
  creation lazily on first use. No separate migration step needed.

---

## 9. Estimated effort

Significantly smaller than the previous two proposals. A focused
30-60 minutes:

- Code change: ~5 min (it really is ~15 lines)
- Smoke test against fresh Postgres: ~15 min
- Commit message + push: ~5 min
- Buffer for surprises: ~15 min

If anything takes longer than this estimate, something unexpected has surfaced
and is worth flagging back to this session for triage.
