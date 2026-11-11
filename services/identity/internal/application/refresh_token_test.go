package application_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/threadline/pkg/observability"
	"github.com/threadline/pkg/tenant"
	"github.com/threadline/services/identity/internal/application"
	"github.com/threadline/services/identity/internal/application/ports"
	"github.com/threadline/services/identity/internal/domain/tokenfamily"
)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

type stubTokenGenerator struct {
	rawToken string
	jtiQueue []string
	idx      int
	err      error
}

func (g *stubTokenGenerator) GenerateRefreshToken() (string, string, error) {
	if g.err != nil {
		return "", "", g.err
	}
	return g.rawToken, tokenfamily.HashToken(g.rawToken), nil
}

func (g *stubTokenGenerator) GenerateJTI() (string, error) {
	if g.err != nil {
		return "", g.err
	}
	if g.idx < len(g.jtiQueue) {
		val := g.jtiQueue[g.idx]
		g.idx++
		return val, nil
	}
	return "01900000-0000-7000-8000-000000000099", nil
}

type mockTokenStore struct {
	exchangeCalled       bool
	capturedTenantID     string
	capturedPresented    string
	capturedNewHash      string
	capturedNewJTI       string
	capturedNewExpiresAt time.Time
	capturedNow          time.Time
	capturedTTL          time.Duration
	exchangeRes          *ports.ExchangeResult
	exchangeErr          error

	revokeCalled       bool
	revokeTenantID     string
	revokeFamilyID     string
	revokeReason       string
	revokeErr          error
}

func (m *mockTokenStore) CreateFamily(_ context.Context, _ *tokenfamily.TokenFamily, _ time.Duration) error {
	return nil
}

func (m *mockTokenStore) ExchangeToken(
	_ context.Context,
	tenantID string,
	presentedTokenHash string,
	newTokenHash string,
	newJTI string,
	newExpiresAt time.Time,
	now time.Time,
	ttl time.Duration,
) (*ports.ExchangeResult, error) {
	m.exchangeCalled = true
	m.capturedTenantID = tenantID
	m.capturedPresented = presentedTokenHash
	m.capturedNewHash = newTokenHash
	m.capturedNewJTI = newJTI
	m.capturedNewExpiresAt = newExpiresAt
	m.capturedNow = now
	m.capturedTTL = ttl
	return m.exchangeRes, m.exchangeErr
}

func (m *mockTokenStore) GetFamily(_ context.Context, _, _ string) (*tokenfamily.TokenFamily, error) {
	return nil, nil
}

func (m *mockTokenStore) RevokeFamily(_ context.Context, tenantID, familyID, reason string, _ time.Time) error {
	m.revokeCalled = true
	m.revokeTenantID = tenantID
	m.revokeFamilyID = familyID
	m.revokeReason = reason
	return m.revokeErr
}

func (m *mockTokenStore) CompromiseFamily(_ context.Context, _, _, _ string, _ time.Time) error {
	return nil
}

type mockAccessTokenIssuer struct {
	called         bool
	capturedClaims ports.AccessTokenClaims
	result         ports.AccessTokenResult
	err            error
}

func (m *mockAccessTokenIssuer) IssueAccessToken(_ context.Context, claims ports.AccessTokenClaims) (ports.AccessTokenResult, error) {
	m.called = true
	m.capturedClaims = claims
	if m.err != nil {
		return ports.AccessTokenResult{}, m.err
	}
	return m.result, nil
}

type mockSubjectClaimsProvider struct {
	called bool
	claims ports.SubjectClaims
	err    error
}

func (m *mockSubjectClaimsProvider) GetSubjectClaims(_ context.Context, _, _ string) (ports.SubjectClaims, error) {
	m.called = true
	return m.claims, m.err
}

type mockAuditLogger struct {
	events []ports.AuditEvent
	err    error
}

func (m *mockAuditLogger) LogRefreshAudit(_ context.Context, event ports.AuditEvent) error {
	m.events = append(m.events, event)
	return m.err
}

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return observability.NewJSONLogger("identity-service", "test", buf)
}

func TestNewRefreshTokenUseCase_Validation(t *testing.T) {
	store := &mockTokenStore{}
	issuer := &mockAccessTokenIssuer{}
	audit := &mockAuditLogger{}

	if _, err := application.NewRefreshTokenUseCase(nil, issuer, audit); !errors.Is(err, application.ErrNilTokenStore) {
		t.Fatalf("expected ErrNilTokenStore, got: %v", err)
	}
	if _, err := application.NewRefreshTokenUseCase(store, nil, audit); !errors.Is(err, application.ErrNilAccessTokenIssuer) {
		t.Fatalf("expected ErrNilAccessTokenIssuer, got: %v", err)
	}
	if _, err := application.NewRefreshTokenUseCase(store, issuer, audit, application.WithAccessTokenTTL(0)); !errors.Is(err, application.ErrInvalidTokenTTL) {
		t.Fatalf("expected ErrInvalidTokenTTL for zero access TTL, got: %v", err)
	}
	if _, err := application.NewRefreshTokenUseCase(store, issuer, audit, application.WithRefreshTokenTTL(-time.Minute)); !errors.Is(err, application.ErrInvalidTokenTTL) {
		t.Fatalf("expected ErrInvalidTokenTTL for negative refresh TTL, got: %v", err)
	}
}

