package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/threadline/pkg/httpx"
	"github.com/threadline/pkg/tenant"
	"github.com/threadline/services/identity/internal/application"
	"github.com/threadline/services/identity/internal/application/ports"
	"github.com/threadline/services/identity/internal/domain/tokenfamily"
)

const (
	// DefaultRefreshCookieName specifies the standard cookie identifier for refresh tokens.
	DefaultRefreshCookieName = "refresh_token"

	// DefaultRefreshCookiePath scopes the cookie strictly to authentication endpoints.
	DefaultRefreshCookiePath = "/v1/auth"

	// DefaultRefreshCookieTTL configures the 30-day sliding refresh cookie lifetime.
	DefaultRefreshCookieTTL = 30 * 24 * time.Hour
)

// RefreshTokenExecutor defines the application contract required by Handler.
type RefreshTokenExecutor interface {
	Execute(ctx context.Context, cmd application.RefreshTokenCommand) (*application.RefreshTokenResult, error)
}

// CookieConfig configures cookie properties for refresh token transmission.
type CookieConfig struct {
	Name     string
	Path     string
	Domain   string
	Secure   bool
	SameSite http.SameSite
	TTL      time.Duration
}

// DefaultCookieConfig produces a production-hardened cookie configuration.
func DefaultCookieConfig() CookieConfig {
	return CookieConfig{
		Name:     DefaultRefreshCookieName,
		Path:     DefaultRefreshCookiePath,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		TTL:      DefaultRefreshCookieTTL,
	}
}

// HandlerOption configures optional behavior on Handler.
type HandlerOption func(*Handler)

// WithCookieConfig overrides default cookie properties.
func WithCookieConfig(cfg CookieConfig) HandlerOption {
	return func(h *Handler) {
		h.cookieCfg = cfg
	}
}

// WithInsecureCookies disables the Secure cookie flag for non-TLS local testing.
func WithInsecureCookies(insecure bool) HandlerOption {
	return func(h *Handler) {
		h.cookieCfg.Secure = !insecure
	}
}

// Handler exposes HTTP entrypoints for the identity service.
type Handler struct {
	useCase   RefreshTokenExecutor
	cookieCfg CookieConfig
}

// NewHandler constructs an initialized HTTP Handler.
func NewHandler(useCase RefreshTokenExecutor, opts ...HandlerOption) *Handler {
	h := &Handler{
		useCase:   useCase,
		cookieCfg: DefaultCookieConfig(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	return h
}

// RegisterRoutes registers endpoints on a standard http.ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/refresh", h.Refresh)
}

// Routes returns an http.Handler with all identity endpoints mounted.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

// refreshRequest carries an optional JSON refresh token body for non-browser clients.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token,omitempty"`
}

// refreshResponse carries the newly minted JWT access token and expiration.
type refreshResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// Refresh handles POST /v1/auth/refresh requests.
// Rotates the presented refresh token and returns a short-lived access token with a rotated cookie.
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteProblem(w, r, httpx.NewProblem(
			http.StatusMethodNotAllowed,
			"METHOD_NOT_ALLOWED",
			"Method Not Allowed",
			fmt.Sprintf("HTTP method %s is not supported for this endpoint", r.Method),
		))
		return
	}

	// Resolve multi-tenant context (fail-closed).
	tenantID, err := h.resolveTenant(r)
	if err != nil {
		httpx.WriteProblem(w, r, httpx.BadRequest(err.Error()))
		return
	}

	// 2. Extract presented refresh token (cookie takes precedence, then JSON body).
	rawToken := h.extractRefreshToken(r)
	if rawToken == "" {
		httpx.WriteProblem(w, r, httpx.BadRequest("refresh token is required in cookie or request body"))
		return
	}

	// 3. Extract client IP, User-Agent, request ID, and edge geo hints.
	clientIP := h.extractClientIP(r)
	userAgent := r.UserAgent()
	geoHint := h.extractGeoHint(r)
	requestID := r.Header.Get(httpx.HeaderRequestID)

	cmd := application.RefreshTokenCommand{
		TenantID:     tenantID,
		RefreshToken: rawToken,
		ClientIP:     clientIP,
		UserAgent:    userAgent,
		GeoHint:      geoHint,
		GeoHash:      r.Header.Get("X-Geo-Hash"),
		RequestID:    requestID,
	}

	// 4. Execute atomic token rotation usecase.
	res, err := h.useCase.Execute(r.Context(), cmd)
	if err != nil {
		h.handleError(w, r, err)
		return
	}

	// 5. Success: Set rotated refresh token in SameSite=Strict; HttpOnly; Secure cookie.
	h.setRefreshCookie(w, res.RefreshToken, h.cookieCfg.TTL)

	// 6. Write JSON response with short-lived JWT access token.
	expiresIn := int64(time.Until(res.AccessTokenExpiresAt).Seconds())
	if expiresIn <= 0 {
		expiresIn = int64(application.DefaultAccessTokenTTL.Seconds())
	}

	resp := refreshResponse{
		AccessToken: res.AccessToken,
		TokenType:   res.TokenType,
		ExpiresIn:   expiresIn,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) resolveTenant(r *http.Request) (string, error) {
	if tid, err := tenant.TenantIDFromContext(r.Context()); err == nil && tid != "" {
		return tid, nil
	}
	raw := strings.TrimSpace(r.Header.Get(tenant.HeaderTenantID))
	if raw == "" {
		return "", errors.New("X-Tenant-ID header is required (fail-closed)")
	}
	tc, err := tenant.New(raw)
	if err != nil {
		return "", fmt.Errorf("invalid X-Tenant-ID: %w", err)
	}
	return tc.TenantID(), nil
}

