package global

import (
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/SigNoz/signoz/pkg/errors"

	"github.com/SigNoz/signoz/pkg/factory"
)

var (
	ErrCodeInvalidGlobalConfig = errors.MustNewCode("invalid_global_config")
)

type Config struct {
	ExternalURL    *url.URL `mapstructure:"external_url"`
	IngestionURL   *url.URL `mapstructure:"ingestion_url"`
	MCPURL         *url.URL `mapstructure:"mcp_url"`
	AIAssistantURL *url.URL `mapstructure:"ai_assistant_url"`

	// LoopbackRedirect controls whether interactive SSO is permitted to deliver
	// the freshly minted session token to a loopback address (e.g. a local MCP
	// server acting "as" the operator) instead of the ExternalURL origin.
	// Disabled by default; see pkg/authn/callbackauthn/oidccallbackauthn.
	LoopbackRedirect LoopbackRedirectConfig `mapstructure:"loopback_redirect"`
}

type LoopbackRedirectConfig struct {
	// Enabled turns on loopback delivery. When false, SSO only ever redirects to
	// the configured ExternalURL origin.
	Enabled bool `mapstructure:"enabled"`

	// Ports is the allowlist of loopback ports the post-login redirect may target.
	// Only these ports on 127.0.0.1/::1 are accepted.
	//
	// Stored as strings (not []int) because env values arrive here as a single
	// element (e.g. ["47823,47824"]) rather than a split list; AllowedPorts()
	// normalises both that form and a YAML list by splitting each entry on commas.
	Ports []string `mapstructure:"ports"`
}

// AllowedPorts returns the configured loopback ports, flattened and trimmed. It
// tolerates an env value that arrives as a single comma-joined element (e.g.
// ["47823,47824"]) as well as a normal list form (["47823","47824"]) by
// splitting every entry on commas.
func (c LoopbackRedirectConfig) AllowedPorts() []string {
	out := make([]string, 0, len(c.Ports))
	for _, entry := range c.Ports {
		for _, p := range strings.Split(entry, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func NewConfigFactory() factory.ConfigFactory {
	return factory.NewConfigFactory(factory.MustNewName("global"), newConfig)
}

func newConfig() factory.Config {
	return &Config{
		ExternalURL: &url.URL{
			Scheme: "",
			Host:   "<unset>",
			Path:   "",
		},
		IngestionURL: &url.URL{
			Scheme: "",
			Host:   "<unset>",
			Path:   "",
		},
		// Loopback delivery is OFF by default. These are the default allowlisted
		// ports (the MCP server's fixed loopback range) used only once the feature
		// is explicitly enabled via loopback_redirect.enabled.
		LoopbackRedirect: LoopbackRedirectConfig{
			Enabled: false,
			Ports:   []string{"47823", "47824", "47825", "47826", "47827", "47828", "47829", "47830", "47831", "47832"},
		},
	}
}

func (c Config) Validate() error {
	if c.ExternalURL != nil {
		if c.ExternalURL.Path != "" && c.ExternalURL.Path != "/" {
			if !strings.HasPrefix(c.ExternalURL.Path, "/") {
				return errors.NewInvalidInputf(ErrCodeInvalidGlobalConfig, "global::external_url path must start with '/', got %q", c.ExternalURL.Path)
			}
		}
	}

	if c.LoopbackRedirect.Enabled {
		ports := c.LoopbackRedirect.AllowedPorts()
		if len(ports) == 0 {
			return errors.NewInvalidInputf(ErrCodeInvalidGlobalConfig, "global::loopback_redirect requires at least one port in 'ports' when enabled")
		}
		for _, p := range ports {
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 || n > 65535 {
				return errors.NewInvalidInputf(ErrCodeInvalidGlobalConfig, "global::loopback_redirect port %q is invalid (must be an integer 1-65535)", p)
			}
		}
	}

	return nil
}

func (c Config) ExternalPath() string {
	if c.ExternalURL == nil || c.ExternalURL.Path == "" || c.ExternalURL.Path == "/" {
		return ""
	}

	p := path.Clean("/" + c.ExternalURL.Path)
	if p == "/" {
		return ""
	}

	return p
}

func (c Config) ExternalPathTrailing() string {
	if p := c.ExternalPath(); p != "" {
		return p + "/"
	}

	return "/"
}