func TestRefreshTokenUseCase_LegitimateRotation(t *testing.T) {
	now := time.Date(2026, 11, 12, 8, 30, 0, 0, time.UTC)
	rawRT1 := "rt_1_super_secret_token_never_log_this"
	rawRT2 := "rt_2_new_super_secret_token_never_log_this"
	accessJWT := "eyJhbGciOiJSUzI1NiJ9.payload.signature_never_log_this"

	store := &mockTokenStore{
		exchangeRes: &ports.ExchangeResult{
			FamilyID:    "fam-001",
			TenantID:    "zalora-vn",
			UserID:      "usr-777",
			OldTokenJTI: "jti-rt-1",
			NewTokenJTI: "jti-rt-2",
			Generation:  1,
			RotatedAt:   now,
		},
	}
	issuer := &mockAccessTokenIssuer{
		result: ports.AccessTokenResult{
			Token:     accessJWT,
			JTI:       "jti-acc-2",
			ExpiresAt: now.Add(application.DefaultAccessTokenTTL),
			TokenType: "Bearer",
		},
	}
	claimsProvider := &mockSubjectClaimsProvider{
		claims: ports.SubjectClaims{
			UserID:       "usr-777",
			TenantID:     "zalora-vn",
			Roles:        []string{"customer"},
			Scopes:       []string{"order:read:own", "cart:write"},
			TokenVersion: 3,
			Active:       true,
		},
	}
	audit := &mockAuditLogger{}
	var logBuf bytes.Buffer

	uc, err := application.NewRefreshTokenUseCase(
		store,
		issuer,
		audit,
		application.WithClock(fixedClock{now: now}),
		application.WithTokenGenerator(&stubTokenGenerator{
			rawToken: rawRT2,
			jtiQueue: []string{"jti-rt-2", "jti-acc-2"},
		}),
		application.WithSubjectClaimsProvider(claimsProvider),
		application.WithLogger(newTestLogger(&logBuf)),
	)
	if err != nil {
		t.Fatalf("failed to create use case: %v", err)
	}

	ctx, err := tenant.WithTenantID(context.Background(), "zalora-vn")
	if err != nil {
		t.Fatalf("failed to set tenant context: %v", err)
	}

	res, err := uc.Execute(ctx, application.RefreshTokenCommand{
		RefreshToken: rawRT1,
		ClientIP:     "203.0.113.45:54321",
		UserAgent:    "Mozilla/5.0 (Macintosh; Intel Mac OS X) ThreadlineApp/2.1",
		RequestID:    "req-123",
	})
	if err != nil {
		t.Fatalf("expected successful refresh rotation, got: %v", err)
	}

	// Verify presented token was hashed with SHA-256 before hitting Redis TokenStore
	if !store.exchangeCalled {
		t.Fatal("expected TokenStore.ExchangeToken to be called")
	}
	if store.capturedTenantID != "zalora-vn" {
		t.Errorf("expected tenant 'zalora-vn', got %q", store.capturedTenantID)
	}
	if store.capturedPresented != tokenfamily.HashToken(rawRT1) {
		t.Errorf("expected presented hash %q, got %q", tokenfamily.HashToken(rawRT1), store.capturedPresented)
	}
	if store.capturedNewHash != tokenfamily.HashToken(rawRT2) {
		t.Errorf("expected new hash %q, got %q", tokenfamily.HashToken(rawRT2), store.capturedNewHash)
	}
	if store.capturedNewJTI != "jti-rt-2" {
		t.Errorf("expected new JTI 'jti-rt-2', got %q", store.capturedNewJTI)
	}
	if !store.capturedNow.Equal(now) {
		t.Errorf("expected captured now %v, got %v", now, store.capturedNow)
	}
	if !store.capturedNewExpiresAt.Equal(now.Add(application.DefaultRefreshTokenTTL)) {
		t.Errorf("expected new expires at %v, got %v", now.Add(application.DefaultRefreshTokenTTL), store.capturedNewExpiresAt)
	}
	if store.capturedTTL != application.DefaultRefreshTokenTTL {
		t.Errorf("expected refresh TTL %v, got %v", application.DefaultRefreshTokenTTL, store.capturedTTL)
	}

	// Verify 15m JWT claims forwarded from SubjectClaimsProvider
	if !claimsProvider.called || !issuer.called {
		t.Fatal("expected claimsProvider and accessIssuer to be called")
	}
	if issuer.capturedClaims.ExpiresAt.Sub(issuer.capturedClaims.IssuedAt) != 15*time.Minute {
		t.Errorf("expected 15m access token TTL, got %v", issuer.capturedClaims.ExpiresAt.Sub(issuer.capturedClaims.IssuedAt))
	}
	if issuer.capturedClaims.TokenVersion != 3 {
		t.Errorf("expected TokenVersion 3, got %d", issuer.capturedClaims.TokenVersion)
	}
	if len(issuer.capturedClaims.Roles) != 1 || issuer.capturedClaims.Roles[0] != "customer" {
		t.Errorf("unexpected roles: %v", issuer.capturedClaims.Roles)
	}

	// Verify returned tokens
	if res.AccessToken != accessJWT {
		t.Errorf("unexpected access token: %q", res.AccessToken)
	}
	if res.RefreshToken != rawRT2 {
		t.Errorf("unexpected refresh token: %q", res.RefreshToken)
	}
	if res.TokenType != "Bearer" {
		t.Errorf("expected TokenType Bearer, got %q", res.TokenType)
	}
	if res.Generation != 1 {
		t.Errorf("expected Generation 1, got %d", res.Generation)
	}

	// Verify audit event captures ClientIP, UserAgent, GeoHash
	if len(audit.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(audit.events))
	}
	ev := audit.events[0]
	if ev.Outcome != ports.OutcomeSuccess {
		t.Errorf("expected OutcomeSuccess, got %q", ev.Outcome)
	}
	if ev.Severity != ports.SeverityInfo {
		t.Errorf("expected SeverityInfo, got %q", ev.Severity)
	}
	if ev.ClientIP != "203.0.113.45" {
		t.Errorf("expected normalized ClientIP '203.0.113.45', got %q", ev.ClientIP)
	}
	if ev.UserAgent != "Mozilla/5.0 (Macintosh; Intel Mac OS X) ThreadlineApp/2.1" {
		t.Errorf("unexpected UserAgent: %q", ev.UserAgent)
	}
	if ev.GeoHash == "" || ev.GeoHash == "unknown" {
		t.Errorf("expected deterministic non-empty GeoHash, got %q", ev.GeoHash)
	}
	if ev.FamilyID != "fam-001" || ev.UserID != "usr-777" || ev.TenantID != "zalora-vn" {
		t.Errorf("unexpected identity fields in audit event: %+v", ev)
	}
	if ev.OldTokenJTI != "jti-rt-1" || ev.NewTokenJTI != "jti-rt-2" {
		t.Errorf("unexpected JTI audit trail: old=%q new=%q", ev.OldTokenJTI, ev.NewTokenJTI)
	}

	// Verify secrets NEVER appear in structured logs
	logOutput := logBuf.String()
	for _, secret := range []string{rawRT1, rawRT2, tokenfamily.HashToken(rawRT1), tokenfamily.HashToken(rawRT2), accessJWT} {
		if strings.Contains(logOutput, secret) {
			t.Fatalf("sensitive credential leaked in structured logs: %s", secret)
		}
	}
	var logEntry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logBuf.Bytes()), &logEntry); err != nil {
		t.Fatalf("expected valid JSON log line, got err: %v, raw: %s", err, logOutput)
	}
	if logEntry["client_ip"] != "203.0.113.45" {
		t.Errorf("expected client_ip in structured log, got %v", logEntry["client_ip"])
	}
	if logEntry["geo_hash"] != ev.GeoHash {
		t.Errorf("expected geo_hash %q in structured log, got %v", ev.GeoHash, logEntry["geo_hash"])
	}
	if logEntry["tenant_id"] != "zalora-vn" {
		t.Errorf("expected tenant_id in structured log, got %v", logEntry["tenant_id"])
	}
}

