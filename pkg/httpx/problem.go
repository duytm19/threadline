package httpx

import (
	"encoding/json"
	"net/http"

	"go.opentelemetry.io/otel/trace"
)

// ContentTypeProblemJSON represents the RFC 7807 MIME type.
const ContentTypeProblemJSON = "application/problem+json"

// ValidationError encapsulates granular field-level validation errors.
type ValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ProblemDetails represents an RFC 7807 compliant error envelope.
type ProblemDetails struct {
	Type     string            `json:"type"`
	Title    string            `json:"title"`
	Status   int               `json:"status"`
	Code     string            `json:"code"`
	Detail   string            `json:"detail,omitempty"`
	Instance string            `json:"instance,omitempty"`
	TraceID  string            `json:"trace_id,omitempty"`
	Errors   []ValidationError `json:"errors,omitempty"`
}

// WriteProblem writes an RFC 7807 Problem Details JSON response.
func WriteProblem(w http.ResponseWriter, r *http.Request, problem ProblemDetails) {
	if problem.Instance == "" && r != nil {
		problem.Instance = r.URL.Path
	}
	if problem.TraceID == "" && r != nil {
		span := trace.SpanFromContext(r.Context())
		if span.SpanContext().IsValid() {
			problem.TraceID = span.SpanContext().TraceID().String()
		}
	}
	if problem.Type == "" {
		problem.Type = "https://api.threadline.dev/problems/" + problem.Code
	}

	w.Header().Set("Content-Type", ContentTypeProblemJSON)
	w.WriteHeader(problem.Status)
	_ = json.NewEncoder(w).Encode(problem)
}

// NewProblem creates a standard ProblemDetails instance.
func NewProblem(status int, code, title, detail string) ProblemDetails {
	return ProblemDetails{
		Status: status,
		Code:   code,
		Title:  title,
		Detail: detail,
	}
}

// Common error constructors adhering to AD-12
func BadRequest(detail string) ProblemDetails {
	return NewProblem(http.StatusBadRequest, "MALFORMED_REQUEST", "Bad Request", detail)
}

func Unauthorized(detail string) ProblemDetails {
	return NewProblem(http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthorized", detail)
}

func Forbidden(detail string) ProblemDetails {
	return NewProblem(http.StatusForbidden, "FORBIDDEN", "Forbidden", detail)
}

func NotFound(resource string) ProblemDetails {
	return NewProblem(http.StatusNotFound, "NOT_FOUND", "Resource Not Found", resource+" was not found")
}

func Conflict(code, detail string) ProblemDetails {
	return NewProblem(http.StatusConflict, code, "Conflict", detail)
}

func UnprocessableEntity(detail string, errs []ValidationError) ProblemDetails {
	p := NewProblem(http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Validation Failed", detail)
	p.Errors = errs
	return p
}

func InternalServerError(detail string) ProblemDetails {
	return NewProblem(http.StatusInternalServerError, "INTERNAL_ERROR", "Internal Server Error", detail)
}

func ServiceUnavailable(detail string) ProblemDetails {
	return NewProblem(http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "Service Unavailable", detail)
}
