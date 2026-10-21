package httpx

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/threadline/pkg/tenant"
	"go.opentelemetry.io/otel/trace"
)

type contextKey string

const (
	HeaderRequestID = "X-Request-ID"
	HeaderTenantID  = tenant.HeaderTenantID

	requestContextKey contextKey = "request_id"
)

// WithTenant stores the tenant ID in context as TenantContext.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	tc, err := tenant.New(tenantID)
	if err != nil {
		return ctx
	}
	return tenant.WithContext(ctx, tc)
}

// TenantFromContext extracts the tenant ID from context.
func TenantFromContext(ctx context.Context) (string, bool) {
	id, err := tenant.TenantIDFromContext(ctx)
	if err != nil || id == "" {
		return "", false
	}
	return id, true
}

// MustTenantFromContext extracts tenant or panics if unset (used in tenant-bound usecases).
func MustTenantFromContext(ctx context.Context) string {
	return tenant.MustTenantIDFromContext(ctx)
}

// RequestIDFromContext extracts request ID from context.
func RequestIDFromContext(ctx context.Context) string {
	val, ok := ctx.Value(requestContextKey).(string)
	if !ok {
		return ""
	}
	return val
}

// MiddlewareRequestID injects or generates X-Request-ID.
func MiddlewareRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(HeaderRequestID)
		if reqID == "" {
			newID, err := uuid.NewV7()
			if err != nil {
				reqID = uuid.NewString()
			} else {
				reqID = newID.String()
			}
		}

		ctx := context.WithValue(r.Context(), requestContextKey, reqID)
		w.Header().Set(HeaderRequestID, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// MiddlewareTenantContext extracts, validates, and enforces X-Tenant-ID.
func MiddlewareTenantContext(required bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenantID := r.Header.Get(HeaderTenantID)
			if tenantID == "" {
				if required {
					WriteProblem(w, r, BadRequest("X-Tenant-ID header is required"))
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			tc, err := tenant.New(tenantID)
			if err != nil {
				WriteProblem(w, r, BadRequest(fmt.Sprintf("Invalid X-Tenant-ID: %v", err)))
				return
			}

			ctx := tenant.WithContext(r.Context(), tc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// MiddlewareRecoverer catches panics and formats an RFC 7807 response.
func MiddlewareRecoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rvr := recover(); rvr != nil {
					err, ok := rvr.(error)
					if !ok {
						err = fmt.Errorf("%v", rvr)
					}

					span := trace.SpanFromContext(r.Context())
					traceID := ""
					if span.SpanContext().IsValid() {
						traceID = span.SpanContext().TraceID().String()
					}

					logger.ErrorContext(r.Context(), "unhandled panic recovered",
						slog.String("error", err.Error()),
						slog.String("trace_id", traceID),
						slog.String("path", r.URL.Path),
						slog.String("method", r.Method),
					)

					WriteProblem(w, r, InternalServerError("An unexpected server error occurred"))
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
