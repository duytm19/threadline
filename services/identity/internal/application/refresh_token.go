package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/threadline/pkg/observability"
	"github.com/threadline/pkg/tenant"
	"github.com/threadline/services/identity/internal/application/ports"
	"github.com/threadline/services/identity/internal/domain/tokenfamily"
)

const (
	// DefaultAccessTokenTTL defines the short-lived 15-minute access token lifetime.
	DefaultAccessTokenTTL = 15 * time.Minute

	// DefaultRefreshTokenTTL defines the 30-day sliding family/refresh token lifetime.
	DefaultRefreshTokenTTL = 30 * 24 * time.Hour

	// DefaultTokenIssuer identifies the Threadline identity service issuer claim.
	DefaultTokenIssuer = "https://auth.threadline.dev"

	// DefaultTokenAudience identifies the Threadline API audience claim.
	DefaultTokenAudience = "threadline-api"

	alertCodeCrossTenantMismatch  = "AD4_CROSS_TENANT_MISMATCH"
	alertCodeCrossTenantViolation = "AD4_CROSS_TENANT_VIOLATION"
)

var (
	// ErrNilTokenStore is returned when RefreshTokenUseCase is constructed without a TokenStore.
	ErrNilTokenStore = errors.New("application: token store must not be nil")

	// ErrNilAccessTokenIssuer is returned when RefreshTokenUseCase is constructed without an AccessTokenIssuer.
	ErrNilAccessTokenIssuer = errors.New("application: access token issuer must not be nil")

	// ErrInvalidTokenTTL is returned when configured access or refresh token TTL is non-positive.
	ErrInvalidTokenTTL = errors.New("application: token TTL must be strictly positive")

	// ErrMissingRefreshToken is returned when the presented refresh token string is empty.
	ErrMissingRefreshToken = errors.New("application: refresh token must not be empty")

	// ErrCrossTenantMismatch is returned when context tenant and command tenant disagree (fail-closed per AD-4).
	ErrCrossTenantMismatch = errors.New("application: tenant context does not match command tenant")

	// ErrUserInactive is returned when the token owner's account is disabled or locked.
	ErrUserInactive = errors.New("application: user account is inactive")
)

// RefreshTokenCommand carries the inputs and client audit metadata for a token refresh request.
type RefreshTokenCommand struct {
	TenantID     string
	RefreshToken string
	ClientIP     string
	UserAgent    string
	GeoHash      string
	GeoHint      ports.GeoHint
	RequestID    string
}

// RefreshTokenResult holds the newly minted JWT access token and rotated opaque refresh token.
type RefreshTokenResult struct {
	AccessToken           string
	AccessTokenJTI        string
	AccessTokenExpiresAt  time.Time
	TokenType             string
	RefreshToken          string
	RefreshTokenJTI       string
	RefreshTokenExpiresAt time.Time
	FamilyID              string
	TenantID              string
	UserID                string
	Generation            int
}

// RefreshOption configures optional dependencies and policies for RefreshTokenUseCase.
type RefreshOption func(*RefreshTokenUseCase)

// WithClock overrides the clock used for timestamps and expirations.
func WithClock(clock ports.Clock) RefreshOption {
	return func(uc *RefreshTokenUseCase) {
		if clock != nil {
			uc.clock = clock
		}
	}
}

// WithTokenGenerator overrides the generator for new refresh tokens and JTIs.
func WithTokenGenerator(gen ports.TokenGenerator) RefreshOption {
	return func(uc *RefreshTokenUseCase) {
		if gen != nil {
			uc.tokenGen = gen
		}
	}
}

// WithSubjectClaimsProvider injects a provider to load fresh user roles, scopes, and account status.
func WithSubjectClaimsProvider(provider ports.SubjectClaimsProvider) RefreshOption {
	return func(uc *RefreshTokenUseCase) {
		uc.claimsProvider = provider
	}
}

// WithGeoResolver overrides the geographic hash resolver.
func WithGeoResolver(resolver ports.GeoResolver) RefreshOption {
	return func(uc *RefreshTokenUseCase) {
		if resolver != nil {
			uc.geoResolver = resolver
		}
	}
}

// WithLogger injects the structured JSON logger.
func WithLogger(logger *slog.Logger) RefreshOption {
	return func(uc *RefreshTokenUseCase) {
		if logger != nil {
			uc.logger = logger
		}
	}
}

