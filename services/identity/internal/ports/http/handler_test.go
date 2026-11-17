package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/threadline/pkg/httpx"
	"github.com/threadline/services/identity/internal/application"
	identityhttp "github.com/threadline/services/identity/internal/ports/http"
	"github.com/threadline/services/identity/internal/domain/tokenfamily"
)

type mockExecutor struct {
	capturedCmd application.RefreshTokenCommand
	res         *application.RefreshTokenResult
	err         error
}

func (m *mockExecutor) Execute(_ context.Context, cmd application.RefreshTokenCommand) (*application.RefreshTokenResult, error) {
	m.capturedCmd = cmd
	return m.res, m.err
}

func TestHandler_HappyPathRotation(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	mock := &mockExecutor{
		res: &application.RefreshTokenResult{
			AccessToken:           "jwt-access-token-15m",
			AccessTokenJTI:        "jti-acc-1",
			AccessTokenExpiresAt:  now.Add(15 * time.Minute),
			TokenType:             "Bearer",
			RefreshToken:          "rt-new-token-generation-2",
			RefreshTokenJTI:       "jti-rt-2",
			RefreshTokenExpiresAt: now.Add(30 * 24 * time.Hour),
			FamilyID:              "fam-123",
			TenantID:              "zalora-vn",
			UserID:                "usr-999",
			Generation:            2,
		},
	}

	handler := identityhttp.NewHandler(mock)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", nil)
	req.Header.Set("X-Tenant-ID", "zalora-vn")
	req.Header.Set("User-Agent", "ThreadlineMobile/1.0 (iOS)")
	req.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18")
	req.AddCookie(&http.Cookie{
		Name:  identityhttp.DefaultRefreshCookieName,
		Value: "rt-valid-presented-token-1",
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// 1. Assert Status 200 OK
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d (body: %s)", rec.Code, rec.Body.String())
	}

	// 2. Assert Set-Cookie attributes: SameSite=Strict; HttpOnly; Secure
	cookies := rec.Result().Cookies()
	var refreshCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == identityhttp.DefaultRefreshCookieName {
			refreshCookie = c
			break
		}
	}
	if refreshCookie == nil {
		t.Fatal("expected Set-Cookie with refresh_token, but none was found")
	}

	if refreshCookie.Value != "rt-new-token-generation-2" {
		t.Errorf("expected rotated refresh token 'rt-new-token-generation-2', got %q", refreshCookie.Value)
	}
	if !refreshCookie.HttpOnly {
		t.Errorf("expected HttpOnly=true, got %v", refreshCookie.HttpOnly)
	}
	if !refreshCookie.Secure {
		t.Errorf("expected Secure=true, got %v", refreshCookie.Secure)
	}
	if refreshCookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("expected SameSite=Strict (%v), got %v", http.SameSiteStrictMode, refreshCookie.SameSite)
	}
	if refreshCookie.Path != identityhttp.DefaultRefreshCookiePath {
		t.Errorf("expected Path=%s, got %s", identityhttp.DefaultRefreshCookiePath, refreshCookie.Path)
	}
	if refreshCookie.MaxAge <= 0 {
		t.Errorf("expected positive MaxAge, got %d", refreshCookie.MaxAge)
	}

	// Raw header string check for exact directive formatting
	setCookieRaw := rec.Header().Get("Set-Cookie")
	if !strings.Contains(setCookieRaw, "SameSite=Strict") {
		t.Errorf("raw Set-Cookie missing 'SameSite=Strict': %s", setCookieRaw)
	}
	if !strings.Contains(setCookieRaw, "HttpOnly") {
		t.Errorf("raw Set-Cookie missing 'HttpOnly': %s", setCookieRaw)
	}
	if !strings.Contains(setCookieRaw, "Secure") {
		t.Errorf("raw Set-Cookie missing 'Secure': %s", setCookieRaw)
	}

	// 3. Assert Response Body JSON
	var resp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("expected valid JSON body: %v", err)
	}
	if resp.AccessToken != "jwt-access-token-15m" {
		t.Errorf("unexpected access_token: %q", resp.AccessToken)
	}
	if resp.TokenType != "Bearer" {
		t.Errorf("unexpected token_type: %q", resp.TokenType)
	}
	if resp.ExpiresIn <= 0 {
		t.Errorf("expected positive expires_in, got %d", resp.ExpiresIn)
	}

	// 4. Assert Command Forwarding
	if mock.capturedCmd.TenantID != "zalora-vn" {
		t.Errorf("expected tenant 'zalora-vn', got %q", mock.capturedCmd.TenantID)
	}
	if mock.capturedCmd.RefreshToken != "rt-valid-presented-token-1" {
		t.Errorf("expected token 'rt-valid-presented-token-1', got %q", mock.capturedCmd.RefreshToken)
	}
	if mock.capturedCmd.ClientIP != "203.0.113.195, 70.41.3.18" {
		t.Errorf("expected ClientIP '203.0.113.195, 70.41.3.18', got %q", mock.capturedCmd.ClientIP)
	}
	if mock.capturedCmd.UserAgent != "ThreadlineMobile/1.0 (iOS)" {
		t.Errorf("unexpected UserAgent: %q", mock.capturedCmd.UserAgent)
	}
}

