# SSO is authoritative for roles (reconcile on every login)

**Status:** Implemented (fork) · **Decision date:** 2026-06-18

## Problem

SSO computed a user's role from their IdP groups on every login, but only
**applied** it when the user was first created (or a pending invite activated).
For an existing user, `GetOrCreateUser` returned them untouched
(`pkg/modules/user/impluser/setter.go`), so changing someone's Entra group
membership did **not** change their SigNoz role on subsequent logins. A user who
was ADMIN and then removed from the admin group stayed ADMIN — a stale-privilege
security gap.

## Decision

**SSO is authoritative for roles, fail-closed, with no separate toggle.**

- **Authoritative** — on every SSO login, the user's role is reconciled to the
  result of the role mapping. Manual role overrides in the SigNoz UI do not
  survive the next SSO login (by design).
- **Gated on "is a role mapping configured"** (`RoleMapping.IsAuthoritative()` =
  has group mappings or `useRoleAttribute`). No new config toggle: if you
  configure a mapping you opt into authoritative behaviour; if you don't, roles
  are left alone (so orgs that assign roles by hand are unaffected). A bare
  default role does **not** make SSO authoritative.
- **Fail-closed** — if the IdP sends no matching group (or no groups at all), the
  user is downgraded to the **Default role**. Security is favoured over
  availability of elevated access.

### Accepted trade-offs

- **Manual elevation can't persist** for SSO users in a mapped domain — the next
  login reverts to what the groups say. Intended.
- **A claim outage downgrades users.** If Entra stops emitting the `groups` claim
  (e.g. the overage misconfiguration in `ENTRA-GROUP-ROLE-MAPPING-SETUP.md`),
  affected users drop to the Default role until it's fixed. This is the
  consequence of fail-closed and is accepted; prevention lives at the Entra
  config layer (the runbook), not by weakening the reconcile.

## Implementation

- `authtypes.RoleMapping.IsAuthoritative()` — the gate.
- `user.Setter.SyncManagedRole(orgID, userID, managedRoleName)` (impl in
  `impluser/setter.go`) — reconciles a user's role to exactly one managed role:
  1. `authz.ModifyGrant(existing → [new])` — updates the **OpenFGA** grants, the
     actual enforcement (this is the step a naive `UpdateUserRoles`-only fix would
     have missed, making it a silent no-op);
  2. `UpdateUserRoles(...)` — syncs the `user_role` table;
  3. `tokenizer.DeleteIdentity(...)` — invalidates the cached identity so it takes
     effect immediately.
  It is a **no-op when the role already matches** (avoids authz churn and session
  disruption on every login) and **never touches the root user**.
- `implsession` SSO callback calls `SyncManagedRole` after user resolution when
  `roleMapping.IsAuthoritative()`.

This mirrors the existing admin "set role" recipe, so it inherits that path's
correctness.

## Scope notes

- Applies to all callback SSO providers (OIDC/Entra, SAML, Google) since the
  reconcile lives in the shared session callback.
- **Session longevity (separate axis, not addressed here):** an existing SigNoz
  session token persists (rotating) until idle/max expiry independent of the IdP;
  the role reconcile only runs at login. Continuous mid-session enforcement would
  be a separate change.

## Verification

- Unit tests (`authtypes/mapping_test.go`): `IsAuthoritative` matrix; fail-closed
  role resolution (matched / highest-of-multiple / unmapped→default / no
  groups→default / nil mapping).
- `go build` (incl. `./cmd/community/`), `go vet`, `gofmt` clean.
- `SyncManagedRole`'s execution path is validated by parity with the admin
  set-role flow (`ModifyGrant` + `UpdateUserRoles` + `DeleteIdentity`); there is
  no mock scaffolding in `impluser` for a full unit test, so end-to-end proof is
  a real login: change a user's Entra group, sign in, confirm the role changes.