// WithAccessTokenTTL configures the access token lifetime.
func WithAccessTokenTTL(ttl time.Duration) RefreshOption {
	return func(uc *RefreshTokenUseCase) {
		uc.accessTokenTTL = ttl
	}
}

// WithRefreshTokenTTL configures the refresh token lifetime.
func WithRefreshTokenTTL(ttl time.Duration) RefreshOption {
	return func(uc *RefreshTokenUseCase) {
		uc.refreshTokenTTL = ttl
	}
}

// WithTokenIssuer configures the JWT iss claim.
func WithTokenIssuer(issuer string) RefreshOption {
	return func(uc *RefreshTokenUseCase) {
		if strings.TrimSpace(issuer) != "" {
			uc.issuer = strings.TrimSpace(issuer)
		}
	}
}

// WithTokenAudience configures the JWT aud claim.
func WithTokenAudience(audience ...string) RefreshOption {
	return func(uc *RefreshTokenUseCase) {
		if len(audience) > 0 {
			copied := make([]string, len(audience))
			copy(copied, audience)
			uc.audience = copied
		}
	}
}

// RefreshTokenUseCase orchestrates atomic refresh token rotation, active reuse detection,
// short-lived JWT issuance, and immutable security audit logging.
type RefreshTokenUseCase struct {
	tokenStore      ports.TokenStore
	accessIssuer    ports.AccessTokenIssuer
	auditLogger     ports.AuditLogger
	claimsProvider  ports.SubjectClaimsProvider
	tokenGen        ports.TokenGenerator
	geoResolver     ports.GeoResolver
	clock           ports.Clock
	logger          *slog.Logger
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
	issuer          string
	audience        []string
}

// NewRefreshTokenUseCase constructs a validated RefreshTokenUseCase with production defaults.
func NewRefreshTokenUseCase(
	tokenStore ports.TokenStore,
	accessIssuer ports.AccessTokenIssuer,
	auditLogger ports.AuditLogger,
	opts ...RefreshOption,
) (*RefreshTokenUseCase, error) {
	if tokenStore == nil {
		return nil, ErrNilTokenStore
	}
	if accessIssuer == nil {
		return nil, ErrNilAccessTokenIssuer
	}

	uc := &RefreshTokenUseCase{
		tokenStore:      tokenStore,
		accessIssuer:    accessIssuer,
		auditLogger:     auditLogger,
		tokenGen:        NewCryptoTokenGenerator(),
		geoResolver:     NewDeterministicGeoHasher(""),
		clock:           SystemClock{},
		logger:          slog.Default(),
		accessTokenTTL:  DefaultAccessTokenTTL,
		refreshTokenTTL: DefaultRefreshTokenTTL,
		issuer:          DefaultTokenIssuer,
		audience:        []string{DefaultTokenAudience},
	}

	for _, opt := range opts {
		if opt != nil {
			opt(uc)
		}
	}

	if uc.accessTokenTTL <= 0 || uc.refreshTokenTTL <= 0 {
		return nil, ErrInvalidTokenTTL
	}

	return uc, nil
}

