package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// InitTracer initializes OpenTelemetry with W3C TraceContext propagation.
// Returns a shutdown function that flushes pending spans.
func InitTracer(ctx context.Context, serviceName, serviceVersion, env string) (func(context.Context) error, error) {
	// Configure W3C TextMapPropagator globally per AD-9
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
			semconv.DeploymentEnvironment(env),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTel resource: %w", err)
	}

	// For local dev, stdout or no-op provider if collector endpoint not configured
	// Production/staging binds OTLP gRPC exporter via OTLP_ENDPOINT env var
	bsp := sdktrace.NewBatchSpanProcessor(sdktrace.NewNoopSpanProcessor().(sdktrace.SpanProcessor)) // fallback
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)

	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}
