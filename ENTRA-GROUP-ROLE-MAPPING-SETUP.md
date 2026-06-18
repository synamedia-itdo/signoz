# Entra ID → SigNoz group/role mapping setup

How to make Microsoft Entra ID groups map to SigNoz roles (VIEWER / EDITOR /
ADMIN) over the community OIDC SSO. Written after a real debugging session — the
two non-obvious traps are called out explicitly.

---

## How the mapping works (so the config makes sense)

On login, SigNoz reads the **`groups` claim** from the Entra **ID token** and
matches each value against the keys you configure under *Role Mapping (Advanced)
→ Group to Role Mappings*. The match is **exact, case-sensitive string equality**
— no normalization. The highest-privilege matched role wins; if nothing matches,
the user gets the **Default role**.

Two consequences drive everything below:

1. **Entra emits group _Object IDs_ (GUIDs), not display names**, in the `groups`
   claim. So your mapping **keys must be the group Object IDs**, not names.
2. **Entra only emits the claim at all if it's configured _and_ stays under the
   overage limit.** Over the limit, the token carries an overage pointer
   (`hasgroups: true` / `_claim_names`) **instead of** the `groups` array — and
   SigNoz then sees no groups, so every user falls to the Default role.

---

## Step 1 — emit the groups claim (and avoid overage)

App Registration → **Token configuration** → **Add groups claim** (or Edit if it
exists).

The four options are **checkboxes, not radio buttons — selecting more than one is
a _union_.** For an org with many directory groups:

- ✅ Check **only** "Groups assigned to the application" *(recommended to avoid
  exceeding the limit)*.
- ❌ **Uncheck "Security groups"** — this is the trap. Leaving it checked makes
  the claim `Security groups ∪ Groups-assigned-to-the-application`, i.e. **all**
  the user's security groups. With hundreds of groups that blows the overage
  limit, the `groups` array is dropped, and role mapping silently fails for
  everyone. ("Groups assigned to the application" only helps if it's the *only*
  box checked.)
- ❌ Leave "Directory roles" and "All groups" unchecked.
- Tick the claim for the **ID token** (SigNoz reads the ID token).

**Verify what you actually saved** in App Registration → **Manifest**:

```jsonc
"groupMembershipClaims": "ApplicationGroup"   // correct
// NOT "SecurityGroup, ApplicationGroup"  ← the union trap (causes overage)
// NOT "All" / "SecurityGroup"
```

> Overage limits differ by flow: **200** for the authorization-code flow SigNoz
> uses, but only **6** for the implicit flow that jwt.ms uses. So a jwt.ms test
> can show overage (`hasgroups: true`) when the real SigNoz flow would be fine —
> don't be misled by it. Restricting to "Groups assigned to the application"
> keeps you well under both.

## Step 2 — assign the role groups to the application

Entra ID → **Enterprise applications** → *your app* → **Users and groups** →
assign the **groups** (the groups themselves, not just individual users — direct
user assignments do not appear in the groups claim). These assigned groups are
exactly what "Groups assigned to the application" emits.

## Step 3 — map Object IDs to roles in SigNoz

For each role group, get its **Object Id** from Entra ID → **Groups** → *group* →
*Object Id* (a GUID). Then in SigNoz → *Organization Settings → Authenticated
Domains → (OIDC) → Role Mapping (Advanced) → Group to Role Mappings*:

| Group (key) | Role (value) |
|---|---|
| `8f4e2a1b-…-c2a1` (Object ID) | `ADMIN` |
| `1b2c9d8e-…-7f6a` (Object ID) | `EDITOR` |

> The field is labelled generically and accepts free text, but for Entra the key
> **must be the group Object ID (GUID)** — a display name will never match the
> claim. Set a sensible **Default role** for users in none of the mapped groups.

---

## Verifying

- Easiest: **sign in to SigNoz** and check the role you land with. A normal login
  is sufficient — no stack rebuild needed.
- To inspect the raw claim, jwt.ms works but **mind the implicit 6-group overage**
  (above); add `&prompt=login` to force a fresh token.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Everyone gets the Default role | `groups` claim missing (overage) | Manifest `groupMembershipClaims` is a union (e.g. `"SecurityGroup, ApplicationGroup"`) → **uncheck Security groups** so it's `"ApplicationGroup"` only |
| `hasgroups: true` / `_claim_names` in the token | Too many groups emitted (overage) | Same as above — restrict to "Groups assigned to the application" |
| Groups present but still Default role | Mapping keyed by name | Use the group **Object ID (GUID)** as the key |
| Claim never appears even for small group sets | Claim not on the ID token, or groups not assigned to the Enterprise App | Tick the **ID token** box; assign the **groups** under Enterprise app → Users and groups |
