package oidccallbackauthn

import (
	"context"
	"log/slog"
	"net/url"
	"path"
	"slices"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/SigNoz/signoz/pkg/authn"
	"github.com/SigNoz/signoz/pkg/errors"
	"github.com/SigNoz/signoz/pkg/factory"
	"github.com/SigNoz/signoz/pkg/global"
	"github.com/SigNoz/signoz/pkg/http/client"
	"github.com/SigNoz/signoz/pkg/types/authtypes"
	"github.com/SigNoz/signoz/pkg/valuer"
)

const redirectPath string = "/api/v1/complete/oidc"

// Default scopes. "openid" is mandatory for OIDC; email/profile yield the
// standard identity claims. Group claims for providers such as Entra ID are not
// requested via a scope -- they are emitted as (optional) ID-token / userinfo
// claims that the provider is configured to include.
var scopes []string = []string{oidc.ScopeOpenID, "email", "profile"}

var _ authn.CallbackAuthN = (*AuthN)(nil)

type AuthN struct {
	store        authtypes.AuthNStore
	settings     factory.ScopedProviderSettings
	httpClient   *client.Client
	globalConfig global.Config
}

func New(ctx context.Context, store authtypes.AuthNStore, providerSettings factory.ProviderSettings, globalConfig global.Config) (*AuthN, error) {
	settings := factory.NewScopedProviderSettings(providerSettings, "github.com/SigNoz/signoz/pkg/authn/callbackauthn/oidccallbackauthn")

	httpClient, err := client.New(settings.Logger(), providerSettings.TracerProvider, providerSettings.MeterProvider)
	if err != nil {
		return nil, err
	}

	return &AuthN{
		store:        store,
		settings:     settings,
		httpClient:   httpClient,
		globalConfig: globalConfig,
	}, nil
}

func (a *AuthN) LoginURL(ctx context.Context, siteURL *url.URL, authDomain *authtypes.AuthDomain) (string, error) {
	if authDomain.AuthDomainConfig().AuthNProvider != authtypes.AuthNProviderOIDC {
		return "", errors.Newf(errors.TypeInternal, authtypes.ErrCodeAuthDomainMismatch, "domain type is not oidc")
	}

	config := authDomain.AuthDomainConfig().OIDC
	if config == nil {
		return "", errors.New(errors.TypeInvalidInput, errors.CodeInvalidInput, "oidc: config is not set on the auth domain")
	}

	// The siteURL becomes the post-login token-delivery target (carried in the
	// state). Reject it early unless it is the configured external origin or a
	// permitted loopback address.
	if err := a.validateDeliveryTarget(siteURL); err != nil {
		return "", err
	}

	ctx = a.clientContext(ctx)
	oidcProvider, err := a.newProvider(ctx, config)
	if err != nil {
		return "", err
	}

	oauth2Config, err := a.oauth2Config(config, oidcProvider)
	if err != nil {
		return "", err
	}

	return oauth2Config.AuthCodeURL(
		authtypes.NewState(siteURL, authDomain.StorableAuthDomain().ID).URL.String(),
	), nil
}

