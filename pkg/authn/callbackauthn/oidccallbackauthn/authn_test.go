package oidccallbackauthn

import (
	"net/url"
	"testing"

	"github.com/SigNoz/signoz/pkg/global"
	"github.com/stretchr/testify/require"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

func TestValidateDeliveryTarget(t *testing.T) {
	externalURL := mustURL(t, "https://signoz.example.com")

	loopbackOn := global.Config{
		ExternalURL:      externalURL,
		LoopbackRedirect: global.LoopbackRedirectConfig{Enabled: true, Ports: []int{8765, 8766}},
	}
	loopbackOff := global.Config{
		ExternalURL:      externalURL,
		LoopbackRedirect: global.LoopbackRedirectConfig{Enabled: false},
	}

	tests := []struct {
		name    string
		cfg     global.Config
		target  string
		allowed bool
	}{
		{name: "external origin allowed", cfg: loopbackOff, target: "https://signoz.example.com/login", allowed: true},
		{name: "external origin allowed even with loopback on", cfg: loopbackOn, target: "https://signoz.example.com/x", allowed: true},
		{name: "wrong scheme for external origin rejected", cfg: loopbackOff, target: "http://signoz.example.com/login", allowed: false},
		{name: "arbitrary external host rejected when loopback off", cfg: loopbackOff, target: "https://evil.example/cb", allowed: false},
		{name: "loopback rejected when disabled", cfg: loopbackOff, target: "http://127.0.0.1:8765/cb", allowed: false},
		{name: "loopback 127.0.0.1 allowlisted port allowed", cfg: loopbackOn, target: "http://127.0.0.1:8765/cb", allowed: true},
		{name: "loopback ::1 allowlisted port allowed", cfg: loopbackOn, target: "http://[::1]:8766/cb", allowed: true},
		{name: "loopback port not in allowlist rejected", cfg: loopbackOn, target: "http://127.0.0.1:9999/cb", allowed: false},
		{name: "loopback https rejected", cfg: loopbackOn, target: "https://127.0.0.1:8765/cb", allowed: false},
		{name: "loopback with userinfo rejected", cfg: loopbackOn, target: "http://user@127.0.0.1:8765/cb", allowed: false},
		{name: "non-loopback host rejected when loopback on", cfg: loopbackOn, target: "http://evil.example:8765/cb", allowed: false},
		{name: "0.0.0.0 rejected", cfg: loopbackOn, target: "http://0.0.0.0:8765/cb", allowed: false},
		{name: "loopback without port rejected", cfg: loopbackOn, target: "http://127.0.0.1/cb", allowed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &AuthN{globalConfig: tt.cfg}
			err := a.validateDeliveryTarget(mustURL(t, tt.target))
			if tt.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}

	t.Run("nil target rejected", func(t *testing.T) {
		a := &AuthN{globalConfig: loopbackOn}
		require.Error(t, a.validateDeliveryTarget(nil))
	})
}

func TestRedirectURLRequiresExternalURL(t *testing.T) {
	// Unset ExternalURL (the default) must produce a clear error rather than a
	// broken redirect_uri.
	a := &AuthN{globalConfig: global.Config{ExternalURL: &url.URL{Scheme: "", Host: "<unset>"}}}
	_, err := a.redirectURL()
	require.Error(t, err)

	a = &AuthN{globalConfig: global.Config{ExternalURL: mustURL(t, "https://signoz.example.com/signoz")}}
	got, err := a.redirectURL()
	require.NoError(t, err)
	require.Equal(t, "https://signoz.example.com/signoz/api/v1/complete/oidc", got)
}
