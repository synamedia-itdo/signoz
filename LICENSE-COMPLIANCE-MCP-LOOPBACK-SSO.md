# License-Compliance & Security Evidence: Loopback-redirect SSO

**Author:** adicks@synamedia.com
**Date:** 2026-06-17
**Branch:** `feat/community-loopback-sso`
**Proposal:** `PROPOSAL-MCP-LOOPBACK-SSO.md`
**Purpose:** Record the license posture and — because this is security-sensitive
— how each guardrail requirement (R1–R9) from the proposal is satisfied, deferred,
or delegated to the MCP client.

---

## 1. License boundary (unchanged)

Per the root `LICENSE`, everything outside `ee/` and `cmd/enterprise/` is MIT
("MIT Expat"), directory-based, no per-file headers. All changes here are under
`pkg/` → MIT.

**Clean-room:** authored from our own MIT tree, the OAuth 2.0 / RFC 8252 specs,
and the public `golang.org/x/oauth2` + `coreos/go-oidc` docs. No `ee/` file was
opened or referenced. No new dependencies.

---

## 2. Files changed (all MIT)

| File | Change |
|---|---|
| `pkg/global/config.go` | Add `LoopbackRedirectConfig{Enabled, Ports}` to `global.Config` (default **off**); `Validate()` rejects `enabled` without in-range ports. |
| `pkg/authn/callbackauthn/oidccallbackauthn/authn.go` | Source OAuth `redirect_uri` from `ExternalURL` (`redirectURL()`, guarded); add `validateDeliveryTarget()`; enforce it in `LoginURL` and `HandleCallback`. |
| `pkg/authn/callbackauthn/oidccallbackauthn/authn_test.go` | Table tests for the validator + `redirectURL` guard. |

No change was needed in `pkg/modules/session/implsession` — the final redirect
already uses `state.URL` (`module.go:168`), and validation happens upstream in the
OIDC provider before any token is minted.

---

## 3. Guardrail compliance (R1–R9)

| Req | Status | How |
|---|---|---|
| **R1** Allowlisted delivery host | **Enforced (server-side)** | `validateDeliveryTarget`: permits only the `ExternalURL` origin (scheme+host) or a loopback `127.0.0.1`/`::1`; everything else hard-rejected. Enforced in both `LoginURL` (early) and `HandleCallback` (authoritative, **before** the token exchange and before the session token is minted). |
| **R2** Gated, off by default | **Enforced** | `global.LoopbackRedirect.Enabled` defaults `false`; when disabled only the `ExternalURL` origin is allowed (today's effective behaviour). `Validate()` rejects `enabled` with an empty port list. |
| **R3** Loopback scheme/shape lock + port allowlist | **Enforced (path clause relaxed)** | Requires `http` scheme, host ∈ {`127.0.0.1`,`::1`}, no userinfo, and a port present in the configured allowlist. **Deviation:** the URL *path* is not pinned to a fixed value (the MCP server chooses its own callback path); loopback-host + port-allowlist + enabled-gate are the load-bearing controls, and the path adds little on a loopback-only target. Noted for review. |
| **R4** `redirect_uri` pinned to SigNoz | **Enforced** | `redirectURL()` builds the IdP `redirect_uri` solely from `ExternalURL`; identical in `LoginURL` and `HandleCallback`. The client `ref`/`siteURL` can no longer influence the `redirect_uri` — only the (validated) delivery target. |
| **R5** Anti-injection nonce + loopback bind | **Delegated to MCP client** | SigNoz round-trips the `state` (which the client can populate with a nonce in `ref`); generating/verifying the nonce and binding the loopback listener to `127.0.0.1` are MCP-server responsibilities (documented in the proposal §7). No SigNoz code can enforce this for an external client. |
| **R6** Minimise token-in-URL (one-time code) | **Accepted as-is (decision 2026-06-17)** | The tokens (`accessToken`/`refreshToken`, opaque — not JWTs) are delivered directly in the loopback redirect query. This is **identical to upstream SigNoz's existing Google/SAML SSO**, which deliver the same tokens in the redirect query via the shared `NewURLValuesFromToken` path (`module.go:172`); a one-time-code + back-channel `/sessions/exchange` endpoint was considered and **declined** to avoid a permanent divergence from upstream for an already-shipped posture. Acceptable because the target is loopback-only (R1/R3, can't leave the host). **Compensating control:** the client-side loopback mitigations (R5) — single-use path nonce, no logging of the callback URL, self-contained success page — are therefore **required, not optional** (SigNoz does not sign the loopback target into `state`, so the client nonce is what binds the delivered token to the intended process). |
| **R7** No weakening of token lifetime | **Met** | The loopback path reuses the unchanged session-mint path (`module.go:163`, `tokenizer.CreateToken`) and the same rotation/idle/max semantics (`authtypes/token.go`). No new long-lived/non-expiring token. |
| **R8** Audit | **Partial** | Disallowed targets are logged at `ERROR` with the offending target. A dedicated audit event for *successful* loopback deliveries is a recommended follow-up; the generic login audit path still applies. |
| **R9** Non-loopback flow unchanged | **Met** | With loopback disabled (default), only the `ExternalURL` origin is permitted — equivalent to prior behaviour. The loopback option is purely additive and gated. |

