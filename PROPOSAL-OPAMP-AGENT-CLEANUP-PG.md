# Proposal: Fix Postgres-incompatible cleanup query in OPAMP agent model

**Author:** adicks@synamedia.com
**Date:** 2026-06-12
**Status:** Proposed
**Target repo:** `github.com/synamedia-itdo/signoz`
**Branch:** continue on `feat/community-postgres-sqlstore` (one more small commit
in the same series) or a new short-lived branch -- either works
**Severity:** Cosmetic. Logs an error every cleanup cycle but does not affect
telemetry ingestion, queries, dashboards, or any user-visible behaviour.

---

## 1. The bug

After switching the metadata store to Postgres, the following error appears in
SigNoz's logs (roughly once per cleanup cycle):

```
failed to delete old agents
ERROR: for SELECT DISTINCT, ORDER BY expressions must appear in select list (SQLSTATE 42P10)
```

The cleanup function is at:
**`pkg/query-service/app/opamp/model/agent.go`** -- `(*Agent).KeepOnlyLast50Agents`
(currently at line ~73).

Its job: delete OPAMP-registered OTel collector records that aren't among the
50 most recently created. The current implementation builds this inner SELECT:

```go
agent.store.BunDB().
    NewSelect().
    ColumnExpr("distinct(agent_id)").
    Model(new(opamptypes.StorableAgent)).
    Where("org_id = ?", agent.OrgID).
    OrderExpr("created_at DESC").
    Limit(50)
```

which generates SQL equivalent to:

```sql
SELECT DISTINCT agent_id
FROM agent
WHERE org_id = ?
ORDER BY created_at DESC
LIMIT 50
```

SQLite accepts this. Postgres rejects it (SQLSTATE `42P10`) because the SQL
standard requires that **any column you `ORDER BY` must appear in the SELECT
list when `DISTINCT` is used**. SQLite is lenient on this; Postgres is not.

Net effect: the cleanup query fails, so old agent rows are never deleted. The
table accumulates indefinitely. For a demo or a single-collector deployment
this is invisible. For a long-running production with churning collectors it
would slowly bloat the table.

---

## 2. Schema observation

The `agent` table (verified against a live Postgres instance) has a UNIQUE
constraint on `agent_id`:

```
"agent_agent_id_key" UNIQUE CONSTRAINT, btree (agent_id)
```

So `DISTINCT` is **redundant** at the schema level -- there can only ever be
one row per `agent_id`. The original `DISTINCT` is defensive but not
load-bearing.

This gives us multiple valid fixes, ordered by simplicity:

| Fix | SQL | Pros | Cons |
|---|---|---|---|
| **A: Drop DISTINCT** | `SELECT agent_id FROM agent ORDER BY created_at DESC LIMIT 50` | Smallest diff, valid SQL on both engines | Implicitly relies on the UNIQUE constraint -- if a future migration drops it, duplicates could re-appear |
| **B: GROUP BY + MAX** | `SELECT agent_id FROM agent GROUP BY agent_id ORDER BY MAX(created_at) DESC LIMIT 50` | Portable, preserves the dedup intent even if the UNIQUE constraint is removed | Slightly more complex query, ORDER BY MAX(created_at) is non-obvious |
| **C: Add column to SELECT** | `SELECT DISTINCT agent_id, created_at FROM agent ORDER BY created_at DESC LIMIT 50` | Minimal change | Breaks the semantics -- DISTINCT now dedupes on `(agent_id, created_at)`, so rows with the same `agent_id` but different `created_at` could both appear; not equivalent |

**Recommendation: B (GROUP BY + MAX).** It's defensively portable, preserves
the original "deduplicate by agent_id" intent, and doesn't tightly couple this
code to the UNIQUE constraint on the table.

---

## 3. Implementation plan

### 3.1 Modify `pkg/query-service/app/opamp/model/agent.go`

Replace the inner SELECT in `KeepOnlyLast50Agents`:

```go
// BEFORE
agent.store.BunDB().
    NewSelect().
    ColumnExpr("distinct(agent_id)").
    Model(new(opamptypes.StorableAgent)).
    Where("org_id = ?", agent.OrgID).
    OrderExpr("created_at DESC").
    Limit(50)

// AFTER
agent.store.BunDB().
    NewSelect().
    ColumnExpr("agent_id").
    Model(new(opamptypes.StorableAgent)).
    Where("org_id = ?", agent.OrgID).
    GroupExpr("agent_id").
    OrderExpr("MAX(created_at) DESC").
    Limit(50)
```

(Verify the bun ORM method is `GroupExpr` -- I'm fairly sure it is; if not, the
equivalent might be `Group("agent_id")`. The exact bun call is the only thing
worth double-checking.)

### 3.2 Add a regression note (optional, recommended)

Above the inner SELECT, add a one-line comment explaining why we're not using
DISTINCT:

```go
// GROUP BY + MAX(created_at) (rather than SELECT DISTINCT ... ORDER BY
// created_at) so the query is valid on both SQLite (lenient) and Postgres
// (strict: requires ORDER BY columns to be in the SELECT list when DISTINCT
// is used).
```

This stops a future maintainer "simplifying" it back to DISTINCT.

---

## 4. Clean-room compliance

Trivially in scope -- we're modifying an existing MIT-licensed file. No `ee/`
file is involved. No reference material outside our own working tree, the
Postgres docs, and the bun ORM docs is needed.

---

## 5. Testing strategy

### 5.1 Unit / build

```bash
go build ./...
```

### 5.2 Smoke test against Postgres

1. Restart a SigNoz container against Postgres
2. Watch logs for ~2 minutes
3. The `failed to delete old agents` error should no longer appear
4. If you want to *prove* the cleanup is running and effective:
   - Pre-populate the `agent` table with 60 fake rows (via psql)
   - Restart SigNoz
   - After the cleanup runs, verify only 50 rows remain via
     `SELECT count(*) FROM agent;`

### 5.3 Regression test against SQLite

The new query must also work on SQLite. The simplest way is to
temporarily run a SigNoz instance with `SIGNOZ_SQLSTORE_PROVIDER=sqlite`
and confirm:

- No errors in logs
- Cleanup works (same pre-populate-then-count check, against the SQLite file)

---

## 6. Estimated effort

Tiny. **10-15 minutes** if everything goes smoothly:

- Code change: ~2 min (~3 lines diff)
- Local build + smoke test against Postgres: ~5 min
- Optional regression test against SQLite: ~5 min
- Commit + push: ~2 min

Suggested commit message:

```
fix(opamp): make agent cleanup query Postgres-compatible

The KeepOnlyLast50Agents inner SELECT used `SELECT DISTINCT agent_id ...
ORDER BY created_at`, which is valid in SQLite but rejected by Postgres
(SQLSTATE 42P10) because the SQL standard requires ORDER BY columns to
appear in the SELECT list when DISTINCT is used. The cleanup silently
failed under Postgres, leaving stale agent rows in the `agent` table.

Replace DISTINCT with GROUP BY agent_id and ORDER BY MAX(created_at) DESC.
This is portable across both engines and preserves the original "dedupe
by agent_id, keep newest" intent independently of the UNIQUE constraint
on the column.
```

---

## 7. Out of scope

A broader audit for other SQLite-only SQL patterns in the codebase would be
worthwhile but separate. Candidates worth a quick grep:

- `SELECT DISTINCT` with `ORDER BY` columns not in the select list (this is the
  most common SQLite-vs-Postgres trap)
- `||` for string concatenation that should be `CONCAT()` or vice versa
- `INSERT OR IGNORE` (SQLite) vs `ON CONFLICT DO NOTHING` (Postgres -- bun
  abstracts most of this, but worth checking raw SQL)
- `strftime()` (SQLite) vs `to_char()` (Postgres)
- Implicit type coercions (SQLite is very lenient, Postgres is strict)

That audit is a separate proposal if any meaningful number of issues turn up.
For now, this is the only one we've seen in practice, so just fix it.
