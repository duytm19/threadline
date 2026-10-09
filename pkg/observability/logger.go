package observability

import (
	"context"
	"io"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/trace"
)

// ContextHandler wraps an slog.Handler to inject OpenTelemetry trace and tenant context automatically.
type ContextHandler struct {
	slog.Handler
}

// NewContextHandler creates a ContextHandler wrapping the target handler.
func NewContextHandler(h slog.Handler) *ContextHandler {
	return &ContextHandler{Handler: h}
}

// Handle appends trace_id, span_id, and tenant_id from context to every log record.
func (h *ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx != nil {
		// OpenTelemetry trace extraction
		span := trace.SpanFromContext(ctx)
		if span.SpanContext().IsValid() {
			r.AddAttrs(
				slog.String("trace_id", span.SpanContext().TraceID().String()),
				slog.String("span_id", span.SpanContext().SpanID().String()),
			)
		}

		// Tenant ID extraction if present in context
		if tenantVal := ctx.Value("tenant_id"); tenantVal != nil {
			if tenantID, ok := tenantVal.(string); ok && tenantID != "" {
				r.AddAttrs(slog.String("tenant_id", tenantID))
			}
		}

		// Request ID extraction if present
		if reqVal := ctx.Value("request_id"); reqVal != nil {
			if reqID, ok := reqVal.(string); ok && reqID != "" {
				r.AddAttrs(slog.String("request_id", reqID))
			}
		}
	}
	return h.Handler.Handle(ctx, r)
}

// NewJSONLogger initializes an enterprise-ready JSON structured logger.
func NewJSONLogger(serviceName, env string, out io.Writer) *slog.Logger {
	if out == nil {
		out = os.Stdout
	}

	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
		AddSource: true,
	}
	if env == "development" || env == "local" {
		opts.Level = slog.LevelDebug
	}

	jsonHandler := slog.NewJSONHandler(out, opts)
	contextHandler := NewContextHandler(jsonHandler)

	logger := slog.New(contextHandler).With(
		slog.String("service", serviceName),
		slog.String("env", env),
	)

	return logger
}