func TestHandler_ReuseDetectionReplayAttack(t *testing.T) {
	mock := &mockExecutor{
		err: tokenfamily.ErrTokenReused,
	}

	handler := identityhttp.NewHandler(mock)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", nil)
	req.Header.Set("X-Tenant-ID", "zalora-vn")
	req.AddCookie(&http.Cookie{
		Name:  identityhttp.DefaultRefreshCookieName,
		Value: "rt-already-used-replay-attempt",
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Scenario 2: Returns 401 Unauthorized
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got: %d", rec.Code)
	}

	// RFC 7807 Content-Type
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, httpx.ContentTypeProblemJSON) {
		t.Errorf("expected Content-Type '%s', got '%s'", httpx.ContentTypeProblemJSON, ct)
	}

	// RFC 7807 Payload with A07_TOKEN_REUSE
	var problem httpx.ProblemDetails
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("failed to parse problem JSON: %v", err)
	}
	if problem.Status != http.StatusUnauthorized {
		t.Errorf("expected Problem Status 401, got %d", problem.Status)
	}
	if problem.Code != "A07_TOKEN_REUSE" {
		t.Errorf("expected Problem Code 'A07_TOKEN_REUSE', got %q", problem.Code)
	}

	// Cookie must be cleared/invalidated
	cookies := rec.Result().Cookies()
	var clearedCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == identityhttp.DefaultRefreshCookieName {
			clearedCookie = c
			break
		}
	}
	if clearedCookie == nil {
		t.Fatal("expected clearing Set-Cookie, none found")
	}
	if clearedCookie.MaxAge != -1 || clearedCookie.Value != "" {
		t.Errorf("expected cleared cookie (MaxAge=-1, Value=''), got MaxAge=%d Value=%q", clearedCookie.MaxAge, clearedCookie.Value)
	}
}

func TestHandler_MissingTenantFailClosed(t *testing.T) {
	mock := &mockExecutor{}
	handler := identityhttp.NewHandler(mock)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", nil)
	req.AddCookie(&http.Cookie{
		Name:  identityhttp.DefaultRefreshCookieName,
		Value: "rt-some-token",
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request on missing tenant, got: %d", rec.Code)
	}

	var problem httpx.ProblemDetails
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("failed to parse problem JSON: %v", err)
	}
	if problem.Code != "MALFORMED_REQUEST" {
		t.Errorf("expected code MALFORMED_REQUEST, got %q", problem.Code)
	}
}

func TestHandler_MissingRefreshToken(t *testing.T) {
	mock := &mockExecutor{}
	handler := identityhttp.NewHandler(mock)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", nil)
	req.Header.Set("X-Tenant-ID", "zalora-vn")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request on missing token, got: %d", rec.Code)
	}
}