func TestRefreshTokenUseCase_ReuseDetectionSecurityAlert(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	replayedRT1 := "rt_1_stolen_replayed_secret"

	store := &mockTokenStore{
		exchangeErr: &ports.TokenExchangeError{
			Err:         tokenfamily.ErrTokenReused,
			Status:      "TOKEN_REUSED",
			FamilyID:    "fam-compromised-99",
			TenantID:    "iconic-au",
			UserID:      "usr-victim-1",
			ReplayedJTI: "jti-rt-1",
			AlertCode:   tokenfamily.SecurityAlertTokenReuse,
		},
	}
	issuer := &mockAccessTokenIssuer{}
	audit := &mockAuditLogger{}
	var logBuf bytes.Buffer

	uc, err := application.NewRefreshTokenUseCase(
		store,
		issuer,
		audit,
		application.WithClock(fixedClock{now: now}),
		application.WithTokenGenerator(&stubTokenGenerator{rawToken: "rt_unused", jtiQueue: []string{"jti-unused"}}),
		application.WithLogger(newTestLogger(&logBuf)),
	)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	ctx, ctxErr := tenant.WithTenantID(context.Background(), "iconic-au")
	if ctxErr != nil {
		t.Fatalf("failed to build tenant context: %v", ctxErr)
	}
	res, err := uc.Execute(ctx, application.RefreshTokenCommand{
		RefreshToken: replayedRT1,
		ClientIP:     "[2001:db8:85a3::8a2e:370:7334]:8443",
		UserAgent:    "curl/8.5.0\r\nFakeHeader: injected",
		GeoHint: ports.GeoHint{
			Country: "RU",
			Region:  "MOW",
			City:    "Moscow",
		},
		RequestID: "req-attack-1",
	})

	if res != nil {
		t.Fatalf("expected nil result on reuse detection, got: %+v", res)
	}
	if !errors.Is(err, tokenfamily.ErrTokenReused) {
		t.Fatalf("expected ErrTokenReused, got: %v", err)
	}
	if issuer.called {
		t.Fatal("AccessTokenIssuer must NOT be called when token reuse is detected")
	}

	if len(audit.events) != 1 {
		t.Fatalf("expected 1 critical audit event, got %d", len(audit.events))
	}
	ev := audit.events[0]
	if ev.Outcome != ports.OutcomeReuseDetected {
		t.Errorf("expected OutcomeReuseDetected, got %q", ev.Outcome)
	}
	if ev.Severity != ports.SeverityCritical {
		t.Errorf("expected SeverityCritical, got %q", ev.Severity)
	}
	if ev.AlertCode != tokenfamily.SecurityAlertTokenReuse {
		t.Errorf("expected AlertCode %q, got %q", tokenfamily.SecurityAlertTokenReuse, ev.AlertCode)
	}
	if ev.FamilyID != "fam-compromised-99" || ev.UserID != "usr-victim-1" || ev.OldTokenJTI != "jti-rt-1" {
		t.Errorf("missing enriched compromise context in audit event: %+v", ev)
	}
	if ev.ClientIP != "2001:db8:85a3::8a2e:370:7334" {
		t.Errorf("expected normalized IPv6 address, got %q", ev.ClientIP)
	}
	if strings.ContainsAny(ev.UserAgent, "\r\n") {
		t.Errorf("expected CRLF stripped from UserAgent, got %q", ev.UserAgent)
	}
	if ev.GeoHash == "" || ev.GeoHash == "unknown" {
		t.Errorf("expected non-empty geo hash for attacker geo hint, got %q", ev.GeoHash)
	}

	logOut := logBuf.String()
	if !strings.Contains(logOut, tokenfamily.SecurityAlertTokenReuse) {
		t.Errorf("expected A07_TOKEN_REUSE alert code in structured log, got: %s", logOut)
	}
	if strings.Contains(logOut, replayedRT1) {
		t.Fatal("replayed raw refresh token leaked in security alert log")
	}
}

