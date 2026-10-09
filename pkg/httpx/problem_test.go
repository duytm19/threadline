package httpx_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/threadline/pkg/httpx"
)

func TestWriteProblem(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)

	prob := httpx.NotFound("Order ORD-123")
	httpx.WriteProblem(rec, req, prob)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if contentType != httpx.ContentTypeProblemJSON {
		t.Fatalf("expected content type %s, got %s", httpx.ContentTypeProblemJSON, contentType)
	}

	var parsed httpx.ProblemDetails
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("failed to parse problem json: %v", err)
	}

	if parsed.Code != "NOT_FOUND" {
		t.Errorf("expected code NOT_FOUND, got %s", parsed.Code)
	}
	if parsed.Instance != "/v1/test" {
		t.Errorf("expected instance /v1/test, got %s", parsed.Instance)
	}
}