func TestHandler_JSONBodyFallback(t *testing.T) {
	mock := &mockExecutor{
		res: &application.RefreshTokenResult{
			AccessToken:           "jwt-body-token",
			AccessTokenExpiresAt:  time.Now().Add(15 * time.Minute),
			TokenType:             "Bearer",
			RefreshToken:          "rt-body-new",
			RefreshTokenExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		},
	}
	handler := identityhttp.NewHandler(mock)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body := bytes.NewBufferString(`{"refresh_token": "rt-from-json-body"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", body)
	req.Header.Set("X-Tenant-ID", "zalora-vn")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from JSON body, got: %d (body: %s)", rec.Code, rec.Body.String())
	}
	if mock.capturedCmd.RefreshToken != "rt-from-json-body" {
		t.Errorf("expected RefreshToken 'rt-from-json-body', got %q", mock.capturedCmd.RefreshToken)
	}
}

func TestHandler_ErrorDispositions(t *testing.T) {
	tests := []struct {
		name           string
		appErr         error
		expectedStatus int
		expectedCode   string
		expectClear    bool
	}{
		{
			name:           "family compromised",
			appErr:         tokenfamily.ErrFamilyCompromised,
			expectedStatus: http.StatusUnauthorized,
			expectedCode:   "A07_TOKEN_REUSE",
			expectClear:    true,
		},
		{
			name:           "token expired",
			appErr:         tokenfamily.ErrTokenExpired,
			expectedStatus: http.StatusUnauthorized,
			expectedCode:   "TOKEN_EXPIRED",
			expectClear:    true,
		},
		{
			name:           "token not found",
			appErr:         tokenfamily.ErrTokenNotFound,
			expectedStatus: http.StatusUnauthorized,
			expectedCode:   "TOKEN_NOT_FOUND",
			expectClear:    true,
		},
		{
			name:           "user inactive",
			appErr:         application.ErrUserInactive,
			expectedStatus: http.StatusForbidden,
			expectedCode:   "USER_INACTIVE",
			expectClear:    true,
		},
		{
			name:           "cross tenant mismatch",
			appErr:         application.ErrCrossTenantMismatch,
			expectedStatus: http.StatusForbidden,
			expectedCode:   "CROSS_TENANT_VIOLATION",
			expectClear:    false,
		},
		{
			name:           "internal storage error",
			appErr:         errors.New("redis connection timeout"),
			expectedStatus: http.StatusInternalServerError,
			expectedCode:   "INTERNAL_ERROR",
			expectClear:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockExecutor{err: tc.appErr}
			handler := identityhttp.NewHandler(mock)
			mux := http.NewServeMux()
			handler.RegisterRoutes(mux)

			req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", nil)
			req.Header.Set("X-Tenant-ID", "zalora-vn")
			req.AddCookie(&http.Cookie{
				Name:  identityhttp.DefaultRefreshCookieName,
				Value: "rt-sample-token",
			})

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tc.expectedStatus {
				t.Errorf("expected status %d, got %d", tc.expectedStatus, rec.Code)
			}

			var problem httpx.ProblemDetails
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatalf("failed to decode problem JSON: %v", err)
			}
			if problem.Code != tc.expectedCode {
				t.Errorf("expected code %q, got %q", tc.expectedCode, problem.Code)
			}

			if tc.expectClear {
				cookies := rec.Result().Cookies()
				var cleared *http.Cookie
				for _, c := range cookies {
					if c.Name == identityhttp.DefaultRefreshCookieName {
						cleared = c
						break
					}
				}
				if cleared == nil || cleared.MaxAge != -1 {
					t.Errorf("expected cookie to be cleared (MaxAge=-1)")
				}
			}
		})
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	handler := identityhttp.NewHandler(&mockExecutor{})
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/refresh", nil)
	rec := httptest.NewRecorder()

	handler.Refresh(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 Method Not Allowed, got: %d", rec.Code)
	}
}

func TestHandler_GeoHintsAndOptions(t *testing.T) {
	mock := &mockExecutor{
		res: &application.RefreshTokenResult{
			AccessToken:          "jwt-access",
			AccessTokenExpiresAt: time.Now().Add(15 * time.Minute),
			RefreshToken:         "rt-geo-new",
		},
	}

	handler := identityhttp.NewHandler(
		mock,
		identityhttp.WithInsecureCookies(true),
		identityhttp.WithCookieConfig(identityhttp.CookieConfig{
			Name:     "custom_rt",
			Path:     "/api/auth",
			Domain:   "example.com",
			Secure:   false,
			SameSite: http.SameSiteStrictMode,
			TTL:      14 * 24 * time.Hour,
		}),
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", nil)
	req.Header.Set("X-Tenant-ID", "zalora-vn")
	req.Header.Set("CF-IPCountry", "VN")
	req.Header.Set("X-Geo-Region", "SG")
	req.Header.Set("X-Geo-City", "Ho Chi Minh City")
	req.Header.Set("X-Geo-Lat", "10.7769")
	req.Header.Set("X-Geo-Lon", "106.7009")
	req.Header.Set("X-Geo-Hash", "w4g8q")
	req.Header.Set(httpx.HeaderRequestID, "req-xyz-789")
	req.AddCookie(&http.Cookie{
		Name:  "custom_rt",
		Value: "rt-custom-input",
	})

	rec := httptest.NewRecorder()
	handler.Refresh(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	if mock.capturedCmd.GeoHint.Country != "VN" {
		t.Errorf("expected Country 'VN', got %q", mock.capturedCmd.GeoHint.Country)
	}
	if mock.capturedCmd.GeoHint.City != "Ho Chi Minh City" {
		t.Errorf("expected City 'Ho Chi Minh City', got %q", mock.capturedCmd.GeoHint.City)
	}
	if mock.capturedCmd.GeoHint.Latitude == nil || *mock.capturedCmd.GeoHint.Latitude != 10.7769 {
		t.Errorf("expected Latitude 10.7769, got %v", mock.capturedCmd.GeoHint.Latitude)
	}
	if mock.capturedCmd.GeoHash != "w4g8q" {
		t.Errorf("expected GeoHash 'w4g8q', got %q", mock.capturedCmd.GeoHash)
	}
	if mock.capturedCmd.RequestID != "req-xyz-789" {
		t.Errorf("expected RequestID 'req-xyz-789', got %q", mock.capturedCmd.RequestID)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "custom_rt" {
		t.Fatalf("expected cookie 'custom_rt', got %+v", cookies)
	}
	if cookies[0].Secure {
		t.Errorf("expected Secure=false with WithInsecureCookies, got true")
	}
}