func TestRefreshTokenUseCase_FailClosedMultiTenancy(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)

	t.Run("rejects missing tenant context fail-closed per AD-4", func(t *testing.T) {
		store := &mockTokenStore{}
		issuer := &mockAccessTokenIssuer{}
		audit := &mockAuditLogger{}
		var logBuf bytes.Buffer

		uc, ucErr := application.NewRefreshTokenUseCase(
			store,
			issuer,
			audit,
			application.WithClock(fixedClock{now: now}),
			application.WithLogger(newTestLogger(&logBuf)),
		)
		if ucErr != nil {
			t.Fatalf("unexpected constructor error: %v", ucErr)
		}

		_, err := uc.Execute(context.Background(), application.RefreshTokenCommand{
			RefreshToken: "rt-secret",
			ClientIP:     "198.51.100.12",
			UserAgent:    "TestAgent/1.0",
		})
		if !errors.Is(err, tenant.ErrMissingTenant) {
			t.Fatalf("expected ErrMissingTenant, got: %v", err)
		}
		if store.exchangeCalled {
			t.Fatal("TokenStore must not be called when tenant is missing")
		}
		if len(audit.events) != 1 || audit.events[0].Outcome != ports.OutcomeDenied {
			t.Fatalf("expected denied audit event, got: %+v", audit.events)
		}
	})

	t.Run("rejects cross-tenant mismatch between context and command", func(t *testing.T) {
		store := &mockTokenStore{}
		issuer := &mockAccessTokenIssuer{}
		audit := &mockAuditLogger{}
		var logBuf bytes.Buffer

		uc, ucErr := application.NewRefreshTokenUseCase(
			store,
			issuer,
			audit,
			application.WithClock(fixedClock{now: now}),
			application.WithLogger(newTestLogger(&logBuf)),
		)
		if ucErr != nil {
			t.Fatalf("unexpected constructor error: %v", ucErr)
		}

		ctx, ctxErr := tenant.WithTenantID(context.Background(), "zalora-vn")
		if ctxErr != nil {
			t.Fatalf("failed to build tenant context: %v", ctxErr)
		}
		_, err := uc.Execute(ctx, application.RefreshTokenCommand{
			TenantID:     "iconic-au",
			RefreshToken: "rt-secret",
			ClientIP:     "198.51.100.12",
			UserAgent:    "TestAgent/1.0",
		})
		if !errors.Is(err, application.ErrCrossTenantMismatch) {
			t.Fatalf("expected ErrCrossTenantMismatch, got: %v", err)
		}
		if store.exchangeCalled {
			t.Fatal("TokenStore must not be called on cross-tenant mismatch")
		}
		if len(audit.events) != 1 || audit.events[0].Severity != ports.SeverityCritical {
			t.Fatalf("expected critical audit event for cross-tenant mismatch, got: %+v", audit.events)
		}
	})

	t.Run("rejects redis hash-tag injection characters in tenant id", func(t *testing.T) {
		store := &mockTokenStore{}
		issuer := &mockAccessTokenIssuer{}
		audit := &mockAuditLogger{}

		uc, ucErr := application.NewRefreshTokenUseCase(
			store,
			issuer,
			audit,
			application.WithClock(fixedClock{now: now}),
			application.WithLogger(newTestLogger(&bytes.Buffer{})),
		)
		if ucErr != nil {
			t.Fatalf("unexpected constructor error: %v", ucErr)
		}

		_, err := uc.Execute(context.Background(), application.RefreshTokenCommand{
			TenantID:     "zalora-vn}:token:evil",
			RefreshToken: "rt-secret",
			ClientIP:     "198.51.100.12",
		})
		if !errors.Is(err, tenant.ErrInvalidTenantID) {
			t.Fatalf("expected ErrInvalidTenantID, got: %v", err)
		}
		if store.exchangeCalled {
			t.Fatal("TokenStore must not be called with invalid tenant ID")
		}
	})

	t.Run("revokes family when SubjectClaimsProvider returns cross-tenant claims", func(t *testing.T) {
		store := &mockTokenStore{
			exchangeRes: &ports.ExchangeResult{
				FamilyID:    "fam-cross-claims",
				TenantID:    "zalora-vn",
				UserID:      "usr-1",
				OldTokenJTI: "jti-1",
				NewTokenJTI: "jti-2",
				Generation:  1,
				RotatedAt:   now,
			},
		}
		issuer := &mockAccessTokenIssuer{}
		claimsProvider := &mockSubjectClaimsProvider{
			claims: ports.SubjectClaims{
				UserID:   "usr-1",
				TenantID: "iconic-au",
				Active:   true,
			},
		}
		audit := &mockAuditLogger{}

		uc, ucErr := application.NewRefreshTokenUseCase(
			store,
			issuer,
			audit,
			application.WithClock(fixedClock{now: now}),
			application.WithTokenGenerator(&stubTokenGenerator{rawToken: "rt-2", jtiQueue: []string{"jti-2"}}),
			application.WithSubjectClaimsProvider(claimsProvider),
			application.WithLogger(newTestLogger(&bytes.Buffer{})),
		)
		if ucErr != nil {
			t.Fatalf("unexpected constructor error: %v", ucErr)
		}

		ctx, ctxErr := tenant.WithTenantID(context.Background(), "zalora-vn")
		if ctxErr != nil {
			t.Fatalf("failed to build tenant context: %v", ctxErr)
		}
		_, err := uc.Execute(ctx, application.RefreshTokenCommand{
			RefreshToken: "rt-1",
			ClientIP:     "203.0.113.10",
			UserAgent:    "TestAgent/1.0",
		})
		if !errors.Is(err, application.ErrCrossTenantMismatch) {
			t.Fatalf("expected ErrCrossTenantMismatch, got: %v", err)
		}
		if !store.revokeCalled || store.revokeReason != "cross_tenant_claims_mismatch" {
			t.Fatalf("expected RevokeFamily on cross-tenant claims mismatch, got called=%v reason=%q", store.revokeCalled, store.revokeReason)
		}
		if issuer.called {
			t.Fatal("AccessTokenIssuer must not be called on cross-tenant claims mismatch")
		}
	})

	t.Run("rejects mismatched tenant returned in exchange result", func(t *testing.T) {
		store := &mockTokenStore{
			exchangeRes: &ports.ExchangeResult{
				FamilyID:    "fam-mismatch",
				TenantID:    "iconic-au",
				UserID:      "usr-1",
				OldTokenJTI: "jti-1",
				NewTokenJTI: "jti-2",
				Generation:  1,
				RotatedAt:   now,
			},
		}
		issuer := &mockAccessTokenIssuer{}
		audit := &mockAuditLogger{}

		uc, ucErr := application.NewRefreshTokenUseCase(
			store,
			issuer,
			audit,
			application.WithClock(fixedClock{now: now}),
			application.WithTokenGenerator(&stubTokenGenerator{rawToken: "rt-2", jtiQueue: []string{"jti-2"}}),
			application.WithLogger(newTestLogger(&bytes.Buffer{})),
		)
		if ucErr != nil {
			t.Fatalf("unexpected constructor error: %v", ucErr)
		}

		ctx, ctxErr := tenant.WithTenantID(context.Background(), "zalora-vn")
		if ctxErr != nil {
			t.Fatalf("failed to build tenant context: %v", ctxErr)
		}
		_, err := uc.Execute(ctx, application.RefreshTokenCommand{
			RefreshToken: "rt-1",
			ClientIP:     "203.0.113.10",
			UserAgent:    "TestAgent/1.0",
		})
		if !errors.Is(err, application.ErrCrossTenantMismatch) {
			t.Fatalf("expected ErrCrossTenantMismatch, got: %v", err)
		}
		if issuer.called {
			t.Fatal("AccessTokenIssuer must not be called on exchange result tenant mismatch")
		}
		if len(audit.events) != 1 || audit.events[0].Severity != ports.SeverityCritical {
			t.Fatalf("expected critical audit event, got: %+v", audit.events)
		}
	})
}

