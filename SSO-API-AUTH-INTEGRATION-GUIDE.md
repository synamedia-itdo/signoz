# SSO API Auth Integration Guide — loopback "as-me" flow

**Audience:** developers integrating a local client (e.g. the Metis MCP server)
that needs to call the SigNoz REST API **as the human operator**, using
interactive Entra/OIDC SSO captured on a loopback listener.
**Applies to:** the Synamedia fork (`synamedia-itdo/signoz`) with the loopback
redirect support (fork PRs #3/#4).

> TL;DR: SigNoz is the OAuth client; it mints its **own** session credential and
> 303-redirects it to your loopback listener. You capture an **opaque access
> token** (not a JWT) from the redirect's query string, send it as
> `Authorization: Bearer`, and keep it alive with `/api/v2/sessions/rotate`.

---

## 0. Prerequisites (server side, one-time)

- `SIGNOZ_GLOBAL_EXTERNAL__URL` set to SigNoz's real URL (e.g.
  `https://signoz.ml.dataops.engit.synamedia.com`). The OIDC `redirect_uri` is
  derived from this.
- Entra app registration: the **only** redirect URI is
  `<external_url>/api/v1/complete/oidc`. **Do not** register any loopback URL in
  Entra — Entra never sees it.
- `SIGNOZ_GLOBAL_LOOPBACK__REDIRECT_ENABLED=true`.
- Loopback ports: default allowlist is `47823–47832` (in-code). Override with
  `SIGNOZ_GLOBAL_LOOPBACK__REDIRECT_PORTS=...` only if needed. **Your listener
  must bind one of the allowlisted ports**, or SigNoz refuses to deliver.

---

## 1. Credential model (read this first)

- The credential you receive and use is an **opaque access token** — a random
  string, **not a JWT**. Do not try to decode/parse it. Treat it as a secret.
- You authenticate API calls with header: `Authorization: Bearer <accessToken>`.
- You also receive a **refresh token** (also opaque) used only to rotate.
- There is no IdP/Entra token involved in API calls — SigNoz only accepts its own
  session token (or a `SIGNOZ-API-KEY`, which is a service account, i.e. *not*
  "as me", and out of scope here).

---

## 2. Step 1 — initiate: get the IdP login URL

```
GET /api/v2/sessions/context?email=<operator-email>&ref=<loopback-callback-url>
```

- **Response: `200`, JSON.** (Not a redirect.) Standard envelope `{status, data}`.
- The IdP login URL is **nested per-org, per-provider** (no flat `ssoUrl` field):

```jsonc
{
  "status": "success",
  "data": {
    "exists": true,
    "orgs": [
      {
        "id": "0192...",
        "name": "synamedia.com",
        "authNSupport": {
          "callback": [
            { "provider": "oidc", "url": "https://login.microsoftonline.com/<tenant>/oauth2/v2.0/authorize?...&redirect_uri=https%3A%2F%2F<external>%2Fapi%2Fv1%2Fcomplete%2Foidc&state=<encodes your ref>" }
          ],
          "password": []
        },
        "warning": null
      }
    ]
  }
}
```

**Extract:** `data.orgs[i].authNSupport.callback[j]` where `provider == "oidc"` →
open its `url`. Handle the cases where there's no `oidc` callback (SSO not
configured for that domain) or `data.exists == false`.

### What to pass as `ref` (important — see §4)
Put your **unguessable correlation token in the PATH**, not the query:

```
ref = http://127.0.0.1:47823/sso/callback/<random-nonce>
```

---

## 3. Step 2 — open the browser

Open the extracted `url` in the system browser. The operator authenticates with
Entra (as themselves). Your loopback listener waits for the callback.

---

## 4. Step 3 — capture: the loopback redirect (the load-bearing bit)

On success, SigNoz `303 See Other`-redirects the browser to your loopback target
with the session credential in the **query string**:

```
http://127.0.0.1:47823/sso/callback/<random-nonce>?tokenType=bearer&accessToken=<opaque>&refreshToken=<opaque>&expiresIn=<seconds>
```

**Query params** (from `authtypes.NewURLValuesFromToken`):

| Param | Meaning |
|---|---|
| `accessToken` | opaque bearer token → use as `Authorization: Bearer` |
| `refreshToken` | opaque token → used only for rotation |
| `tokenType` | always `bearer` |
| `expiresIn` | **seconds until rotation is due** (the rotation interval), *not* total session lifetime |

> The param names are `accessToken` / `refreshToken` — **not** `accessJwt` /
> `refreshJwt`.

### What is preserved from your `ref` — and what is NOT

SigNoz rebuilds the redirect URL from your `ref` keeping **scheme + host + port +
path**, then **replaces the entire query string** with the token params above.

- ✅ **Path is preserved intact** (`/sso/callback/<nonce>` comes back unchanged).
- ❌ **Any query string you put in `ref` is dropped.** (The `state` round-trip
  keeps only scheme/host/path; the final redirect overwrites the query.)

