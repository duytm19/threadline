package tokenfamily

// State represents the lifecycle state of a TokenFamily aggregate root.
type State string

const (
	// StateActive indicates the family is newly created and holding its initial active token.
	StateActive State = "ACTIVE"

	// StateRotated indicates the family has undergone at least one legitimate token rotation.
	StateRotated State = "ROTATED"

	// StateCompromised indicates active token reuse or replay was detected; all tokens are permanently invalidated.
	StateCompromised State = "COMPROMISED"

	// StateRevoked indicates the family was explicitly terminated (e.g., user logout).
	StateRevoked State = "REVOKED"
)

// String returns the string representation of State.
func (s State) String() string {
	return string(s)
}

// IsActive returns true if the family is in StateActive.
func (s State) IsActive() bool {
	return s == StateActive
}

// IsRotated returns true if the family is in StateRotated.
func (s State) IsRotated() bool {
	return s == StateRotated
}

// IsCompromised returns true if the family has been flagged as compromised.
func (s State) IsCompromised() bool {
	return s == StateCompromised
}

// IsRevoked returns true if the family was revoked.
func (s State) IsRevoked() bool {
	return s == StateRevoked
}

// IsValid validates whether the state is an approved state machine value.
func (s State) IsValid() bool {
	switch s {
	case StateActive, StateRotated, StateCompromised, StateRevoked:
		return true
	default:
		return false
	}
}

// TokenState represents the lifecycle state of an individual refresh token within a family.
type TokenState string

const (
	// TokenStateActive indicates the token is the currently valid, unconsumed refresh token.
	TokenStateActive TokenState = "ACTIVE"

	// TokenStateUsed indicates the token has already been rotated; any presentation signals reuse.
	TokenStateUsed TokenState = "USED"

	// TokenStateRevoked indicates the token was invalidated due to revocation or family compromise.
	TokenStateRevoked TokenState = "REVOKED"
)

// String returns the string representation of TokenState.
func (s TokenState) String() string {
	return string(s)
}

// IsValid checks if TokenState is valid.
func (s TokenState) IsValid() bool {
	switch s {
	case TokenStateActive, TokenStateUsed, TokenStateRevoked:
		return true
	default:
		return false
	}
}
