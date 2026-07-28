package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadDefaultsAndOverrides(t *testing.T) {
	t.Setenv("HTTP_ADDRESS", "127.0.0.1:9090")
	t.Setenv("S3_USE_SSL", "true")
	t.Setenv("SHUTDOWN_PERIOD", "3s")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:9090", cfg.HTTPAddress)
	require.True(t, cfg.S3UseSSL)
	require.Equal(t, "avatars.exchange", cfg.AMQPExchange)
	require.Equal(t, "3s", cfg.ShutdownPeriod.String())
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Setenv("S3_USE_SSL", "sometimes")
	_, err := Load()
	require.Error(t, err)
}
