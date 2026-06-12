# Proposal: Native OIDC Authentication for SigNoz Community Edition

**Author:** adicks@synamedia.com
**Date:** 2026-06-12
**Status:** Proposed
**Target repo:** A Synamedia fork of `github.com/SigNoz/signoz`

---

## 1. Goal

Add a native OIDC authentication provider to the SigNoz **community edition** so
that Synamedia users can sign in with their Microsoft Entra ID account via true
SSO -- no shared password, no secondary login screen, no enterprise license.

The user experience target is identical to the existing Google SSO flow:

1. User visits SigNoz
2. Clicks "Sign in with Entra ID" (or is auto-redirected if SSO is enforced for their domain)
3. Authenticates with Microsoft
4. Lands in the SigNoz dashboard, fully authenticated

---

## 2. Why this is straightforward

The SigNoz codebase is dual-licensed:

- **MIT** -- everything outside `ee/` and `cmd/enterprise/`
- **Enterprise** -- `ee/` and `cmd/enterprise/` only

Investigating the OIDC code path revealed that **almost all of the OIDC machinery
is already MIT-licensed**:

| Component | Location | License |
|---|---|---|
| `AuthNProviderOIDC` constant | `pkg/types/authtypes/authn.go` | MIT |
| `OIDCConfig` struct (explicitly mentions Azure) | `pkg/types/authtypes/oidc.go` | MIT |
| Domain config validation for OIDC | `pkg/types/authtypes/domain.go` | MIT |
| `/api/v1/complete/oidc` route registration | `pkg/apiserver/signozapiserver/session.go` | MIT |
| `CreateSessionByOIDCCallback` handler | `pkg/modules/session/implsession/handler.go` | MIT |
| Session creation + token issuance flow | `pkg/modules/session/implsession/module.go` | MIT |
| Frontend OIDC settings UI | `frontend/src/...` | MIT |
| **Actual OIDC callback implementation** | `ee/authn/callbackauthn/oidccallbackauthn/` | **Enterprise** |

The only piece in the enterprise license is the one Go file that implements the
`authn.CallbackAuthN` interface for OIDC. **Everything else is already in MIT
space and works.** The community server simply doesn't wire any implementation
into the `AuthNProviderOIDC` slot, so any attempt to use OIDC in community
edition fails at runtime.

**Our job is to write an MIT-licensed OIDC callback implementation and wire it
into the community server.** That's it.

---

## 3. Implementation plan

### 3.1 New file: `pkg/authn/callbackauthn/oidccallbackauthn/authn.go`

Create a new MIT-licensed package that implements the `authn.CallbackAuthN`
interface. Use the existing **Google** implementation at
`pkg/authn/callbackauthn/googlecallbackauthn/authn.go` as the template -- it is
also MIT licensed and uses the same `github.com/coreos/go-oidc/v3/oidc` library
that we need.

The implementation must:

- Use the existing `OIDCConfig` struct from `pkg/types/authtypes/oidc.go`
  (no schema changes needed)
- Honour `IssuerAlias` for Azure (the comment in `OIDCConfig` explicitly calls
  this out -- Entra's discovery document returns a different issuer URL than the
  one used to discover it)
- Use `GetUserInfo` to fetch additional claims when set
- Apply `ClaimMapping` for email / name / groups extraction
- Return an `authtypes.CallbackIdentity` from `HandleCallback`

Suggested file structure (mirroring `googlecallbackauthn/authn.go`):

```go
package oidccallbackauthn

import (
    "context"
    "net/url"

    "github.com/coreos/go-oidc/v3/oidc"
    "golang.org/x/oauth2"

    "github.com/SigNoz/signoz/pkg/authn"
    "github.com/SigNoz/signoz/pkg/errors"
    "github.com/SigNoz/signoz/pkg/factory"
    "github.com/SigNoz/signoz/pkg/http/client"
    "github.com/SigNoz/signoz/pkg/types/authtypes"
    "github.com/SigNoz/signoz/pkg/valuer"
)

const redirectPath = "/api/v1/complete/oidc"

var _ authn.CallbackAuthN = (*AuthN)(nil)

type AuthN struct {
    store      authtypes.AuthNStore
    settings   factory.ScopedProviderSettings
    httpClient *client.Client
}

func New(ctx context.Context, store authtypes.AuthNStore, providerSettings factory.ProviderSettings) (*AuthN, error) { /* ... */ }
func (a *AuthN) LoginURL(ctx context.Context, siteURL *url.URL, authDomain *authtypes.AuthDomain) (string, error) { /* ... */ }
func (a *AuthN) HandleCallback(ctx context.Context, query url.Values) (*authtypes.CallbackIdentity, error) { /* ... */ }
func (a *AuthN) ProviderInfo(ctx context.Context, authDomain *authtypes.AuthDomain) *authtypes.AuthNProviderInfo { /* ... */ }
```

