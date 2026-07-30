package observability

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceHTTPHandlerContinuesIncomingTrace(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	otel.SetTracerProvider(sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	))
	otel.SetTextMapPropagator(propagation.TraceContext{})

	var spanContext trace.SpanContext
	handler := TraceHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spanContext = trace.SpanContextFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}), "test-server")

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set(
		"traceparent",
		"00-11111111111111111111111111111111-2222222222222222-01",
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.True(t, spanContext.IsValid())
	require.Equal(t, "11111111111111111111111111111111", spanContext.TraceID().String())
	require.NotEqual(t, "2222222222222222", spanContext.SpanID().String())
}
