package redis

import (
	"errors"
	"fmt"
)

var (
	// ErrEmptyTenantID is returned when tenantID is empty, ensuring fail-closed isolation per AD-4.
	ErrEmptyTenantID = errors.New("redis: tenant_id must not be empty (fail-closed)")

	// ErrEmptyTokenHash is returned when token hash is empty.
	ErrEmptyTokenHash = errors.New("redis: token_hash must not be empty")

	// ErrEmptyFamilyID is returned when familyID is empty.
	ErrEmptyFamilyID = errors.New("redis: family_id must not be empty")
)

// KeyPrefix returns the tenant-scoped key prefix with a Redis hash tag for cluster slot affinity.
func KeyPrefix(tenantID string) (string, error) {
	if tenantID == "" {
		return "", ErrEmptyTenantID
	}
	return fmt.Sprintf("thrd:{%s}", tenantID), nil
}

// TokenKey builds the key for a refresh token SHA-256 hash record.
// Format: thrd:{<tenant_id>}:token:<token_hash>
func TokenKey(tenantID, tokenHash string) (string, error) {
	if tenantID == "" {
		return "", ErrEmptyTenantID
	}
	if tokenHash == "" {
		return "", ErrEmptyTokenHash
	}
	return fmt.Sprintf("thrd:{%s}:token:%s", tenantID, tokenHash), nil
}

// FamilyKey builds the key for a TokenFamily metadata hash.
// Format: thrd:{<tenant_id>}:family:<family_id>
func FamilyKey(tenantID, familyID string) (string, error) {
	if tenantID == "" {
		return "", ErrEmptyTenantID
	}
	if familyID == "" {
		return "", ErrEmptyFamilyID
	}
	return fmt.Sprintf("thrd:{%s}:family:%s", tenantID, familyID), nil
}

// FamilyTokensKey builds the key for the set of token hashes in a family.
// Format: thrd:{<tenant_id>}:family:<family_id>:tokens
func FamilyTokensKey(tenantID, familyID string) (string, error) {
	if tenantID == "" {
		return "", ErrEmptyTenantID
	}
	if familyID == "" {
		return "", ErrEmptyFamilyID
	}
	return fmt.Sprintf("thrd:{%s}:family:%s:tokens", tenantID, familyID), nil
}
