# Proposal: Loopback-redirect SSO so an MCP server can hold a SigNoz session "as me"

**Author:** adicks@synamedia.com
**Date:** 2026-06-17
**Status:** Proposed
**Target repo:** `github.com/synamedia-itdo/signoz` (the Synamedia fork)
**Branch:** new short-lived branch, e.g. `feat/community-loopback-sso`
**Related:** Builds on the native OIDC work (`83ba90075`) and the same clean-room
discipline as the OIDC / Postgres / OpenFGA proposals.
**Severity / risk:** Security-sensitive. This changes how an authenticated
session token is delivered, so the guardrails in §5 are **requirements, not
suggestions**.

---

## 1. Goal

Let a locally-running **MCP server** (which already exposes a loopback HTTP
listener on a small, fixed set of `127.0.0.1` ports) obtain a **SigNoz session
token for the human operator** via interactive SSO — no shared password, no
browser-resident copy/paste, no service account. The MCP server:

1. starts its loopback listener,
2. triggers an interactive Entra ID login in the browser (the human authenticates
   **as themselves**),
3. receives the freshly minted **SigNoz session JWT** at its loopback endpoint,
4. holds it in memory, attaches it as `Authorization: Bearer …` on SigNoz API
   calls, and **rotates** it in the background until the process restarts or the
   session reaches its hard maximum lifetime.

This is the OAuth 2.0 / RFC 8252 "native app loopback redirect" pattern applied
to SigNoz's own session-token issuance.

---

## 2. Why "as me" (and not a service account)

When an LLM drives the SigNoz API through an MCP server, **the human is the
actor** — the model is a tool. Calls should therefore be attributed to, and
authorised as, the human:

- **Correctness / accountability:** actions (including mistakes) are logged and
  authorised as the operator, not an opaque robot identity.
- **No privilege drift:** a service account is a *second* identity whose RBAC
  must be kept aligned with the operator's — double maintenance and a standing
  risk of over- or under-grant. "As me" has exactly the operator's rights, always.
- **Scope is controlled at the right layer:** what the LLM may do is bounded by
  the *tools the MCP server exposes*, not by under-provisioning a shared
  principal.

SigNoz's session JWT is already a *user-principal* token
(`pkg/modules/session/implsession/module.go:163`,
`authtypes.NewPrincipalUserIdentity(...)`), so this goal is natural — the only
problem is *delivery* (§3).

---

## 3. The blocker: redirect_uri and token-delivery are fused to one URL

SigNoz is the OIDC **Relying Party**. Its API accepts only a **SigNoz session
JWT** (`Authorization: Bearer`) or a **service-account API key**
(`SIGNOZ-API-KEY`) — never a raw IdP token (`pkg/identn/config.go`,
`pkg/identn/{tokenizeridentn,apikeyidentn}`). So the MCP server must capture
SigNoz's *own* JWT, which only SigNoz can mint.

Today the SSO flow couples two distinct concerns into a single client-supplied
`ref`/`siteURL`:

| Concern | Where it's built | Current source |
|---|---|---|
| IdP OAuth **`redirect_uri`** (where Entra returns the `code`) | `oidccallbackauthn/authn.go` `oauth2Config` (`LoginURL` + `HandleCallback`) | `siteURL.Scheme/Host` + `ExternalPath()` + `/api/v1/complete/oidc` |
| **Post-login token delivery** (where SigNoz 303-redirects the minted JWT) | `implsession/module.go:168-175` → `state.URL` | `siteURL` (carried in `state`, `authtypes` `NewState`) |

- The `ref` enters at `implsession/handler.go:38`
  (`url.Parse(req.URL.Query().Get("ref"))`) and flows to
  `provider.LoginURL(ctx, siteURL, authDomain)` (`module.go:209`).
- The OAuth `state` is literally `siteURL` + `domain_id`
  (`authtypes/authn.go:52-71`), round-tripped through Entra.
- After callback, `module.go:168` redirects the browser to `state.URL` with the
  token in the query (`authtypes.NewURLValuesFromToken`).