Key differences from the Google template:

- Issuer URL comes from `authDomain.AuthDomainConfig().OIDC.Issuer` (not
  hardcoded)
- Use `IssuerAlias` if set, to work around Azure's discovery quirk
- Claim names come from `OIDC.ClaimMapping` (Google's are hardcoded)
- No Google-specific group fetching -- groups come from the ID token / userinfo
  endpoint claims directly (Entra includes them when configured to do so)

**License header:** Copy SigNoz's MIT header style. This file is brand new code,
not derived from `ee/`, so it can be safely MIT-licensed.

### 3.2 Wire it in: `pkg/signoz/authn.go`

The existing file registers email/password and Google. Add OIDC:

```go
package signoz

import (
    "context"

    "github.com/SigNoz/signoz/pkg/authn"
    "github.com/SigNoz/signoz/pkg/authn/callbackauthn/googlecallbackauthn"
    "github.com/SigNoz/signoz/pkg/authn/callbackauthn/oidccallbackauthn"  // NEW
    "github.com/SigNoz/signoz/pkg/authn/passwordauthn/emailpasswordauthn"
    "github.com/SigNoz/signoz/pkg/factory"
    "github.com/SigNoz/signoz/pkg/licensing"
    "github.com/SigNoz/signoz/pkg/types/authtypes"
)

func NewAuthNs(ctx context.Context, providerSettings factory.ProviderSettings, store authtypes.AuthNStore, licensing licensing.Licensing) (map[authtypes.AuthNProvider]authn.AuthN, error) {
    emailPasswordAuthN := emailpasswordauthn.New(store)

    googleCallbackAuthN, err := googlecallbackauthn.New(ctx, store, providerSettings)
    if err != nil {
        return nil, err
    }

    oidcCallbackAuthN, err := oidccallbackauthn.New(ctx, store, providerSettings)  // NEW
    if err != nil {
        return nil, err
    }

    return map[authtypes.AuthNProvider]authn.AuthN{
        authtypes.AuthNProviderEmailPassword: emailPasswordAuthN,
        authtypes.AuthNProviderGoogleAuth:    googleCallbackAuthN,
        authtypes.AuthNProviderOIDC:          oidcCallbackAuthN,  // NEW
    }, nil
}
```

**Note on enterprise build:** the enterprise server (`cmd/enterprise/server.go`)
calls `signoz.NewAuthNs` first and then overrides `AuthNProviderOIDC` with the
enterprise implementation. After our change, the community-edition OIDC entry
will be overwritten in the enterprise build -- no conflict, no compilation
problem. Synamedia is using community edition anyway, so the override never
fires for us.

### 3.3 That's the whole change

Two files touched:

1. **New file:** `pkg/authn/callbackauthn/oidccallbackauthn/authn.go` (~200 lines)
2. **Modified file:** `pkg/signoz/authn.go` (3 added imports/lines)

No schema changes. No new API routes. No frontend changes. No new config fields.
No enterprise files touched.

---

## 4. Configuration (post-deployment, no code)

After deploying the patched image, configure OIDC for the Synamedia domain via
the existing SigNoz Settings UI:

1. **Microsoft Entra ID -- App Registration**
   - Create an app registration
   - Redirect URI: `https://signoz.ml.engit.synamedia.com/api/v1/complete/oidc`
   - Create a client secret
   - Capture: tenant ID, client ID, client secret
   - (Optional) Configure group claims if you want role mapping from AD groups

2. **SigNoz UI -- Settings > Organization Settings > Authenticated Domains**
   - Add domain: `synamedia.com`
   - SSO Type: OIDC
   - Issuer: `https://login.microsoftonline.com/<tenant_id>/v2.0`
   - Issuer Alias: (leave blank unless Entra's discovery doc complains)
   - Client ID: from Entra
   - Client Secret: from Entra
   - Get User Info: true (Entra's ID tokens are thin)
   - Claim Mapping: email -> `email`, name -> `name`, groups -> `groups`
   - Role Mapping: optional -- map AD group object IDs to SigNoz roles (VIEWER /
     EDITOR / ADMIN)
   - Enforce SSO: true (after testing)

3. **Test in an incognito window**
   - Visit `https://signoz.ml.engit.synamedia.com`
   - Should redirect to Entra, then back, fully logged in

---

## 5. Testing strategy

Before committing the work as done:

1. **Build:** `go build ./cmd/community/` from the fork root -- must compile
2. **Lint:** `go vet ./...` should be clean for our new package
3. **Unit test:** add a minimal test for `HandleCallback` against the
   `go-oidc` test helpers (optional but nice)
4. **Integration test:** deploy the new image to the signoz-stack, configure
   against a real Entra test tenant, sign in end-to-end with a real user
5. **Negative test:** user not in the allowed Entra group -> denied (only
   relevant if we wire group-based access control in `RoleMapping`)
6. **Regression test:** existing email+password login for the root admin user
   still works (the escape hatch)

---

## 6. Maintaining the fork

To keep upstream changes flowing in:

```bash
# One-time
git remote add upstream https://github.com/SigNoz/signoz.git

# Periodically (e.g. before each demo refresh)
git fetch upstream
git checkout main
git merge upstream/main   # or: git rebase upstream/main on a feature branch
```

Since we only touch:

- `pkg/signoz/authn.go` (three additive lines)
- A brand-new file in a brand-new directory

...merge conflicts will be rare and limited to the three-line edit in
`pkg/signoz/authn.go`. The new file is entirely under our control.

---

## 7. Integration with the `signoz-stack` deployment repo

The `signoz-stack` repo (the one this proposal lives in) currently runs:

```yaml
signoz:
  image: signoz/signoz:${SIGNOZ_VERSION:-v0.118.0}
```

After this work, we have two choices for using the Synamedia-built image:

### Option A: Local build via Docker Compose (recommended for development)

Lay the directories out as siblings:

```
~/code/
  signoz-stack/      <- this repo (deployment)
  signoz/            <- the Synamedia fork (source)
```

Then in `signoz-stack/docker-compose.yaml`:

```yaml
signoz:
  image: synamedia/signoz:local
  build:
    context: ../signoz
    dockerfile: cmd/community/Dockerfile
```

Workflow:
- Edit code in `../signoz/`
- `docker compose build signoz` from `signoz-stack/`
- `docker compose up -d signoz` to roll out

### Option B: Push to a registry (recommended for stable releases)

Once the fork is stable, tag releases and push to a registry (GitHub Container
Registry / Docker Hub / Synamedia internal). Then:

```yaml
signoz:
  image: ghcr.io/synamedia/signoz:v0.118.0-entra.1
```

Tag scheme suggestion: `<upstream_version>-entra.<patch>`. So when upstream
v0.118.0 + our patch 1 = `v0.118.0-entra.1`.

---

## 8. Checklist for the implementation session

- [ ] Fork `github.com/SigNoz/signoz` to `github.com/synamedia/signoz`
- [ ] Clone it locally as a sibling of `signoz-stack`
- [ ] Move this `PROPOSAL.md` into the fork
- [ ] Create branch: `feat/community-oidc`
- [ ] Read `pkg/authn/callbackauthn/googlecallbackauthn/authn.go` end to end
- [ ] Read `pkg/types/authtypes/oidc.go` (the existing config)
- [ ] Read `pkg/modules/session/implsession/module.go::CreateCallbackAuthNSession`
      (to see what your `HandleCallback` must return)
- [ ] Implement `pkg/authn/callbackauthn/oidccallbackauthn/authn.go`
- [ ] Wire into `pkg/signoz/authn.go`
- [ ] `go build ./cmd/community/` to verify it compiles
- [ ] Update `signoz-stack/docker-compose.yaml` to use the local build
- [ ] Deploy and configure against a real Entra tenant
- [ ] Open PR (against the Synamedia fork's main branch)
- [ ] Document the Entra setup steps for the team in a runbook

---

## 9. Open questions

- **Naming:** should the new package be `oidccallbackauthn` (matching the
  enterprise one) or something distinctive like `entracallbackauthn` /
  `genericoidccallbackauthn`? Matching the enterprise name keeps the import
  path natural and the diff small. Recommend `oidccallbackauthn`.
- **Group claim handling:** Entra includes group object IDs (UUIDs), not group
  names. Make sure the `ClaimMapping` and `RoleMapping` flow tolerates this.
- **`InsecureSkipEmailVerified`:** Entra doesn't always set `email_verified`.
  Default to `true` for Entra, or document that admins must toggle it on.
- **Logout:** out of scope for this proposal. SigNoz's logout just clears
  local tokens; an Entra-aware logout that hits the OIDC end_session_endpoint
  could be a follow-up.
