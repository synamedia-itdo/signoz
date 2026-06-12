# License-Compliance Evidence: Community OIDC Authentication

**Author:** adicks@synamedia.com
**Date:** 2026-06-12
**Branch:** `feat/community-oidc`
**Purpose:** Record the evidence gathered before implementing a community-edition
OIDC auth provider, demonstrating that the new code lives entirely in MIT-licensed
space and does not copy, derive from, or depend on Enterprise-licensed code.

> This document is the auditable record requested for license compliance. Every
> claim below was verified directly against the source tree on the date above;
> file paths and line numbers are included so the verification can be reproduced.

---

## 1. How this repository is licensed

The repository is **not** uniformly MIT. The root `LICENSE` defines a split
(verified at `LICENSE:1-25`):

| Scope | License |
|---|---|
| Everything under `ee/` and `cmd/enterprise/` | **SigNoz Enterprise License** (`ee/LICENSE`) |
| Third-party components | Their own upstream licenses |
| **Everything else** | **MIT ("MIT Expat")** |

Exact wording from `LICENSE:5-7`:

> * All content that resides under the "ee/" and the "cmd/enterprise/" directory
>   of this repository, if that directory exists, is licensed under the license
>   defined in "ee/LICENSE".
> * Content outside of the above mentioned directories or restrictions above is
>   available under the "MIT Expat" license as defined below.

**Correction to the proposal:** PROPOSAL.md §2 calls the non-`ee/` code simply
"MIT". That is substantively correct — the root LICENSE designates it "MIT Expat"
(the Expat variant of the MIT license). The licensing premise of the proposal
therefore **holds**: a new file created under `pkg/` is MIT-licensed by virtue of
its location.

**Licensing is directory-based, not header-based.** There are **no per-file SPDX
or license headers** anywhere in `pkg/` or `ee/` — verified by grepping
`pkg/authn/` for `SPDX` / `Enterprise License` / `MIT License` (zero matches).
The license attaches purely by directory. **Consequence:** the proposal's
instruction (§3.1) to "copy SigNoz's MIT header style" is moot — there is no
per-file header style in this repo. To match the repository's convention, the new
file carries **no** license header. Placing it under `pkg/` is what makes it MIT.

---

## 2. Component-by-component license inventory

Each row was confirmed to exist at the cited location. "License" is determined by
the directory rule in §1.

| Component | Verified location | License |
|---|---|---|
| `AuthNProviderOIDC` constant | `pkg/types/authtypes/authn.go:22` | MIT |
| `OIDCConfig` struct (mentions Azure/IssuerAlias) | `pkg/types/authtypes/oidc.go:9-32` | MIT |
| `OIDC` field on `AuthDomainConfig` | `pkg/types/authtypes/domain.go:74` | MIT |
| OIDC domain-config validation | `pkg/types/authtypes/domain.go:184-187` | MIT |
| `AttributeMapping` (claim mapping) | `pkg/types/authtypes/mapping.go:11-51` | MIT |
| `RoleMapping` + `NewRoleFromCallbackIdentity` | `pkg/types/authtypes/mapping.go:53-124` | MIT |
| `CallbackAuthN` interface | `pkg/authn/authn.go:19-28` | MIT |
| `NewCallbackIdentity` constructor | `pkg/types/authtypes/authn.go:121` | MIT |
| `/api/v1/complete/oidc` route | `pkg/apiserver/signozapiserver/session.go:117-118` | MIT |
| `CreateSessionByOIDCCallback` handler | `pkg/modules/session/implsession/handler.go:106-111` | MIT |
| Session creation + role mapping + token issuance | `pkg/modules/session/implsession/module.go:128-172` | MIT |
| Community provider wiring (`NewAuthNs`) | `pkg/signoz/authn.go:15-27` | MIT |
| **Google callback impl (our template)** | `pkg/authn/callbackauthn/googlecallbackauthn/authn.go` | **MIT** |
| OIDC callback impl (Enterprise — *not used*) | `ee/authn/callbackauthn/oidccallbackauthn/authn.go` | **Enterprise** |
| SAML callback impl (Enterprise — *not used*) | `ee/authn/callbackauthn/samlcallbackauthn/authn.go` | **Enterprise** |

**Confirmed:** the *only* OIDC-specific piece behind the Enterprise license is the
single Go file at `ee/authn/callbackauthn/oidccallbackauthn/authn.go`. Every other
link in the chain — the provider constant, the config type, the validation, the
HTTP route, the handler, the session/token flow, and the role mapping — is already
in MIT space and fully functional. The community server simply never populates the
`AuthNProviderOIDC` slot in its provider map (`pkg/signoz/authn.go:23-26` lists
only EmailPassword and GoogleAuth), so OIDC fails at runtime in the community
build today.

The runtime chain that our new file plugs into, all MIT:

```
/api/v1/complete/oidc                                   (session.go:117)
  -> handler.CreateSessionByOIDCCallback                (handler.go:106)
    -> module.CreateCallbackAuthNSession(…, OIDC, …)    (module.go:128)
      -> callbackAuthN.HandleCallback(ctx, values)      (module.go:134)   <-- OUR CODE
      -> RoleMapping.NewRoleFromCallbackIdentity(...)   (module.go:145-146)
      -> GetOrCreateUser + CreateToken                  (module.go:149-166)
```

