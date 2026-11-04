package redis

import (
	"errors"
	"fmt"

	"github.com/threadline/services/identity/internal/domain/tokenfamily"
)

var (
	// ErrCrossTenantViolation is returned when a cross-tenant data access violation is detected.
	ErrCrossTenantViolation = errors.New("redis: cross-tenant access violation detected")

	// ErrFamilyAlreadyExists is returned when attempting to create a family that already exists.
	ErrFamilyAlreadyExists = errors.New("redis: token family already exists")

	// ErrFamilyNotFound is returned when the requested family is not found in Redis.
	ErrFamilyNotFound = errors.New("redis: token family not found")

	// ErrCorruptedTokenData is returned when token metadata in Redis is missing required fields.
	ErrCorruptedTokenData = errors.New("redis: corrupted token data in storage")

	// ErrUnexpectedLuaResponse is returned when the Lua script returns an unparseable response.
	ErrUnexpectedLuaResponse = errors.New("redis: unexpected response format from lua script")
)

// MapStatusToError maps a Lua script status string to standard domain or adapter errors.
func MapStatusToError(status string) error {
	switch status {
	case "SUCCESS":
		return nil
	case "TOKEN_NOT_FOUND":
		return tokenfamily.ErrTokenNotFound
	case "TOKEN_EXPIRED":
		return tokenfamily.ErrTokenExpired
	case "TOKEN_REUSED":
		return tokenfamily.ErrTokenReused
	case "FAMILY_COMPROMISED":
		return tokenfamily.ErrFamilyCompromised
	case "FAMILY_REVOKED", "TOKEN_REVOKED":
		return tokenfamily.ErrFamilyRevoked
	case "CROSS_TENANT_VIOLATION":
		return ErrCrossTenantViolation
	case "FAMILY_ALREADY_EXISTS":
		return ErrFamilyAlreadyExists
	case "FAMILY_NOT_FOUND":
		return ErrFamilyNotFound
	case "CORRUPTED_TOKEN_DATA":
		return ErrCorruptedTokenData
	default:
		return fmt.Errorf("redis: unknown status from lua script: %s", status)
	}
}
