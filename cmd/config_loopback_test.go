package cmd

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression test for the loopback port allowlist set via environment variable.
// koanf delivers the comma-joined env value as a single-element slice (e.g.
// ["47823,47824,47825"]); AllowedPorts() must split it back out. A previous
// []int field failed here with "cannot parse value as 'int'".
func TestNewSigNozConfig_LoopbackPortsFromEnv(t *testing.T) {
	t.Setenv("SIGNOZ_GLOBAL_LOOPBACK__REDIRECT_ENABLED", "true")
	t.Setenv("SIGNOZ_GLOBAL_LOOPBACK__REDIRECT_PORTS", "47823,47824,47825")

	logger := slog.New(slog.DiscardHandler)
	config, err := NewSigNozConfig(context.Background(), logger, nil)
	require.NoError(t, err)

	assert.True(t, config.Global.LoopbackRedirect.Enabled)
	assert.Equal(t, []string{"47823", "47824", "47825"}, config.Global.LoopbackRedirect.AllowedPorts())
}