// Execute rotates an active refresh token for a new refresh token and 15-minute JWT access token,
// emitting structured audit logs (including client IP, User-Agent, and geographic hash) for every outcome.
func (uc *RefreshTokenUseCase) Execute(ctx context.Context, cmd RefreshTokenCommand) (*RefreshTokenResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	now := uc.clock.Now().UTC()
	clientIP := NormalizeClientIP(cmd.ClientIP)
	userAgent := SanitizeUserAgent(cmd.UserAgent)
	geoHash := uc.resolveGeoHash(ctx, clientIP, cmd.GeoHash, cmd.GeoHint)
	traceID := observability.TraceIDFromContext(ctx)

	baseEvent := ports.AuditEvent{
		EventID:    newAuditEventID(),
		ClientIP:   clientIP,
		UserAgent:  userAgent,
		GeoHash:    geoHash,
		TraceID:    traceID,
		RequestID:  cmd.RequestID,
		OccurredAt: now,
	}

	// Enforce fail-closed multi-tenant scope per AD-4.
	tenantID, scopedCtx, tenantErr := uc.resolveTenant(ctx, cmd.TenantID)
	if tenantErr != nil {
		ev := baseEvent
		ev.TenantID = strings.TrimSpace(cmd.TenantID)
		ev.EventType = "auth.refresh.tenant_rejected"
		ev.Outcome = ports.OutcomeDenied
		ev.Severity = ports.SeverityWarn
		ev.Reason = "missing_or_invalid_tenant"

		if errors.Is(tenantErr, ErrCrossTenantMismatch) {
			ev.Severity = ports.SeverityCritical
			ev.AlertCode = alertCodeCrossTenantMismatch
			ev.Reason = "cross_tenant_context_mismatch"
		}

		uc.recordAudit(ctx, ev, "refresh token rejected: tenant validation failed")
		return nil, tenantErr
	}
	baseEvent.TenantID = tenantID

	rawRefreshToken := strings.TrimSpace(cmd.RefreshToken)
	if rawRefreshToken == "" {
		ev := baseEvent
		ev.EventType = "auth.refresh.missing_token"
		ev.Outcome = ports.OutcomeDenied
		ev.Severity = ports.SeverityWarn
		ev.Reason = "missing_refresh_token"
		uc.recordAudit(scopedCtx, ev, "refresh token rejected: missing refresh token")
		return nil, ErrMissingRefreshToken
	}

	presentedHash := tokenfamily.HashToken(rawRefreshToken)

	newRawRefreshToken, newRefreshHash, err := uc.tokenGen.GenerateRefreshToken()
	if err != nil {
		ev := baseEvent
		ev.EventType = "auth.refresh.token_generation_failed"
		ev.Outcome = ports.OutcomeFailed
		ev.Severity = ports.SeverityCritical
		ev.Reason = "refresh_token_generation_error"
		uc.recordAudit(scopedCtx, ev, "refresh token generation failed")
		return nil, err
	}

	newRefreshJTI, err := uc.tokenGen.GenerateJTI()
	if err != nil {
		ev := baseEvent
		ev.EventType = "auth.refresh.jti_generation_failed"
		ev.Outcome = ports.OutcomeFailed
		ev.Severity = ports.SeverityCritical
		ev.Reason = "jti_generation_error"
		uc.recordAudit(scopedCtx, ev, "refresh token jti generation failed")
		return nil, err
	}

	newRefreshExpiresAt := now.Add(uc.refreshTokenTTL)

	// Atomically exchange presented refresh token in Redis (handles active reuse detection).
	exchangeRes, err := uc.tokenStore.ExchangeToken(
		scopedCtx,
		tenantID,
		presentedHash,
		newRefreshHash,
		newRefreshJTI,
		newRefreshExpiresAt,
		now,
		uc.refreshTokenTTL,
	)
	if err != nil {
		uc.auditExchangeError(scopedCtx, baseEvent, err)
		return nil, err
	}
	if exchangeRes == nil {
		ev := baseEvent
		ev.EventType = "auth.refresh.failed"
		ev.Outcome = ports.OutcomeFailed
		ev.Severity = ports.SeverityCritical
		ev.Reason = "empty_exchange_result"
		uc.recordAudit(scopedCtx, ev, "refresh token exchange failed: empty exchange result")
		return nil, errors.New("application: empty exchange result from token store")
	}
	if exchangeRes.TenantID != "" && exchangeRes.TenantID != tenantID {
		ev := baseEvent
		ev.FamilyID = exchangeRes.FamilyID
		ev.UserID = exchangeRes.UserID
		ev.OldTokenJTI = exchangeRes.OldTokenJTI
		ev.NewTokenJTI = exchangeRes.NewTokenJTI
		ev.Generation = exchangeRes.Generation
		ev.EventType = "auth.refresh.cross_tenant_violation"
		ev.Outcome = ports.OutcomeDenied
		ev.Severity = ports.SeverityCritical
		ev.AlertCode = alertCodeCrossTenantViolation
		ev.Reason = "cross_tenant_exchange_result"
		uc.recordAudit(scopedCtx, ev, "security alert: exchange result tenant mismatch")
		return nil, ErrCrossTenantMismatch
	}

	baseEvent.FamilyID = exchangeRes.FamilyID
	baseEvent.UserID = exchangeRes.UserID
	baseEvent.OldTokenJTI = exchangeRes.OldTokenJTI
	baseEvent.NewTokenJTI = exchangeRes.NewTokenJTI
	baseEvent.Generation = exchangeRes.Generation

	// Resolve fresh user subject claims (roles, scopes, version, active status).
	claims := ports.SubjectClaims{
		UserID:       exchangeRes.UserID,
		TenantID:     tenantID,
		TokenVersion: 1,
		Active:       true,
	}
	if uc.claimsProvider != nil {
		loadedClaims, claimsErr := uc.claimsProvider.GetSubjectClaims(scopedCtx, tenantID, exchangeRes.UserID)
		if claimsErr != nil {
			ev := baseEvent
			ev.EventType = "auth.refresh.claims_lookup_failed"
			ev.Outcome = ports.OutcomeFailed
			ev.Severity = ports.SeverityCritical
			ev.Reason = "subject_claims_lookup_failed"
			uc.recordAudit(scopedCtx, ev, "failed to load subject claims during refresh")
			return nil, fmt.Errorf("application: failed to load subject claims: %w", claimsErr)
		}
		claims = loadedClaims
	}

	if claims.TenantID != "" && claims.TenantID != tenantID {
		if revErr := uc.tokenStore.RevokeFamily(scopedCtx, tenantID, exchangeRes.FamilyID, "cross_tenant_claims_mismatch", now); revErr != nil {
			uc.logger.ErrorContext(scopedCtx, "failed to revoke family after cross-tenant claims mismatch",
				slog.String("tenant_id", tenantID),
				slog.String("family_id", exchangeRes.FamilyID),
				slog.String("error", revErr.Error()),
			)
		}
		ev := baseEvent
		ev.EventType = "auth.refresh.cross_tenant_claims"
		ev.Outcome = ports.OutcomeDenied
		ev.Severity = ports.SeverityCritical
		ev.AlertCode = alertCodeCrossTenantViolation
		ev.Reason = "cross_tenant_claims_mismatch"
		uc.recordAudit(scopedCtx, ev, "security alert: subject claims tenant mismatch")
		return nil, ErrCrossTenantMismatch
	}

	if !claims.Active {
		if revErr := uc.tokenStore.RevokeFamily(scopedCtx, tenantID, exchangeRes.FamilyID, "user_inactive", now); revErr != nil {
			uc.logger.ErrorContext(scopedCtx, "failed to revoke family for inactive user",
				slog.String("tenant_id", tenantID),
				slog.String("family_id", exchangeRes.FamilyID),
				slog.String("error", revErr.Error()),
			)
		}
		ev := baseEvent
		ev.EventType = "auth.refresh.user_inactive"
		ev.Outcome = ports.OutcomeDenied
		ev.Severity = ports.SeverityWarn
		ev.Reason = "user_inactive"
		uc.recordAudit(scopedCtx, ev, "refresh token denied: user account inactive")
		return nil, ErrUserInactive
	}

	accessJTI, err := uc.tokenGen.GenerateJTI()
	if err != nil {
		ev := baseEvent
		ev.EventType = "auth.refresh.access_jti_failed"
		ev.Outcome = ports.OutcomeFailed
		ev.Severity = ports.SeverityCritical
		ev.Reason = "access_jti_generation_error"
		uc.recordAudit(scopedCtx, ev, "failed to generate access token jti")
		return nil, err
	}

	accessExpiresAt := now.Add(uc.accessTokenTTL)
	audienceCopy := make([]string, len(uc.audience))
	copy(audienceCopy, uc.audience)
	var rolesCopy []string
	if len(claims.Roles) > 0 {
		rolesCopy = make([]string, len(claims.Roles))
		copy(rolesCopy, claims.Roles)
	}
	var scopesCopy []string
	if len(claims.Scopes) > 0 {
		scopesCopy = make([]string, len(claims.Scopes))
		copy(scopesCopy, claims.Scopes)
	}

	accessRes, err := uc.accessIssuer.IssueAccessToken(scopedCtx, ports.AccessTokenClaims{
		Issuer:       uc.issuer,
		Subject:      exchangeRes.UserID,
		Audience:     audienceCopy,
		TenantID:     tenantID,
		FamilyID:     exchangeRes.FamilyID,
		JTI:          accessJTI,
		Roles:        rolesCopy,
		Scopes:       scopesCopy,
		TokenVersion: claims.TokenVersion,
		IssuedAt:     now,
		ExpiresAt:    accessExpiresAt,
	})
	if err != nil {
		ev := baseEvent
		ev.EventType = "auth.refresh.access_token_issue_failed"
		ev.Outcome = ports.OutcomeFailed
		ev.Severity = ports.SeverityCritical
		ev.Reason = "access_token_signing_failed"
		uc.recordAudit(scopedCtx, ev, "failed to issue short-lived access token")
		return nil, fmt.Errorf("application: failed to issue access token: %w", err)
	}

	tokenType := accessRes.TokenType
	if tokenType == "" {
		tokenType = "Bearer"
	}
	issuedAccessJTI := accessRes.JTI
	if issuedAccessJTI == "" {
		issuedAccessJTI = accessJTI
	}
	issuedAccessExp := accessRes.ExpiresAt
	if issuedAccessExp.IsZero() {
		issuedAccessExp = accessExpiresAt
	}

	successEv := baseEvent
	successEv.EventType = "auth.refresh.succeeded"
	successEv.Outcome = ports.OutcomeSuccess
	successEv.Severity = ports.SeverityInfo
	successEv.Reason = "token_rotated"
	uc.recordAudit(scopedCtx, successEv, "refresh token rotated")

	return &RefreshTokenResult{
		AccessToken:           accessRes.Token,
		AccessTokenJTI:        issuedAccessJTI,
		AccessTokenExpiresAt:  issuedAccessExp,
		TokenType:             tokenType,
		RefreshToken:          newRawRefreshToken,
		RefreshTokenJTI:       exchangeRes.NewTokenJTI,
		RefreshTokenExpiresAt: newRefreshExpiresAt,
		FamilyID:              exchangeRes.FamilyID,
		TenantID:              tenantID,
		UserID:                exchangeRes.UserID,
		Generation:            exchangeRes.Generation,
	}, nil
}

