package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetAvatarDoesNotAdvertiseUnsupportedFormat(t *testing.T) {
	specification, err := GetSwagger()
	require.NoError(t, err)

	path := specification.Paths.Find("/api/v1/avatars/{avatar_id}")
	require.NotNil(t, path)
	require.NotNil(t, path.Get)
	for _, parameter := range path.Get.Parameters {
		require.NotNil(t, parameter.Value)
		require.NotEqual(t, "format", parameter.Value.Name)
	}
}

func TestHealthEndpointsAreDocumented(t *testing.T) {
	specification, err := GetSwagger()
	require.NoError(t, err)

	require.NotNil(t, specification.Paths.Find("/health"))
	require.NotNil(t, specification.Paths.Find("/live"))
}