func TestRefreshTokenUseCase_InactiveUserRevokesFamily(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

	store := &mockTokenStore{
		exchangeRes: &ports.ExchangeResult{
			FamilyID:    "fam-locked-user",
			TenantID:    "zalora-vn",
			UserID:      "usr-banned",
			OldTokenJTI: "jti-1",
			NewTokenJTI: "jti-2",
			Generation:  1,
			RotatedAt:   now,
		},
	}
	issuer := &mockAccessTokenIssuer{}
	claimsProvider := &mockSubjectClaimsProvider{
		claims: ports.SubjectClaims{
			UserID:   "usr-banned",
			TenantID: "zalora-vn",
			Active:   false,
		},
	}
	audit := &mockAuditLogger{}

	uc, err := application.NewRefreshTokenUseCase(
		store,
		issuer,
		audit,
		application.WithClock(fixedClock{now: now}),
		application.WithTokenGenerator(&stubTokenGenerator{rawToken: "rt-2", jtiQueue: []string{"jti-2"}}),
		application.WithSubjectClaimsProvider(claimsProvider),
		application.WithLogger(newTestLogger(&bytes.Buffer{})),
	)
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	ctx, ctxErr := tenant.WithTenantID(context.Background(), "zalora-vn")
	if ctxErr != nil {
		t.Fatalf("failed to build tenant context: %v", ctxErr)
	}
	_, err = uc.Execute(ctx, application.RefreshTokenCommand{
		RefreshToken: "rt-1",
		ClientIP:     "203.0.113.10",
		UserAgent:    "Safari/17.0",
	})
	if !errors.Is(err, application.ErrUserInactive) {
		t.Fatalf("expected ErrUserInactive, got: %v", err)
	}
	if !store.revokeCalled || store.revokeFamilyID != "fam-locked-user" || store.revokeTenantID != "zalora-vn" || store.revokeReason != "user_inactive" {
		t.Fatalf("expected RevokeFamily to be called for inactive user, got called=%v tenant=%q family=%q reason=%q",
			store.revokeCalled, store.revokeTenantID, store.revokeFamilyID, store.revokeReason)
	}
	if issuer.called {
		t.Fatal("AccessTokenIssuer must not be called for inactive user")
	}
	if len(audit.events) != 1 || audit.events[0].Outcome != ports.OutcomeDenied {
		t.Fatalf("expected denied audit event, got: %+v", audit.events)
	}
}