func (h *Handler) extractRefreshToken(r *http.Request) string {
	// First check cookie
	if cookie, err := r.Cookie(h.cookieCfg.Name); err == nil {
		if val := strings.TrimSpace(cookie.Value); val != "" {
			return val
		}
	}

	// Fallback to JSON request body for non-browser clients
	if r.Body != nil && r.ContentLength > 0 {
		var req refreshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			return strings.TrimSpace(req.RefreshToken)
		}
	}

	return ""
}

func (h *Handler) extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (h *Handler) extractGeoHint(r *http.Request) ports.GeoHint {
	var hint ports.GeoHint
	hint.Country = r.Header.Get("CF-IPCountry")
	if hint.Country == "" {
		hint.Country = r.Header.Get("X-Country-Code")
	}
	hint.Region = r.Header.Get("X-Geo-Region")
	hint.City = r.Header.Get("X-Geo-City")
	hint.RawGeo = r.Header.Get("X-Edge-Geo")

	if latStr := r.Header.Get("X-Geo-Lat"); latStr != "" {
		if lat, err := strconv.ParseFloat(latStr, 64); err == nil {
			hint.Latitude = &lat
		}
	}
	if lonStr := r.Header.Get("X-Geo-Lon"); lonStr != "" {
		if lon, err := strconv.ParseFloat(lonStr, 64); err == nil {
			hint.Longitude = &lon
		}
	}
	return hint
}

func (h *Handler) setRefreshCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	cookie := &http.Cookie{
		Name:     h.cookieCfg.Name,
		Value:    token,
		Path:     h.cookieCfg.Path,
		Domain:   h.cookieCfg.Domain,
		MaxAge:   int(ttl.Seconds()),
		Expires:  time.Now().Add(ttl),
		HttpOnly: true,
		Secure:   h.cookieCfg.Secure,
		SameSite: h.cookieCfg.SameSite,
	}
	http.SetCookie(w, cookie)
}

func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	cookie := &http.Cookie{
		Name:     h.cookieCfg.Name,
		Value:    "",
		Path:     h.cookieCfg.Path,
		Domain:   h.cookieCfg.Domain,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   h.cookieCfg.Secure,
		SameSite: h.cookieCfg.SameSite,
	}
	http.SetCookie(w, cookie)
}

func (h *Handler) handleError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tokenfamily.ErrTokenReused):
		h.clearRefreshCookie(w)
		httpx.WriteProblem(w, r, httpx.NewProblem(
			http.StatusUnauthorized,
			"A07_TOKEN_REUSE",
			"Unauthorized",
			"Active token reuse detected; entire token family has been revoked",
		))

	case errors.Is(err, tokenfamily.ErrFamilyCompromised):
		h.clearRefreshCookie(w)
		httpx.WriteProblem(w, r, httpx.NewProblem(
			http.StatusUnauthorized,
			"A07_TOKEN_REUSE",
			"Unauthorized",
			"Token family is compromised; please re-authenticate",
		))

	case errors.Is(err, tokenfamily.ErrTokenExpired), errors.Is(err, tokenfamily.ErrFamilyRevoked):
		h.clearRefreshCookie(w)
		httpx.WriteProblem(w, r, httpx.NewProblem(
			http.StatusUnauthorized,
			"TOKEN_EXPIRED",
			"Unauthorized",
			"Refresh token has expired or was revoked",
		))

	case errors.Is(err, tokenfamily.ErrTokenNotFound):
		h.clearRefreshCookie(w)
		httpx.WriteProblem(w, r, httpx.NewProblem(
			http.StatusUnauthorized,
			"TOKEN_NOT_FOUND",
			"Unauthorized",
			"Presented refresh token was not found",
		))

	case errors.Is(err, application.ErrCrossTenantMismatch), errors.Is(err, ports.ErrCrossTenantViolation):
		httpx.WriteProblem(w, r, httpx.NewProblem(
			http.StatusForbidden,
			"CROSS_TENANT_VIOLATION",
			"Forbidden",
			"Cross-tenant access violation detected",
		))

	case errors.Is(err, application.ErrUserInactive):
		h.clearRefreshCookie(w)
		httpx.WriteProblem(w, r, httpx.NewProblem(
			http.StatusForbidden,
			"USER_INACTIVE",
			"Forbidden",
			"User account is inactive or suspended",
		))

	case errors.Is(err, application.ErrMissingRefreshToken):
		httpx.WriteProblem(w, r, httpx.BadRequest("refresh token is required"))

	default:
		httpx.WriteProblem(w, r, httpx.InternalServerError("failed to rotate refresh token"))
	}
}