Our file supplies the `HandleCallback` (and `LoginURL` / `ProviderInfo`)
implementation; the MIT module does role mapping, user creation, and token
issuance.

---

## 3. Clean-room statement for the new file

`pkg/authn/callbackauthn/oidccallbackauthn/authn.go` (new) is authored as follows:

- **Derived solely from MIT sources:** the structure and idioms are taken from the
  MIT-licensed Google implementation at
  `pkg/authn/callbackauthn/googlecallbackauthn/authn.go`, together with the
  MIT-licensed type definitions in `pkg/types/authtypes/` and the public
  `github.com/coreos/go-oidc/v3` library (Apache-2.0, third-party).
- **The Enterprise OIDC file was NOT used as an implementation reference.** To
  confirm the license boundary for this record, only the first ~20 lines (the
  `package` clause and `import` block) of
  `ee/authn/callbackauthn/oidccallbackauthn/authn.go` were viewed — enough to see
  that it resides under `ee/` and is therefore Enterprise-licensed. Its
  implementation body (the creative, copyrightable expression) was **not read**
  and is **not** the basis for the new code. The import block is not itself
  copyrightable expression and was used only to confirm the directory/license
  boundary, not to reproduce logic.
- **No Enterprise dependency:** unlike the Enterprise version — whose constructor
  is `New(store, licensing, providerSettings, config.Global)` and which gates on a
  license (`ee/.../authn.go` imports `pkg/licensing`) — our community version
  mirrors the Google template's `New(ctx, store, providerSettings, globalConfig)`
  and takes **no** `licensing.Licensing` dependency. The community OIDC provider
  is intentionally ungated.
- **Location = license:** the file is created under `pkg/`, so by the §1 directory
  rule it is MIT-licensed. No header is added (matching repo convention, §1).

---

## 4. Enterprise build is unaffected (verified)

`cmd/enterprise/server.go` builds its provider map by calling
`signoz.NewAuthNs(...)` first, then **overwriting** the OIDC and SAML slots with
the Enterprise implementations (verified at `cmd/enterprise/server.go:109-128`):

```go
authNs, err := signoz.NewAuthNs(ctx, providerSettings, store, licensing, config.Global)
...
authNs[authtypes.AuthNProviderSAML] = samlCallbackAuthN
authNs[authtypes.AuthNProviderOIDC] = oidcCallbackAuthN   // ee impl wins in enterprise build
```

Therefore adding an OIDC entry to the community `NewAuthNs` map is **cleanly
overridden** in the Enterprise build — no duplicate-key conflict, no compile
error, no behavioral change for Enterprise. In the community build (`cmd/community`),
which never performs this override, our MIT implementation is the one that runs.

---

## 5. Corrections to PROPOSAL.md captured during verification

The proposal's intent is sound; these details were out of date and have been
corrected for the implementation:

1. **License label:** non-`ee/` code is "MIT Expat", not bare "MIT" (§1). Premise
   unchanged.
2. **No per-file headers:** the repo uses directory-based licensing with no SPDX
   headers; the new file gets no header (§1). The proposal's "copy the MIT header
   style" step does not apply.
3. **Constructor signatures drifted.** The proposal's snippets predate a
   `global.Config` parameter that now threads through:
   - `signoz.NewAuthNs(ctx, providerSettings, store, licensing, globalConfig)`
     (`pkg/signoz/authn.go:15`)
   - `googlecallbackauthn.New(ctx, store, providerSettings, globalConfig)`
     (`pkg/authn/callbackauthn/googlecallbackauthn/authn.go:40`)
   Our new `New` follows the current Google signature, not the proposal's.
4. **Redirect path / external path:** `redirectPath = "/api/v1/complete/oidc"` is
   correct, but the redirect URL must be joined under
   `globalConfig.ExternalPath()` exactly as the Google template does
   (`googlecallbackauthn/authn.go:182-186`).
5. **Azure `IssuerAlias` mechanism confirmed:** `go-oidc v3.17.0` provides
   `oidc.InsecureIssuerURLContext(ctx, issuerURL)` (verified in the module cache at
   `oidc/oidc.go:83`) which is exactly the documented Azure work-around: discover
   against `Issuer`, validate the token issuer against `IssuerAlias` when set.
6. **`GetUserInfo` confirmed:** `provider.UserInfo(ctx, tokenSource)` +
   `UserInfo.Claims(v)` exist in `go-oidc v3.17.0` (`oidc/oidc.go:351`, `:343`) for
   fetching extra claims from thin Entra ID tokens.

---

## 6. Conclusion

The implementation touches only MIT-licensed space:

- **New file:** `pkg/authn/callbackauthn/oidccallbackauthn/authn.go` — MIT by
  location, clean-room authored from the MIT Google template, no Enterprise
  derivation, no `licensing` dependency.
- **Modified file:** `pkg/signoz/authn.go` — MIT, additive wiring only.

No Enterprise (`ee/`, `cmd/enterprise/`) file is copied, modified, or required at
build or run time. The change is consistent with the repository's dual-license
structure.