func TestRefreshTokenUseCase_RejectionOutcomesAndAuditResilience(t *testing.T) {
	now := time.Date(2026, 11, 12, 11, 0, 0, 0, time.UTC)

	rejectionCases := []struct {
		name            string
		storeErr        error
		expectedOutcome ports.AuditOutcome
		expectedAlert   string
	}{
		{
			name:            "family compromised",
			storeErr:        tokenfamily.ErrFamilyCompromised,
			expectedOutcome: ports.OutcomeDenied,
			expectedAlert:   tokenfamily.SecurityAlertTokenReuse,
		},
		{
			name:            "family revoked",
			storeErr:        tokenfamily.ErrFamilyRevoked,
			expectedOutcome: ports.OutcomeDenied,
		},
		{
			name:            "token expired",
			storeErr:        tokenfamily.ErrTokenExpired,
			expectedOutcome: ports.OutcomeDenied,
		},
		{
			name:            "token not found",
			storeErr:        tokenfamily.ErrTokenNotFound,
			expectedOutcome: ports.OutcomeDenied,
		},
		{
			name:            "unwrapped token reused",
			storeErr:        tokenfamily.ErrTokenReused,
			expectedOutcome: ports.OutcomeReuseDetected,
			expectedAlert:   tokenfamily.SecurityAlertTokenReuse,
		},
		{
			name:            "cross tenant storage violation",
			storeErr:        ports.ErrCrossTenantViolation,
			expectedOutcome: ports.OutcomeDenied,
			expectedAlert:   "AD4_CROSS_TENANT_VIOLATION",
		},
	}

	for _, tc := range rejectionCases {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockTokenStore{exchangeErr: tc.storeErr}
			issuer := &mockAccessTokenIssuer{}
			audit := &mockAuditLogger{}

			uc, ucErr := application.NewRefreshTokenUseCase(
				store,
				issuer,
				audit,
				application.WithClock(fixedClock{now: now}),
				application.WithTokenGenerator(&stubTokenGenerator{rawToken: "rt-2", jtiQueue: []string{"jti-2"}}),
				application.WithLogger(newTestLogger(&bytes.Buffer{})),
			)
			if ucErr != nil {
				t.Fatalf("unexpected constructor error: %v", ucErr)
			}

			ctx, ctxErr := tenant.WithTenantID(context.Background(), "zalora-vn")
			if ctxErr != nil {
				t.Fatalf("failed to build tenant context: %v", ctxErr)
			}
			_, err := uc.Execute(ctx, application.RefreshTokenCommand{
				RefreshToken: "rt-presented",
				ClientIP:     "198.51.100.77",
				UserAgent:    "TestClient/2.0",
			})
			if !errors.Is(err, tc.storeErr) {
				t.Fatalf("expected error %v, got %v", tc.storeErr, err)
			}
			if len(audit.events) != 1 {
				t.Fatalf("expected 1 audit event, got %d", len(audit.events))
			}
			ev := audit.events[0]
			if ev.Outcome != tc.expectedOutcome {
				t.Errorf("expected outcome %q, got %q", tc.expectedOutcome, ev.Outcome)
			}
			if ev.AlertCode != tc.expectedAlert {
				t.Errorf("expected alert %q, got %q", tc.expectedAlert, ev.AlertCode)
			}
			if ev.ClientIP != "198.51.100.77" || ev.UserAgent != "TestClient/2.0" || ev.GeoHash == "" {
				t.Errorf("expected full client context in audit event: %+v", ev)
			}
		})
	}

	t.Run("audit logger delivery failure after rotation does not withhold rotated RT-2", func(t *testing.T) {
		store := &mockTokenStore{
			exchangeRes: &ports.ExchangeResult{
				FamilyID:    "fam-1",
				TenantID:    "zalora-vn",
				UserID:      "usr-1",
				OldTokenJTI: "jti-1",
				NewTokenJTI: "jti-2",
				Generation:  1,
				RotatedAt:   now,
			},
		}
		issuer := &mockAccessTokenIssuer{
			result: ports.AccessTokenResult{
				Token:     "jwt-access",
				JTI:       "jti-acc",
				ExpiresAt: now.Add(15 * time.Minute),
				TokenType: "Bearer",
			},
		}
		audit := &mockAuditLogger{err: errors.New("audit sink temporarily unavailable")}
		var logBuf bytes.Buffer

		uc, ucErr := application.NewRefreshTokenUseCase(
			store,
			issuer,
			audit,
			application.WithClock(fixedClock{now: now}),
			application.WithTokenGenerator(&stubTokenGenerator{rawToken: "rt-new-2", jtiQueue: []string{"jti-2", "jti-acc"}}),
			application.WithLogger(newTestLogger(&logBuf)),
		)
		if ucErr != nil {
			t.Fatalf("unexpected constructor error: %v", ucErr)
		}

		ctx, ctxErr := tenant.WithTenantID(context.Background(), "zalora-vn")
		if ctxErr != nil {
			t.Fatalf("failed to build tenant context: %v", ctxErr)
		}
		res, err := uc.Execute(ctx, application.RefreshTokenCommand{
			RefreshToken: "rt-1",
			ClientIP:     "203.0.113.5",
			UserAgent:    "ThreadlineMobile/1.0",
		})
		if err != nil {
			t.Fatalf("expected rotated tokens to still be returned when audit sink fails post-rotation, got: %v", err)
		}
		if res.RefreshToken != "rt-new-2" {
			t.Errorf("expected rt-new-2, got %q", res.RefreshToken)
		}
		if !strings.Contains(logBuf.String(), "audit_logger_delivery_failed") {
			t.Errorf("expected fallback error log for failed audit sink, got: %s", logBuf.String())
		}
	})
}

