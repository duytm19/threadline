package ports

import (
	"context"
	"errors"
	"time"

	"github.com/threadline/services/identity/internal/domain/tokenfamily"
)

// ErrCrossTenantViolation indicates a storage-level cross-tenant isolation breach.
var ErrCrossTenantViolation = errors.New("tokenstore: cross-tenant access violation detected")

// TokenExchangeError enriches a token exchange rejection error with storage metadata
// (such as FamilyID, UserID, ReplayedJTI, and AlertCode) for immutable security auditing.
type TokenExchangeError struct {
	Err         error
	Status      string
	FamilyID    string
	TenantID    string
	UserID      string
	ReplayedJTI string
	AlertCode   string
}

// Error returns the underlying error description.
func (e *TokenExchangeError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Status
}

// Unwrap exposes the wrapped sentinel error for errors.Is/errors.As matching.
func (e *TokenExchangeError) Unwrap() error {
	return e.Err
}

// ExchangeResult encapsulates the result of an atomic token exchange operation.
type ExchangeResult struct {
	FamilyID    string
	TenantID    string
	UserID      string
	OldTokenJTI string
	NewTokenJTI string
	Generation  int
	RotatedAt   time.Time
}

// TokenStore defines the driven port for managing token family lifecycle in persistent storage.
type TokenStore interface {
	// CreateFamily persists a newly initialized TokenFamily and its initial active token.
	CreateFamily(ctx context.Context, family *tokenfamily.TokenFamily, ttl time.Duration) error

	// ExchangeToken performs an atomic token exchange using the Lua script.
	// If the presented token is valid and unconsumed, it rotates to the new token.
	// If the presented token is already used, it immediately compromises the family and revokes all active tokens.
	ExchangeToken(
		ctx context.Context,
		tenantID string,
		presentedTokenHash string,
		newTokenHash string,
		newJTI string,
		newExpiresAt time.Time,
		now time.Time,
		ttl time.Duration,
	) (*ExchangeResult, error)

	// GetFamily fetches a TokenFamily and its active/used tokens from storage.
	GetFamily(ctx context.Context, tenantID, familyID string) (*tokenfamily.TokenFamily, error)

	// RevokeFamily terminates a family and marks all active tokens as revoked.
	RevokeFamily(ctx context.Context, tenantID, familyID, reason string, now time.Time) error

	// CompromiseFamily flags a family as compromised and invalidates all associated tokens.
	CompromiseFamily(ctx context.Context, tenantID, familyID, reason string, now time.Time) error
}