Because both derive from the same `siteURL`, the token can only be delivered
where the IdP `redirect_uri` is **registered and handled by SigNoz** — i.e. the
SigNoz origin. Setting `ref=http://127.0.0.1:PORT` would make the IdP
`redirect_uri` `http://127.0.0.1:PORT/api/v1/complete/oidc`, which the MCP server
cannot fulfil (it can't run the code exchange or sign a SigNoz JWT). Dead end as
written.

---

## 4. The design: decouple, then capture on loopback

**Split the fused URL into two independently-sourced values:**

1. **IdP `redirect_uri`** → source from SigNoz's **own configured external URL**
   (`global.Config.ExternalURL`, already a full `*url.URL`:
   `pkg/global/config.go:18`). This is fixed, registered once in Entra, and
   always handled by SigNoz. (It is already in scope in the OIDC provider — the
   provider already uses `globalConfig.ExternalPath()`.)
2. **Post-login token delivery** (`state.URL`) → may be a **validated loopback
   URL** (`http://127.0.0.1:<port>/…`), subject to §5.

Then the MCP flow is:

```
MCP server (loopback :PORT)                 Browser                 SigNoz                 Entra
  |-- GET /api/v2/sessions/context             |                      |                      |
  |     ?email=me&ref=http://127.0.0.1:PORT/cb |                      |                      |
  |<------------- login URL --------------------------------------------|                      |
  |-- open browser to login URL -------------->|                      |                      |
  |                                            |---- authenticate (as me) -------------------->|
  |                                            |<--- 302 code -> SigNoz /api/v1/complete/oidc --|
  |                                            |--- code ------------>| exchange + verify       |
  |                                            |                      | mint USER session JWT   |
  |                                            |<-- 303 http://127.0.0.1:PORT/cb?accessJwt=… ---|
  |<-- GET /cb?accessJwt=… (loopback capture) -|                      |                      |
  | hold token in memory; use as Bearer; rotate via /api/v2/sessions/rotate                    |
```

The IdP `redirect_uri` never changes and never points at the MCP server; only
SigNoz's *final* browser redirect (carrying an already-minted token) is allowed
to target loopback.

---

## 5. Security guardrails — REQUIREMENTS

> Decoupling `redirect_uri` from the delivery target **removes the implicit
> protection** that exists today (the IdP only redirects to URIs registered with
> it). We therefore **must** add explicit protection, or we create a
> session-token-exfiltration open redirect. The following are mandatory.

**R1 — Allowlisted delivery host (the critical one).** After decoupling, the
post-login redirect target (`state.URL` host) MUST be validated against an
allowlist and rejected otherwise. Allowed:
  - the configured `ExternalURL` origin (the normal browser flow), **or**
  - a loopback host: literal **`127.0.0.1`** or **`::1`** (per RFC 8252 §7.3,
    prefer IP literals over `localhost` to avoid DNS-rebinding; `localhost` MAY
    be allowed behind config but is not recommended).

  Any other host → hard error, no redirect, no token. This check lives
  server-side in SigNoz (not the MCP server) and is the single most important
  control.

**R2 — Feature gated, off by default.** The loopback-delivery capability MUST be
disabled unless explicitly enabled. Decision in §8 (new flag vs. anchoring on the
existing `global.MCPURL`). Default state allows only the `ExternalURL` origin
(i.e. today's behaviour).

**R3 — Loopback scheme/shape lock.** For a loopback target, require
`scheme == http`, host ∈ {`127.0.0.1`,`::1`}, no userinfo, and an expected fixed
path (e.g. `/mcp/sso/callback`). Only the **port** may vary, and it MUST fall
within a **configured port allowlist** (matches the MCP server's known fixed set
of ~10 ports). Exact-match everything except the port (RFC 8252 §7.3).

**R4 — `redirect_uri` stays pinned to SigNoz.** The IdP `redirect_uri` is sourced
solely from `ExternalURL` and is identical in the auth request (`LoginURL`) and
the token exchange (`HandleCallback`). The `ref`/loopback value MUST NOT be able
to influence the IdP `redirect_uri` under any circumstance.

**R5 — Anti-injection nonce (login CSRF).** The MCP server MUST include a
high-entropy, single-use nonce in `ref` and verify it on the loopback callback,
so a hostile local page/process cannot inject a token into the MCP listener. The
nonce SHOULD also be bound into SigNoz's `state` so the round trip is
correlated end-to-end. The loopback listener MUST bind to `127.0.0.1` only (never
`0.0.0.0`) and SHOULD reject requests with unexpected `Origin`/`Referer`.

**R6 — Minimise token-in-URL exposure (hardening).** The token transits as a
loopback URL query param. Required: the loopback path is single-use and the MCP
server does not log full request URLs. **Recommended hardening:** instead of
delivering the JWT directly, deliver a short-lived one-time code that the MCP
server exchanges for the token over a back-channel `POST` (authorization-code
style). This keeps the JWT out of URLs/logs entirely. Treat as a fast-follow if
not done in v1; R1+R3 make the direct form acceptable on loopback in the interim.

**R7 — No weakening of token lifetime.** The loopback-delivered token is the
*same* session token with the *same* rotation / idle / max-duration semantics
(`authtypes/token.go:124` `IsValid`). No longer-lived or non-expiring variant is
introduced.

**R8 — Audit.** Loopback-delivery logins MUST be audited (the codebase already
has an auditor) with the operator identity, source, and the loopback port used.

**R9 — Non-loopback flow unchanged.** The standard browser SSO path keeps
delivering only to the `ExternalURL` origin; this change adds the loopback option
and must not broaden any other redirect.

---

## 6. Implementation plan (SigNoz side, all MIT under `pkg/`)

### 6.1 `pkg/authn/callbackauthn/oidccallbackauthn/authn.go`
- Build the OAuth `redirect_uri` from `globalConfig.ExternalURL`
  (`Scheme`+`Host`+`ExternalPath()`+`/api/v1/complete/oidc`) in **both**
  `LoginURL` and `HandleCallback` (they must match for the exchange).
- Add a guard: if `ExternalURL` has no scheme or host (default is
  `{Scheme:"", Host:"<unset>"}`, and `Validate()` does **not** enforce these —
  `pkg/global/config.go:43-53`), return a clear configuration error instead of
  emitting a broken `redirect_uri`.

### 6.2 Delivery-target validation (R1/R3) + gating (R2)
- Add a validator (session module or `authtypes`) enforcing R1/R3 against the
  configured `ExternalURL` origin + loopback allowlist + port allowlist.
- Apply it where `ref`→`siteURL`→`state.URL` is accepted
  (`implsession/handler.go:38` / `module.go`), so a disallowed target is rejected
  before any token is minted/redirected.

### 6.3 Config
- See §8 for the gate/allowlist decision. Whichever is chosen, the validator
  reads it; default = loopback delivery **off**.

### 6.4 Deployment prerequisite (already satisfied)
- The design requires `SIGNOZ_GLOBAL_EXTERNAL__URL` to be a full URL. **Verified
  already set** in `signoz-stack/docker-compose.yaml:177` →
  `https://signoz.ml.dataops.engit.synamedia.com`. (The `__` is the env mapping's
  literal-underscore escape → `global::external_url`.)

### 6.5 Entra
- Register `https://<external-host>/api/v1/complete/oidc` as the **only** redirect
  URI (already the case for normal SSO). **No loopback URIs are registered in
  Entra** — Entra never sees the loopback target.

---

## 7. MCP-server side (out of scope for the SigNoz repo, documented for the team)

1. Bind a loopback listener on `127.0.0.1:<port>` (port from the MCP server's
   fixed set), path `/mcp/sso/callback`.
2. Generate a single-use nonce; build `ref=http://127.0.0.1:<port>/mcp/sso/callback?n=<nonce>`.
3. `GET /api/v2/sessions/context?email=<operator>&ref=<ref>` → obtain the per-org
   SSO login URL.
4. Open the system browser to that URL; operator authenticates with Entra.
5. On the loopback callback: verify the nonce, capture the token, close the
   browser tab via a small success page.
6. Hold the token in memory; use as `Authorization: Bearer`.
7. Background-refresh via `POST /api/v2/sessions/rotate`
   (`pkg/apiserver/signozapiserver/session.go:46` → `module.RotateSession` →
   `tokenizer.RotateToken`) before each rotation deadline. Re-run the browser
   flow only on process restart or when the session hits its hard max lifetime.

---

## 8. Open decisions

- **Gate mechanism (R2):** a dedicated flag
  (`SIGNOZ_AUTHN_OIDC_LOOPBACK_REDIRECT_ENABLED` + a port allowlist), **or**
  anchor on the existing `global.MCPURL` (`pkg/global/config.go:20`) — when
  `MCPURL` is itself a loopback URL, treat its host/port as the allowed target.
  Recommendation: a dedicated flag + explicit port allowlist for clarity and
  least privilege; `MCPURL` coupling is tidy but conflates "where the MCP server
  is" with "what redirect we trust."
- **Direct token vs one-time code (R6):** ship direct-on-loopback first (with
  R1/R3) or go straight to the back-channel code exchange. Recommendation: direct
  for v1, code-exchange as a tracked fast-follow.
- **`localhost` literal:** allow alongside `127.0.0.1`/`::1`, or IP-literals only?
  Recommendation: IP-literals only (RFC 8252 §7.3).

---

## 9. Clean-room / license compliance

All changes are MIT, under `pkg/` (OIDC provider, session module, `authtypes`,
`global` config). No `ee/` file is consulted or modified. Reference material: our
own MIT tree, the OAuth 2.0 / RFC 8252 specs, and the public `go-oidc`/`oauth2`
library docs. A `LICENSE-COMPLIANCE-MCP-LOOPBACK-SSO.md` should accompany the PR,
mirroring the prior ones.

---

## 10. Testing strategy

- **Unit:** the R1/R3 validator — table tests covering allowed (`ExternalURL`
  origin; `127.0.0.1`/`::1` on allowlisted ports) and rejected (arbitrary hosts,
  non-loopback IPs, `0.0.0.0`, disallowed ports, wrong scheme, userinfo present,
  feature disabled).
- **Build:** `GOTOOLCHAIN=go1.25.7 go build ./cmd/community/` (host Go 1.26 caveat
  as in the prior work).
- **Integration (manual / scripted):**
  - Feature **off** → loopback `ref` is rejected; normal SSO to `ExternalURL`
    still works.
  - Feature **on** → full loopback flow against a real Entra tenant: token lands
    on the loopback listener, an authenticated `GET /api/v1/...` succeeds **as the
    operator** (verify the principal/roles in the response/audit), and
    `/api/v2/sessions/rotate` keeps it alive.
  - **Negative:** non-loopback `ref` (e.g. `https://evil.example`) → rejected, no
    token minted; wrong/missing nonce on the callback → MCP server rejects.
- **Regression:** existing email/password and browser SSO logins unaffected.

---

## 11. Out of scope

- Applying the same loopback option to the Google or SAML providers (OIDC/Entra
  is the only target here).
- The MCP server implementation itself (separate repo).
- The back-channel one-time-code exchange (R6) if deferred — track separately.