func TestAuditHelpers_GeoHashAndSanitization(t *testing.T) {
	t.Run("normalize client IP handles ports, IPv4-mapped IPv6, and invalid strings", func(t *testing.T) {
		cases := []struct {
			input    string
			expected string
		}{
			{"203.0.113.45", "203.0.113.45"},
			{"203.0.113.45:49152", "203.0.113.45"},
			{"[2001:db8::1]:443", "2001:db8::1"},
			{"::ffff:203.0.113.45", "203.0.113.45"},
			{"203.0.113.45, 10.0.0.5", "203.0.113.45"},
			{"203.0.113.45\r\nevil", "unknown"},
			{"", "unknown"},
			{"not-an-ip", "unknown"},
		}
		for _, tc := range cases {
			if got := application.NormalizeClientIP(tc.input); got != tc.expected {
				t.Errorf("NormalizeClientIP(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		}
	})

	t.Run("encode geohash matches canonical test vector", func(t *testing.T) {
		got := application.EncodeGeohash(57.64911, 10.40744, 11)
		if got != "u4pruydqqvj" {
			t.Errorf("expected u4pruydqqvj, got %q", got)
		}
	})

	t.Run("deterministic geo hasher groups /24 subnets and differentiates regions", func(t *testing.T) {
		hasher := application.NewDeterministicGeoHasher("test-pepper")
		ctx := context.Background()

		h1 := hasher.ResolveGeoHash(ctx, "203.0.113.10", ports.GeoHint{})
		h2 := hasher.ResolveGeoHash(ctx, "203.0.113.250", ports.GeoHint{})
		h3 := hasher.ResolveGeoHash(ctx, "198.51.100.10", ports.GeoHint{})

		if h1 == "unknown" || len(h1) != 64 {
			t.Fatalf("expected 64-char SHA-256 geo hash, got %q", h1)
		}
		if h1 != h2 {
			t.Errorf("expected same /24 subnet to map to identical geo hash (%q != %q)", h1, h2)
		}
		if h1 == h3 {
			t.Errorf("expected different /24 subnets to produce different geo hashes")
		}
		if hasher.ResolveGeoHash(ctx, "unknown", ports.GeoHint{}) != "unknown" {
			t.Errorf("expected 'unknown' when both IP and hint are missing")
		}
	})

	t.Run("deterministic geo hasher supports coordinate hints", func(t *testing.T) {
		hasher := application.NewDeterministicGeoHasher("test-pepper")
		ctx := context.Background()

		lat1, lon1 := 10.8231, 106.6297
		lat2, lon2 := 10.8235, 106.6299
		lat3, lon3 := 21.0285, 105.8542

		g1 := hasher.ResolveGeoHash(ctx, "unknown", ports.GeoHint{Latitude: &lat1, Longitude: &lon1})
		g2 := hasher.ResolveGeoHash(ctx, "unknown", ports.GeoHint{Latitude: &lat2, Longitude: &lon2})
		g3 := hasher.ResolveGeoHash(ctx, "unknown", ports.GeoHint{Latitude: &lat3, Longitude: &lon3})

		if g1 == "unknown" || len(g1) != 64 {
			t.Fatalf("expected 64-char SHA-256 geo hash, got %q", g1)
		}
		if g1 != g2 {
			t.Errorf("expected nearby coordinates in same cell to produce same geo hash (%q != %q)", g1, g2)
		}
		if g1 == g3 {
			t.Errorf("expected distant coordinates to produce different geo hashes")
		}
	})

	t.Run("crypto token generator produces high-entropy unique tokens and UUIDv7 JTIs", func(t *testing.T) {
		gen := application.NewCryptoTokenGenerator()
		raw1, hash1, err := gen.GenerateRefreshToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		raw2, hash2, err := gen.GenerateRefreshToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if raw1 == "" || raw1 == raw2 || hash1 == hash2 {
			t.Fatalf("expected distinct non-empty high-entropy tokens")
		}
		if hash1 != tokenfamily.HashToken(raw1) {
			t.Errorf("token hash mismatch with domain HashToken")
		}
		jti1, err := gen.GenerateJTI()
		if err != nil || jti1 == "" {
			t.Fatalf("expected valid JTI, got %q err=%v", jti1, err)
		}
	})

	t.Run("sanitize geo hash rejects injection characters and accepts valid edge tokens", func(t *testing.T) {
		if got := application.SanitizeGeoHash("  u4pruydqqvj  "); got != "u4pruydqqvj" {
			t.Errorf("expected trimmed valid geohash, got %q", got)
		}
		if got := application.SanitizeGeoHash("sg_sin:01-abc"); got != "sg_sin:01-abc" {
			t.Errorf("expected valid composite edge geo token, got %q", got)
		}
		if got := application.SanitizeGeoHash("bad\ngeo"); got != "" {
			t.Errorf("expected control-char geohash to be rejected, got %q", got)
		}
		if got := application.SanitizeGeoHash("bad geo with spaces"); got != "" {
			t.Errorf("expected whitespace geohash to be rejected, got %q", got)
		}
	})
}

func TestRefreshTokenUseCase_OptionsAndExplicitGeoHash(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

	t.Run("honors custom issuer, audience, TTLs, and explicit sanitized GeoHash", func(t *testing.T) {
		store := &mockTokenStore{
			exchangeRes: &ports.ExchangeResult{
				FamilyID:    "fam-opts",
				TenantID:    "zalora-vn",
				UserID:      "usr-opts",
				OldTokenJTI: "jti-1",
				NewTokenJTI: "jti-2",
				Generation:  2,
				RotatedAt:   now,
			},
		}
		issuer := &mockAccessTokenIssuer{
			result: ports.AccessTokenResult{Token: "jwt-custom"},
		}
		audit := &mockAuditLogger{}

		uc, err := application.NewRefreshTokenUseCase(
			store,
			issuer,
			audit,
			application.WithClock(fixedClock{now: now}),
			application.WithTokenGenerator(&stubTokenGenerator{rawToken: "rt-opt-2", jtiQueue: []string{"jti-2", "jti-acc-opt"}}),
			application.WithAccessTokenTTL(5*time.Minute),
			application.WithRefreshTokenTTL(7*24*time.Hour),
			application.WithTokenIssuer("https://id.threadline.example"),
			application.WithTokenAudience("api-v1", "checkout"),
			application.WithLogger(newTestLogger(&bytes.Buffer{})),
		)
		if err != nil {
			t.Fatalf("unexpected constructor error: %v", err)
		}

		ctx, err := tenant.WithTenantID(context.Background(), "zalora-vn")
		if err != nil {
			t.Fatalf("failed to build tenant context: %v", err)
		}

		res, err := uc.Execute(ctx, application.RefreshTokenCommand{
			RefreshToken: "rt-opt-1",
			ClientIP:     "192.0.2.44",
			UserAgent:    "ThreadlineWeb/3.0",
			GeoHash:      "vn_sgn_cell01",
		})
		if err != nil {
			t.Fatalf("unexpected execute error: %v", err)
		}
		if res.AccessTokenExpiresAt != now.Add(5*time.Minute) {
			t.Errorf("expected 5m access TTL, got %v", res.AccessTokenExpiresAt.Sub(now))
		}
		if res.RefreshTokenExpiresAt != now.Add(7*24*time.Hour) {
			t.Errorf("expected 7d refresh TTL, got %v", res.RefreshTokenExpiresAt.Sub(now))
		}
		if issuer.capturedClaims.Issuer != "https://id.threadline.example" {
			t.Errorf("unexpected issuer: %q", issuer.capturedClaims.Issuer)
		}
		if len(issuer.capturedClaims.Audience) != 2 || issuer.capturedClaims.Audience[1] != "checkout" {
			t.Errorf("unexpected audience: %v", issuer.capturedClaims.Audience)
		}
		if len(audit.events) != 1 || audit.events[0].GeoHash != "vn_sgn_cell01" {
			t.Fatalf("expected explicit sanitized GeoHash 'vn_sgn_cell01', got %+v", audit.events)
		}
	})

	t.Run("TokenExchangeError exposes underlying error and status", func(t *testing.T) {
		exErr := &ports.TokenExchangeError{
			Err:    tokenfamily.ErrTokenReused,
			Status: "TOKEN_REUSED",
		}
		if !errors.Is(exErr, tokenfamily.ErrTokenReused) {
			t.Fatalf("expected TokenExchangeError to unwrap to ErrTokenReused")
		}
		if exErr.Error() != tokenfamily.ErrTokenReused.Error() {
			t.Errorf("unexpected Error() string: %q", exErr.Error())
		}

		fallbackErr := &ports.TokenExchangeError{Status: "TOKEN_REUSED"}
		if fallbackErr.Error() != "TOKEN_REUSED" {
			t.Errorf("expected fallback Status string, got %q", fallbackErr.Error())
		}
	})
}
