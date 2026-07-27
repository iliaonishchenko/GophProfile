package observability

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMetricsInstancesAreIsolated(t *testing.T) {
	first := NewMetrics()
	second := NewMetrics()
	first.RecordUpload(errors.New("upload failed"))

	firstRecorder := httptest.NewRecorder()
	first.Handler().ServeHTTP(firstRecorder, httptest.NewRequest("GET", "/metrics", nil))
	secondRecorder := httptest.NewRecorder()
	second.Handler().ServeHTTP(secondRecorder, httptest.NewRequest("GET", "/metrics", nil))

	require.Contains(t, firstRecorder.Body.String(), `gophprofile_avatar_uploads_total{result="error"} 1`)
	require.NotContains(t, secondRecorder.Body.String(), "gophprofile_avatar_uploads_total")
}
