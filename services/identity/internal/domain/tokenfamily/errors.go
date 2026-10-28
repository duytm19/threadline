package tokenfamily

import "errors"

// Security alert identifier per OAuth 2.0 Security BCP and platform specifications.
const (
	// SecurityAlertTokenReuse is raised when an already-used refresh token is presented for exchange.
	SecurityAlertTokenReuse = "A07_TOKEN_REUSE"
)

var (
	// ErrEmptyFamilyID indicates the provided family ID is empty.
	ErrEmptyFamilyID = errors.New("tokenfamily: family_id must not be empty")

	// ErrEmptyTenantID indicates missing tenant context, violating fail-closed multi-tenancy.
	ErrEmptyTenantID = errors.New("tokenfamily: tenant_id must not be empty (fail-closed)")

	// ErrEmptyUserID indicates the user identifier is empty.
	ErrEmptyUserID = errors.New("tokenfamily: user_id must not be empty")

	// ErrEmptyTokenHash indicates the token SHA-256 hash is empty.
	ErrEmptyTokenHash = errors.New("tokenfamily: token_hash must not be empty")

	// ErrEmptyJTI indicates the JWT ID is empty.
	ErrEmptyJTI = errors.New("tokenfamily: jti must not be empty")

	// ErrInvalidExpiry indicates the expiration timestamp is not strictly in the future.
	ErrInvalidExpiry = errors.New("tokenfamily: expires_at must be in the future")

	// ErrFamilyCompromised indicates the token family was compromised by replay/reuse.
	ErrFamilyCompromised = errors.New("tokenfamily: token family is compromised")

	// ErrFamilyRevoked indicates the token family was explicitly revoked.
	ErrFamilyRevoked = errors.New("tokenfamily: token family has been revoked")

	// ErrTokenNotFound indicates the presented token is not associated with this family.
	ErrTokenNotFound = errors.New("tokenfamily: token not found in family")

	// ErrTokenReused indicates an already-rotated token was presented, triggering immediate compromise.
	ErrTokenReused = errors.New("tokenfamily: token reuse detected (security breach)")

	// ErrTokenExpired indicates the presented token has passed its expiration time.
	ErrTokenExpired = errors.New("tokenfamily: token has expired")

	// ErrTokenAlreadyUsed indicates the token is already in used state.
	ErrTokenAlreadyUsed = errors.New("tokenfamily: token already marked as used")

	// ErrTokenConflict indicates a token with the identical JTI or hash already exists in this family.
	ErrTokenConflict = errors.New("tokenfamily: token with given jti or hash already exists")
)
