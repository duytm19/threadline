package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/threadline/pkg/httpx"
	"github.com/threadline/pkg/tenant"
)

func TestMiddlewareTenantContext(t *testing.T) {
	t.Run("required_missing_header", func(t *testing.T) {
		handler := httpx.MiddlewareTenantContext(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", rec.Code)
		}
	})

	t.Run("required_invalid_header", func(t *testing.T) {
		handler := httpx.MiddlewareTenantContext(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
		req.Header.Set(httpx.HeaderTenantID, "invalid/tenant")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", rec.Code)
		}
	})

	t.Run("required_valid_header", func(t *testing.T) {
		var capturedTenant string
		handler := httpx.MiddlewareTenantContext(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedTenant = httpx.MustTenantFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
		req.Header.Set(httpx.HeaderTenantID, "zalora-vn")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if capturedTenant != "zalora-vn" {
			t.Errorf("expected zalora-vn, got %s", capturedTenant)
		}
	})

	t.Run("optional_missing_header", func(t *testing.T) {
		var tenantExtracted bool
		handler := httpx.MiddlewareTenantContext(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, tenantExtracted = httpx.TenantFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if tenantExtracted {
			t.Errorf("expected no tenant in context")
		}
	})

	t.Run("extract_using_pkg_tenant", func(t *testing.T) {
		var extractedViaTenantPkg string
		handler := httpx.MiddlewareTenantContext(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tc, err := tenant.FromContext(r.Context())
			if err != nil {
				t.Fatalf("failed to extract via tenant.FromContext: %v", err)
			}
			extractedViaTenantPkg = tc.TenantID()
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
		req.Header.Set(httpx.HeaderTenantID, "iconic-au")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if extractedViaTenantPkg != "iconic-au" {
			t.Errorf("expected iconic-au, got %s", extractedViaTenantPkg)
		}
	})
}