---

## 4. Behaviour change to be aware of (important)

**OIDC now sources `redirect_uri` from `global.ExternalURL` instead of the client
`ref`.** Consequences:

1. **`SIGNOZ_GLOBAL_EXTERNAL__URL` must be set** (scheme + host) for OIDC to work
   at all — `redirectURL()` errors clearly otherwise. (`global.Config.Validate()`
   does not enforce scheme/host, so this guard lives in the provider.)
   **Already satisfied** in the deployment: `signoz-stack/docker-compose.yaml:177`
   → `https://signoz.ml.dataops.engit.synamedia.com`.
2. **The Entra app registration's redirect URI must equal**
   `<ExternalURL>/api/v1/complete/oidc` (it already should for normal SSO). No
   loopback URI is ever registered with Entra — Entra only ever redirects to
   SigNoz.
3. **The normal browser flow** requires the frontend's `ref` (its origin) to match
   `ExternalURL`'s scheme+host — true when SigNoz is served at `ExternalURL`.

### Enabling loopback delivery (off by default)

```
SIGNOZ_GLOBAL_LOOPBACK__REDIRECT_ENABLED=true
# PORTS defaults to the MCP server's fixed loopback range 47823-47832, so this
# is only needed to override it:
SIGNOZ_GLOBAL_LOOPBACK__REDIRECT_PORTS=47823,47824,47825,47826,47827,47828,47829,47830,47831,47832
```

(`__` is the env-mapping literal-underscore escape → `global::loopback_redirect::*`;
single `_` is the nesting delimiter.)

**On parsing the port list:** koanf delivers the comma-joined env value as a
*single-element* slice (e.g. `["47823,47824"]`), so a `[]int` field fails at
startup with `cannot parse value as 'int'`. The field is therefore `[]string`
(matching the codebase convention for list config, e.g. `identn.Headers`), and
`LoopbackRedirectConfig.AllowedPorts()` splits each entry on commas — tolerating
both the env single-element form and a YAML list (`ports: [47823, 47824]`). This
is verified by an integration test through the real config pipeline
(`cmd/config_loopback_test.go`). Earlier revisions of this document incorrectly
claimed the env list decoded via `WeaklyTypedInput` + `StringToSliceHookFunc`;
that path does not fire for env values here.

---

## 5. Verification (run 2026-06-17)

- **Build:** `GOTOOLCHAIN=go1.25.7 go build ./pkg/global/ ./pkg/authn/callbackauthn/oidccallbackauthn/ ./cmd/community/` → exit 0.
- **Vet:** clean for both changed packages.
- **Unit tests:** `go test ./pkg/authn/callbackauthn/oidccallbackauthn/ ./pkg/global/` → ok. The validator table covers: external origin (allowed; wrong scheme rejected), arbitrary host rejected, loopback rejected when disabled, `127.0.0.1`/`::1` on allowlisted ports allowed, port-not-in-allowlist rejected, `https`-on-loopback rejected, userinfo rejected, `0.0.0.0` rejected, missing-port rejected, nil rejected; plus the `redirectURL` unset-guard and path-join.
- **gofmt:** clean.

**Not verified here (environment limit):** a full browser→Entra→loopback round
trip against a live tenant. That is the deployment-side end-to-end check (enable
the flag, point the MCP server's loopback `ref` at SigNoz, confirm the token lands
on the listener and authenticates **as the operator**).

---

## 6. Conclusion

The change is MIT, clean-room, and additive. The security-critical control (R1:
server-side allowlist that prevents the decoupled redirect from becoming a
token-exfiltration open redirect) is enforced before any token is minted, and the
capability is off by default and must be explicitly enabled with an explicit port
allowlist. R6 (token-in-URL) is **accepted as-is** for parity with upstream
Google/SAML SSO, with the client-side loopback mitigations (R5) as the required
compensating control. Remaining items (R5 client nonce, R8 success-audit, R3
path-pinning) are documented as client responsibilities or tracked follow-ups,
none of which weaken the default posture.
