package observability

import (
	"context"
	"errors"
	"net/http"
	"os"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"log/slog"
)

const instrumentationName = "github.com/iliaonishchenko/GophProfile"

func Setup(ctx context.Context, serviceName, endpoint string) (func(context.Context) error, error) {
	setupLogger(serviceName)
	if endpoint == "" {
		otel.SetTracerProvider(noop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}
	resource, err := sdkresource.Merge(
		sdkresource.Default(),
		sdkresource.NewSchemaless(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion("iteration-2"),
		),
	)
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return provider.Shutdown, nil
}

func Tracer() trace.Tracer {
	return otel.Tracer(instrumentationName)
}

func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, trace.WithAttributes(attrs...))
}

func End(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

func TraceHTTPHandler(next http.Handler, operation string) http.Handler {
	return otelhttp.NewHandler(
		next,
		operation,
		otelhttp.WithFilter(func(request *http.Request) bool {
			return request.URL.Path != "/metrics"
		}),
	)
}

type traceHandler struct {
	slog.Handler
	service string
}

func (h traceHandler) Handle(ctx context.Context, record slog.Record) error {
	record.AddAttrs(slog.String("service", h.service))
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		record.AddAttrs(
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, record)
}

func setupLogger(serviceName string) {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(traceHandler{Handler: handler, service: serviceName}))
}

func JoinedShutdown(functions ...func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		var result error
		for _, shutdown := range functions {
			result = errors.Join(result, shutdown(ctx))
		}
		return result
	}
}