func (a *AuthN) HandleCallback(ctx context.Context, query url.Values) (*authtypes.CallbackIdentity, error) {
	ctx = a.clientContext(ctx)

	if err := query.Get("error"); err != "" {
		a.settings.Logger().ErrorContext(ctx, "oidc: error while authenticating", slog.String("error", err), slog.String("error_description", query.Get("error_description")))
		return nil, errors.Newf(errors.TypeInternal, errors.CodeInternal, "oidc: error while authenticating").WithAdditional(query.Get("error_description"))
	}

	state, err := authtypes.NewStateFromString(query.Get("state"))
	if err != nil {
		a.settings.Logger().ErrorContext(ctx, "oidc: invalid state", errors.Attr(err))
		return nil, errors.Newf(errors.TypeInvalidInput, authtypes.ErrCodeInvalidState, "oidc: invalid state").WithAdditional(err.Error())
	}

	// Authoritative guardrail: validate the token-delivery target before the token
	// exchange (and well before any session token is minted). A crafted state must
	// not be able to deliver a token to an arbitrary host.
	if err := a.validateDeliveryTarget(state.URL); err != nil {
		a.settings.Logger().ErrorContext(ctx, "oidc: disallowed redirect target", errors.Attr(err), slog.String("target", state.URL.String()))
		return nil, err
	}

	authDomain, err := a.store.GetAuthDomainFromID(ctx, state.DomainID)
	if err != nil {
		return nil, err
	}

	config := authDomain.AuthDomainConfig().OIDC
	if config == nil {
		return nil, errors.New(errors.TypeInvalidInput, errors.CodeInvalidInput, "oidc: config is not set on the auth domain")
	}

	oidcProvider, err := a.newProvider(ctx, config)
	if err != nil {
		return nil, err
	}

	oauth2Config, err := a.oauth2Config(config, oidcProvider)
	if err != nil {
		return nil, err
	}

	token, err := oauth2Config.Exchange(ctx, query.Get("code"))
	if err != nil {
		var retrieveError *oauth2.RetrieveError
		if errors.As(err, &retrieveError) {
			a.settings.Logger().ErrorContext(ctx, "oidc: failed to get token", errors.Attr(err), slog.String("error_description", retrieveError.ErrorDescription), slog.String("body", string(retrieveError.Body)))
			return nil, errors.Newf(errors.TypeForbidden, errors.CodeForbidden, "oidc: failed to get token").WithAdditional(retrieveError.ErrorDescription)
		}

		a.settings.Logger().ErrorContext(ctx, "oidc: failed to get token", errors.Attr(err))
		return nil, errors.Newf(errors.TypeInternal, errors.CodeInternal, "oidc: failed to get token")
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New(errors.TypeInvalidInput, errors.CodeInvalidInput, "oidc: no id_token in token response")
	}

	verifier := oidcProvider.Verifier(&oidc.Config{ClientID: config.ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		a.settings.Logger().ErrorContext(ctx, "oidc: failed to verify token", errors.Attr(err))
		return nil, errors.Newf(errors.TypeForbidden, errors.CodeForbidden, "oidc: failed to verify token")
	}

	// Start from the ID-token claims. Entra ID issues "thin" ID tokens, so when
	// GetUserInfo is set we overlay the richer userinfo claims on top.
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		a.settings.Logger().ErrorContext(ctx, "oidc: missing or invalid claims", errors.Attr(err))
		return nil, errors.Newf(errors.TypeForbidden, errors.CodeForbidden, "oidc: missing or invalid claims").WithAdditional(err.Error())
	}

	if config.GetUserInfo {
		userInfo, err := oidcProvider.UserInfo(ctx, oauth2Config.TokenSource(ctx, token))
		if err != nil {
			a.settings.Logger().ErrorContext(ctx, "oidc: failed to fetch userinfo", errors.Attr(err))
			return nil, errors.Newf(errors.TypeInternal, errors.CodeInternal, "oidc: failed to fetch userinfo").WithAdditional(err.Error())
		}

		userInfoClaims := map[string]any{}
		if err := userInfo.Claims(&userInfoClaims); err != nil {
			a.settings.Logger().ErrorContext(ctx, "oidc: invalid userinfo claims", errors.Attr(err))
			return nil, errors.Newf(errors.TypeInternal, errors.CodeInternal, "oidc: invalid userinfo claims").WithAdditional(err.Error())
		}

		for key, value := range userInfoClaims {
			claims[key] = value
		}
	}

	mapping := config.ClaimMapping
	emailValue := claimString(claims, orDefault(mapping.Email, "email"))
	nameValue := claimString(claims, orDefault(mapping.Name, "name"))
	groups := claimStringSlice(claims, orDefault(mapping.Groups, "groups"))
	role := claimString(claims, orDefault(mapping.Role, "role"))

	if !config.InsecureSkipEmailVerified {
		if !claimBool(claims, "email_verified") {
			a.settings.Logger().ErrorContext(ctx, "oidc: email is not verified", slog.String("email", emailValue))
			return nil, errors.Newf(errors.TypeForbidden, errors.CodeForbidden, "oidc: email is not verified")
		}
	}

	email, err := valuer.NewEmail(emailValue)
	if err != nil {
		return nil, errors.Newf(errors.TypeInvalidInput, errors.CodeInvalidInput, "oidc: failed to parse email").WithAdditional(err.Error())
	}

	return authtypes.NewCallbackIdentity(nameValue, email, authDomain.StorableAuthDomain().OrgID, state, groups, role), nil
}

func (a *AuthN) ProviderInfo(ctx context.Context, authDomain *authtypes.AuthDomain) *authtypes.AuthNProviderInfo {
	return &authtypes.AuthNProviderInfo{
		RelayStatePath: nil,
	}
}

// newProvider performs OIDC discovery against the configured Issuer. Some
// off-spec providers (Azure / Entra ID multi-tenant endpoints, Oracle IDCS)
// report an "issuer" in their discovery document that differs from the URL used
// to discover it, which would otherwise fail issuer validation. When IssuerAlias
// is set we use it as the authoritative issuer for token validation while still
// discovering against Issuer -- the documented go-oidc work-around.
func (a *AuthN) newProvider(ctx context.Context, config *authtypes.OIDCConfig) (*oidc.Provider, error) {
	if config.IssuerAlias != "" {
		ctx = oidc.InsecureIssuerURLContext(ctx, config.IssuerAlias)
	}

	provider, err := oidc.NewProvider(ctx, config.Issuer)
	if err != nil {
		a.settings.Logger().ErrorContext(ctx, "oidc: failed to discover provider", errors.Attr(err), slog.String("issuer", config.Issuer))
		return nil, errors.Newf(errors.TypeInternal, errors.CodeInternal, "oidc: failed to discover provider").WithAdditional(err.Error())
	}

	return provider, nil
}