**Consequence:** key your waiter on the **path** (the nonce segment), never on a
query param you tried to embed in `ref`. A query nonce will silently vanish.

### Failure / cancel case (must handle)

If login fails or the user cancels, SigNoz does **not** redirect to your loopback.
It redirects to a **relative** `<ExternalPath>/login?...&callbackauthnerr=true` on
SigNoz's own origin. Your loopback listener will simply **never be called** — so:

- Put a **timeout** on your waiter (e.g. 2–5 min) and surface a clear "login not
  completed" error.
- Don't assume the callback always arrives.

### Minimal capture handler (pseudocode)

```
GET /sso/callback/<nonce>:
  if nonce != expectedNonce:        # set when you built `ref`
      return 400
  accessToken  = query["accessToken"]
  refreshToken = query["refreshToken"]
  expiresIn    = int(query["expiresIn"])
  store(accessToken, refreshToken, rotateDueAt = now + expiresIn)
  return 200 "You're signed in — you can close this tab."
  signal the waiter
```

---

## 5. Step 4 — call the API as the operator

```
GET /api/v1/...                       (any SigNoz API)
Authorization: Bearer <accessToken>
```

Calls are attributed to, and authorized as, the operator's user — with their
roles. No service account, no privilege drift.

---

## 6. Step 5 — keep the session alive: rotate

```
POST /api/v2/sessions/rotate
Authorization: Bearer <current accessToken>      # required
Content-Type: application/json
{ "refreshToken": "<current refreshToken>" }      # required
```

- **Needs BOTH**: the current access token (Bearer header) **and** the current
  refresh token (JSON body). It's a hybrid, not one-or-the-other.
- **Response `200`, JSON** envelope wrapping a fresh pair (`GettableToken`):

```jsonc
{ "status": "success",
  "data": { "tokenType": "bearer", "accessToken": "<new>", "refreshToken": "<new>", "expiresIn": <seconds> } }
```

- Rotate **before** `expiresIn` elapses; replace your stored pair with the new one.
- **Hard limits still apply.** Independent of rotation, the session expires on:
  - **idle**: not used for longer than the configured idle duration, and
  - **max age**: older than the configured max duration (since first login).
  When either trips, rotation fails — you must re-run the browser SSO flow.

### v1 recommendation
You already capture the `refreshToken` from the loopback, so **rotation is cheap
to include in v1** — store both tokens, rotate on a timer keyed off `expiresIn`,
and fall back to a full re-login when rotation returns an auth error (idle/max
reached) or on process restart. (Shipping "store access token + re-login on
expiry" first and adding rotate later is also fine, but rotate is small enough
that doing it up front avoids surprise mid-session re-auths.)

---

## 7. Step 6 — logout / cleanup (optional)

```
DELETE /api/v2/sessions
Authorization: Bearer <accessToken>
```

Revokes the session server-side. Nice to call on clean shutdown so tokens don't
linger.

---

## 8. Security checklist (client side)

- **Bind the loopback listener to `127.0.0.1` only** (never `0.0.0.0`).
- **Path nonce**: high-entropy, single-use, generated per login; verify it on the
  callback. This is your defence against a hostile local process hitting your
  callback (since the query can't carry it — §4).
- **Don't log full callback URLs** — they contain the tokens.
- Treat `accessToken`/`refreshToken` as **secrets**; keep them in memory only.
- The server-side allowlist (loopback host + port + enabled gate) is enforced by
  SigNoz, but the nonce and the bind address are **your** responsibility.

---

## 9. Endpoint reference

| Step | Method & path | Auth | Body | Success |
|---|---|---|---|---|
| Initiate | `GET /api/v2/sessions/context?email=&ref=` | none | — | `200` JSON (`SessionContext`) |
| Callback (SigNoz→you) | `303` to your `ref` path | — | — | query: `accessToken,refreshToken,tokenType,expiresIn` |
| Rotate | `POST /api/v2/sessions/rotate` | `Bearer` access token | `{"refreshToken": "..."}` | `200` JSON (`GettableToken`) |
| Logout | `DELETE /api/v2/sessions` | `Bearer` access token | — | `200` |

---

## 10. FAQ / gotchas

- **"Is the token a JWT?"** No — opaque. Don't decode it.
- **"Can I put a `?state=` / `?n=` nonce in `ref`?"** No — query is stripped. Use
  the path.
- **"My callback never fires."** Either the user didn't complete login (SigNoz
  redirected to its own `/login`, not you — add a timeout), or your port isn't in
  the server allowlist, or the feature isn't enabled.
- **"`expiresIn` seems short."** It's the **rotation** interval, not the session
  lifetime — rotate and keep going until idle/max forces a re-login.
- **"Multiple `orgs` in the context response?"** Pick the operator's org; match
  the `oidc` callback within it.
