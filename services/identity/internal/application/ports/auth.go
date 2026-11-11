package ports

import (
	"context"
	"time"
)

// AccessTokenClaims defines the standard JWT claims for an issued short-lived access token.
type AccessTokenClaims struct {
	Issuer       string
	Subject      string
	Audience     []string
	TenantID     string
	FamilyID     string
	JTI          string
	Roles        []string
	Scopes       []string
	TokenVersion int64
	IssuedAt     time.Time
	ExpiresAt    time.Time
}

// AccessTokenResult encapsulates the signed JWT access token and its metadata.
type AccessTokenResult struct {
	Token     string
	JTI       string
	ExpiresAt time.Time
	TokenType string
}

// AccessTokenIssuer defines the driven port for signing short-lived JWT access tokens (e.g., RS256).
type AccessTokenIssuer interface {
	IssueAccessToken(ctx context.Context, claims AccessTokenClaims) (AccessTokenResult, error)
}

// SubjectClaims represents current authorization metadata and account status for a user.
type SubjectClaims struct {
	UserID       string
	TenantID     string
	Roles        []string
	Scopes       []string
	TokenVersion int64
	Active       bool
}

// SubjectClaimsProvider defines the driven port for retrieving fresh user roles, scopes, and status.
type SubjectClaimsProvider interface {
	GetSubjectClaims(ctx context.Context, tenantID, userID string) (SubjectClaims, error)
}

// TokenGenerator defines the port for generating high-entropy refresh tokens and unique token IDs.
type TokenGenerator interface {
	GenerateRefreshToken() (rawToken string, tokenHash string, err error)
	GenerateJTI() (string, error)
}

// Clock provides current UTC time for deterministic testing and token lifecycle validation.
type Clock interface {
	Now() time.Time
}