func (a *AuthN) oauth2Config(config *authtypes.OIDCConfig, provider *oidc.Provider) (*oauth2.Config, error) {
	redirectURL, err := a.redirectURL()
	if err != nil {
		return nil, err
	}

	return &oauth2.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		Endpoint:     provider.Endpoint(),
		Scopes:       scopes,
		RedirectURL:  redirectURL,
	}, nil
}

// redirectURL is the fixed OAuth redirect_uri, sourced from SigNoz's own
// configured external URL (NOT from the client-supplied ref/siteURL). It must be
// identical between the auth request and the token exchange, and is the URL
// registered with the IdP. Pinning it here is what lets the post-login delivery
// target be a validated loopback address while the IdP only ever redirects back
// to SigNoz.
func (a *AuthN) redirectURL() (string, error) {
	ext := a.globalConfig.ExternalURL
	if ext == nil || ext.Scheme == "" || ext.Host == "" || ext.Host == "<unset>" {
		return "", errors.New(errors.TypeInvalidInput, errors.CodeInvalidInput, "oidc: global external_url must be configured with a scheme and host to build the OIDC redirect_uri")
	}

	return (&url.URL{
		Scheme: ext.Scheme,
		Host:   ext.Host,
		Path:   path.Join(a.globalConfig.ExternalPath(), redirectPath),
	}).String(), nil
}

// validateDeliveryTarget enforces where SigNoz may deliver the freshly minted
// session token after login. Because the OAuth redirect_uri is now decoupled
// from this target (see redirectURL), an unrestricted target would be a
// session-token-exfiltration open redirect. The only permitted targets are:
//   - SigNoz's own configured external origin (the standard browser flow), or
//   - a loopback address (127.0.0.1 / ::1) on an allowlisted port, and only when
//     loopback delivery is explicitly enabled (off by default).
func (a *AuthN) validateDeliveryTarget(target *url.URL) error {
	if target == nil {
		return errors.New(errors.TypeInvalidInput, errors.CodeInvalidInput, "oidc: redirect target is empty")
	}

	if ext := a.globalConfig.ExternalURL; ext != nil && ext.Host != "" && ext.Host != "<unset>" &&
		target.Scheme == ext.Scheme && target.Host == ext.Host {
		return nil
	}

	loopback := a.globalConfig.LoopbackRedirect
	if !loopback.Enabled {
		return errors.Newf(errors.TypeForbidden, errors.CodeForbidden, "oidc: redirect target host %q is not allowed", target.Host)
	}

	if target.Scheme != "http" {
		return errors.New(errors.TypeForbidden, errors.CodeForbidden, "oidc: loopback redirect must use the http scheme")
	}

	if host := target.Hostname(); host != "127.0.0.1" && host != "::1" {
		return errors.Newf(errors.TypeForbidden, errors.CodeForbidden, "oidc: loopback redirect host must be 127.0.0.1 or ::1, got %q", host)
	}

	if target.User != nil {
		return errors.New(errors.TypeForbidden, errors.CodeForbidden, "oidc: loopback redirect must not contain userinfo")
	}

	port := target.Port()
	if port == "" {
		return errors.New(errors.TypeForbidden, errors.CodeForbidden, "oidc: loopback redirect must specify a port")
	}

	if !slices.Contains(loopback.AllowedPorts(), port) {
		return errors.Newf(errors.TypeForbidden, errors.CodeForbidden, "oidc: loopback redirect port %s is not in the allowlist", port)
	}

	return nil
}

// clientContext injects the provider-managed HTTP client (tracing, metrics,
// proxy settings) so that both go-oidc discovery/userinfo and the oauth2 token
// exchange route through it -- they all read the oauth2.HTTPClient context key.
func (a *AuthN) clientContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, a.httpClient.Client())
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func claimString(claims map[string]any, key string) string {
	if value, ok := claims[key].(string); ok {
		return value
	}
	return ""
}

// claimBool tolerates providers that emit boolean claims (e.g. email_verified)
// as either a JSON bool or a string.
func claimBool(claims map[string]any, key string) bool {
	switch value := claims[key].(type) {
	case bool:
		return value
	case string:
		return value == "true"
	default:
		return false
	}
}

// claimStringSlice tolerates group/role claims arriving as a JSON array of
// strings, a JSON array of arbitrary values, or a single string. Entra ID
// returns group object IDs (UUIDs) as an array of strings.
func claimStringSlice(claims map[string]any, key string) []string {
	switch value := claims[key].(type) {
	case []string:
		return value
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	case string:
		if value == "" {
			return nil
		}
		return []string{value}
	default:
		return nil
	}
}