func (uc *RefreshTokenUseCase) resolveTenant(ctx context.Context, cmdTenantID string) (string, context.Context, error) {
	ctxTenantID, ctxErr := tenant.TenantIDFromContext(ctx)
	trimmedCmd := strings.TrimSpace(cmdTenantID)

	if ctxErr == nil && trimmedCmd != "" && ctxTenantID != trimmedCmd {
		return "", ctx, ErrCrossTenantMismatch
	}

	target := trimmedCmd
	if target == "" {
		if ctxErr != nil {
			return "", ctx, tenant.ErrMissingTenant
		}
		target = ctxTenantID
	}

	tc, err := tenant.New(target)
	if err != nil {
		return "", ctx, err
	}

	if ctxErr != nil {
		ctx = tenant.WithContext(ctx, tc)
	}
	return tc.TenantID(), ctx, nil
}

func (uc *RefreshTokenUseCase) resolveGeoHash(ctx context.Context, clientIP, explicitGeoHash string, hint ports.GeoHint) string {
	if sanitized := SanitizeGeoHash(explicitGeoHash); sanitized != "" {
		return sanitized
	}
	return uc.geoResolver.ResolveGeoHash(ctx, clientIP, hint)
}

func (uc *RefreshTokenUseCase) auditExchangeError(ctx context.Context, base ports.AuditEvent, err error) {
	ev := base

	var exErr *ports.TokenExchangeError
	if errors.As(err, &exErr) {
		ev.FamilyID = exErr.FamilyID
		ev.UserID = exErr.UserID
		ev.OldTokenJTI = exErr.ReplayedJTI
		ev.AlertCode = exErr.AlertCode
	}

	msg := "refresh token exchange rejected"
	switch {
	case errors.Is(err, tokenfamily.ErrTokenReused):
		ev.EventType = "auth.refresh.token_reuse_detected"
		ev.Outcome = ports.OutcomeReuseDetected
		ev.Severity = ports.SeverityCritical
		if ev.AlertCode == "" {
			ev.AlertCode = tokenfamily.SecurityAlertTokenReuse
		}
		ev.Reason = "used_refresh_token_replayed"
		msg = "security alert: refresh token reuse detected"

	case errors.Is(err, tokenfamily.ErrFamilyCompromised):
		ev.EventType = "auth.refresh.family_compromised"
		ev.Outcome = ports.OutcomeDenied
		ev.Severity = ports.SeverityCritical
		if ev.AlertCode == "" {
			ev.AlertCode = tokenfamily.SecurityAlertTokenReuse
		}
		ev.Reason = "family_already_compromised"
		msg = "refresh token rejected: family compromised"

	case errors.Is(err, tokenfamily.ErrFamilyRevoked):
		ev.EventType = "auth.refresh.family_revoked"
		ev.Outcome = ports.OutcomeDenied
		ev.Severity = ports.SeverityWarn
		ev.Reason = "family_revoked"
		msg = "refresh token rejected: family revoked"

	case errors.Is(err, tokenfamily.ErrTokenExpired):
		ev.EventType = "auth.refresh.token_expired"
		ev.Outcome = ports.OutcomeDenied
		ev.Severity = ports.SeverityInfo
		ev.Reason = "token_expired"
		msg = "refresh token rejected: token expired"

	case errors.Is(err, tokenfamily.ErrTokenNotFound):
		ev.EventType = "auth.refresh.token_not_found"
		ev.Outcome = ports.OutcomeDenied
		ev.Severity = ports.SeverityWarn
		ev.Reason = "token_not_found"
		msg = "refresh token rejected: token not found"

	case errors.Is(err, ports.ErrCrossTenantViolation):
		ev.EventType = "auth.refresh.cross_tenant_violation"
		ev.Outcome = ports.OutcomeDenied
		ev.Severity = ports.SeverityCritical
		ev.AlertCode = alertCodeCrossTenantViolation
		ev.Reason = "cross_tenant_token_access"
		msg = "security alert: cross-tenant refresh token attempt"

	default:
		ev.EventType = "auth.refresh.failed"
		ev.Outcome = ports.OutcomeFailed
		ev.Severity = ports.SeverityCritical
		ev.Reason = "token_store_error"
		msg = "refresh token exchange failed"
	}

	uc.recordAudit(ctx, ev, msg)
}

