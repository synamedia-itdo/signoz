package global

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewConfigLoopbackRedirectDefault(t *testing.T) {
	cfg, ok := newConfig().(*Config)
	assert.True(t, ok)
	// Off by default; ports default to the MCP server's fixed loopback range.
	assert.False(t, cfg.LoopbackRedirect.Enabled)
	assert.Equal(t, []string{"47823", "47824", "47825", "47826", "47827", "47828", "47829", "47830", "47831", "47832"}, cfg.LoopbackRedirect.AllowedPorts())
}

func TestExternalPath(t *testing.T) {
	testCases := []struct {
		name     string
		config   Config
		expected string
	}{
		{
			name:     "NilURL",
			config:   Config{ExternalURL: nil},
			expected: "",
		},
		{
			name:     "EmptyPath",
			config:   Config{ExternalURL: &url.URL{Scheme: "https", Host: "example.com", Path: ""}},
			expected: "",
		},
		{
			name:     "RootPath",
			config:   Config{ExternalURL: &url.URL{Scheme: "https", Host: "example.com", Path: "/"}},
			expected: "",
		},
		{
			name:     "SingleSegment",
			config:   Config{ExternalURL: &url.URL{Scheme: "https", Host: "example.com", Path: "/signoz"}},
			expected: "/signoz",
		},
		{
			name:     "TrailingSlash",
			config:   Config{ExternalURL: &url.URL{Scheme: "https", Host: "example.com", Path: "/signoz/"}},
			expected: "/signoz",
		},
		{
			name:     "MultiSegment",
			config:   Config{ExternalURL: &url.URL{Scheme: "https", Host: "example.com", Path: "/a/b/c"}},
			expected: "/a/b/c",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.config.ExternalPath())
		})
	}
}

func TestExternalPathTrailing(t *testing.T) {
	testCases := []struct {
		name     string
		config   Config
		expected string
	}{
		{
			name:     "NilURL",
			config:   Config{ExternalURL: nil},
			expected: "/",
		},
		{
			name:     "EmptyPath",
			config:   Config{ExternalURL: &url.URL{Path: ""}},
			expected: "/",
		},
		{
			name:     "RootPath",
			config:   Config{ExternalURL: &url.URL{Path: "/"}},
			expected: "/",
		},
		{
			name:     "SingleSegment",
			config:   Config{ExternalURL: &url.URL{Path: "/signoz"}},
			expected: "/signoz/",
		},
		{
			name:     "MultiSegment",
			config:   Config{ExternalURL: &url.URL{Path: "/a/b/c"}},
			expected: "/a/b/c/",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.config.ExternalPathTrailing())
		})
	}
}

func TestValidate(t *testing.T) {
	testCases := []struct {
		name   string
		config Config
		fail   bool
	}{
		{
			name:   "NilURL",
			config: Config{ExternalURL: nil},
			fail:   false,
		},
		{
			name:   "EmptyPath",
			config: Config{ExternalURL: &url.URL{Path: ""}},
			fail:   false,
		},
		{
			name:   "RootPath",
			config: Config{ExternalURL: &url.URL{Path: "/"}},
			fail:   false,
		},
		{
			name:   "ValidPath",
			config: Config{ExternalURL: &url.URL{Path: "/signoz"}},
			fail:   false,
		},
		{
			name:   "NoLeadingSlash",
			config: Config{ExternalURL: &url.URL{Path: "signoz"}},
			fail:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()
			if tc.fail {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
		})
	}
}