func (uc *RefreshTokenUseCase) recordAudit(ctx context.Context, ev ports.AuditEvent, msg string) {
	attrs := []any{
		slog.String("audit_event_id", ev.EventID),
		slog.String("event_type", ev.EventType),
		slog.String("severity", string(ev.Severity)),
		slog.String("outcome", string(ev.Outcome)),
		slog.String("tenant_id", ev.TenantID),
		slog.String("client_ip", ev.ClientIP),
		slog.String("user_agent", ev.UserAgent),
		slog.String("geo_hash", ev.GeoHash),
	}

	if ev.AlertCode != "" {
		attrs = append(attrs, slog.String("alert_code", ev.AlertCode))
	}
	if ev.UserID != "" {
		attrs = append(attrs, slog.String("user_id", ev.UserID))
	}
	if ev.FamilyID != "" {
		attrs = append(attrs, slog.String("family_id", ev.FamilyID))
	}
	if ev.OldTokenJTI != "" {
		attrs = append(attrs, slog.String("old_token_jti", ev.OldTokenJTI))
	}
	if ev.NewTokenJTI != "" {
		attrs = append(attrs, slog.String("new_token_jti", ev.NewTokenJTI))
	}
	if ev.Generation > 0 {
		attrs = append(attrs, slog.Int("generation", ev.Generation))
	}
	if ev.Reason != "" {
		attrs = append(attrs, slog.String("reason", ev.Reason))
	}
	if ev.RequestID != "" {
		attrs = append(attrs, slog.String("request_id", ev.RequestID))
	}

	switch ev.Severity {
	case ports.SeverityCritical:
		uc.logger.ErrorContext(ctx, msg, attrs...)
	case ports.SeverityWarn:
		uc.logger.WarnContext(ctx, msg, attrs...)
	default:
		uc.logger.InfoContext(ctx, msg, attrs...)
	}

	if uc.auditLogger != nil {
		if err := uc.auditLogger.LogRefreshAudit(ctx, ev); err != nil {
			deliveryAttrs := append([]any{slog.String("error", err.Error())}, attrs...)
			uc.logger.ErrorContext(ctx, "audit_logger_delivery_failed", deliveryAttrs...)
		}
	}
}

func newAuditEventID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}
