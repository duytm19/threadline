package tokenfamily

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"time"
)

// HashToken computes a deterministic SHA-256 hash of a raw refresh token secret string.
func HashToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

// RefreshToken represents a token record in a token family.
type RefreshToken struct {
	jti       string
	tokenHash string
	state     TokenState
	issuedAt  time.Time
	expiresAt time.Time
	usedAt    *time.Time
}

// JTI returns the unique token identifier.
func (t RefreshToken) JTI() string {
	return t.jti
}

// TokenHash returns the SHA-256 hash of the token secret.
func (t RefreshToken) TokenHash() string {
	return t.tokenHash
}

// State returns the current token state.
func (t RefreshToken) State() TokenState {
	return t.state
}

// IssuedAt returns the token issuance timestamp.
func (t RefreshToken) IssuedAt() time.Time {
	return t.issuedAt
}

// ExpiresAt returns the token expiration timestamp.
func (t RefreshToken) ExpiresAt() time.Time {
	return t.expiresAt
}

// UsedAt returns the timestamp when this token was consumed during rotation, if applicable.
func (t RefreshToken) UsedAt() (time.Time, bool) {
	if t.usedAt == nil {
		return time.Time{}, false
	}
	return *t.usedAt, true
}

// IsActive returns true if the token is valid for exchange.
func (t RefreshToken) IsActive() bool {
	return t.state == TokenStateActive
}

// IsUsed returns true if the token has already been rotated.
func (t RefreshToken) IsUsed() bool {
	return t.state == TokenStateUsed
}

// IsExpired checks if the token has passed its expiration time at reference time now.
func (t RefreshToken) IsExpired(now time.Time) bool {
	return !t.expiresAt.After(now)
}

// ReconstituteRefreshToken restores a RefreshToken from persistence without validation.
func ReconstituteRefreshToken(
	jti string,
	tokenHash string,
	state TokenState,
	issuedAt time.Time,
	expiresAt time.Time,
	usedAt *time.Time,
) RefreshToken {
	return RefreshToken{
		jti:       jti,
		tokenHash: tokenHash,
		state:     state,
		issuedAt:  issuedAt,
		expiresAt: expiresAt,
		usedAt:    usedAt,
	}
}

// TokenFamily represents the aggregate root managing refresh token lifecycle and rotation invariants.
type TokenFamily struct {
	id               string
	tenantID         string
	userID           string
	state            State
	generation       int
	currentJTI       string
	currentHash      string
	tokens           []RefreshToken
	compromisedAt    *time.Time
	compromiseReason string
	createdAt        time.Time
	updatedAt        time.Time
	events           []DomainEvent
}

// NewTokenFamily initializes a fresh TokenFamily in StateActive with an initial refresh token.
func NewTokenFamily(
	id string,
	tenantID string,
	userID string,
	tokenHash string,
	jti string,
	expiresAt time.Time,
	now time.Time,
) (*TokenFamily, error) {
	if id == "" {
		return nil, ErrEmptyFamilyID
	}
	if tenantID == "" {
		return nil, ErrEmptyTenantID
	}
	if userID == "" {
		return nil, ErrEmptyUserID
	}
	if tokenHash == "" {
		return nil, ErrEmptyTokenHash
	}
	if jti == "" {
		return nil, ErrEmptyJTI
	}
	if !expiresAt.After(now) {
		return nil, ErrInvalidExpiry
	}

	initialToken := RefreshToken{
		jti:       jti,
		tokenHash: tokenHash,
		state:     TokenStateActive,
		issuedAt:  now,
		expiresAt: expiresAt,
	}

	family := &TokenFamily{
		id:          id,
		tenantID:    tenantID,
		userID:      userID,
		state:       StateActive,
		generation:  0,
		currentJTI:  jti,
		currentHash: tokenHash,
		tokens:      []RefreshToken{initialToken},
		createdAt:   now,
		updatedAt:   now,
		events: []DomainEvent{
			TokenFamilyCreatedEvent{
				FamilyID:   id,
				TenantID:   tenantID,
				UserID:     userID,
				InitialJTI: jti,
				CreatedAt:  now,
			},
		},
	}

	return family, nil
}

// ID returns the aggregate identifier.
func (f *TokenFamily) ID() string {
	return f.id
}

// TenantID returns the tenant identifier enforcing multi-tenant isolation.
func (f *TokenFamily) TenantID() string {
	return f.tenantID
}

// UserID returns the associated user identifier.
func (f *TokenFamily) UserID() string {
	return f.userID
}

// State returns the current lifecycle state of the family.
func (f *TokenFamily) State() State {
	return f.state
}

// Generation returns the rotation cycle counter.
func (f *TokenFamily) Generation() int {
	return f.generation
}

// CurrentJTI returns the JTI of the active refresh token.
func (f *TokenFamily) CurrentJTI() string {
	return f.currentJTI
}

// CurrentHash returns the SHA-256 hash of the active refresh token.
func (f *TokenFamily) CurrentHash() string {
	return f.currentHash
}

// Tokens returns a defensive copy of all tokens in this family.
func (f *TokenFamily) Tokens() []RefreshToken {
	out := make([]RefreshToken, len(f.tokens))
	copy(out, f.tokens)
	return out
}

// TokenByHash searches for a token matching the provided SHA-256 hash using constant-time comparison.
func (f *TokenFamily) TokenByHash(hash string) (RefreshToken, bool) {
	for _, tok := range f.tokens {
		if subtle.ConstantTimeCompare([]byte(tok.tokenHash), []byte(hash)) == 1 {
			return tok, true
		}
	}
	return RefreshToken{}, false
}

// TokenByJTI searches for a token matching the provided JTI.
func (f *TokenFamily) TokenByJTI(jti string) (RefreshToken, bool) {
	for _, tok := range f.tokens {
		if tok.jti == jti {
			return tok, true
		}
	}
	return RefreshToken{}, false
}

// CompromisedAt returns the timestamp when the family was compromised, if applicable.
func (f *TokenFamily) CompromisedAt() (time.Time, bool) {
	if f.compromisedAt == nil {
		return time.Time{}, false
	}
	return *f.compromisedAt, true
}

// CompromiseReason returns the explanation for family compromise.
func (f *TokenFamily) CompromiseReason() string {
	return f.compromiseReason
}

// CreatedAt returns the timestamp when the family was created.
func (f *TokenFamily) CreatedAt() time.Time {
	return f.createdAt
}

// UpdatedAt returns the timestamp of the last state change.
func (f *TokenFamily) UpdatedAt() time.Time {
	return f.updatedAt
}

// Events returns the recorded domain events.
func (f *TokenFamily) Events() []DomainEvent {
	out := make([]DomainEvent, len(f.events))
	copy(out, f.events)
	return out
}

// ClearEvents clears recorded domain events.
func (f *TokenFamily) ClearEvents() {
	f.events = nil
}

// Rotate exchanges an active refresh token for a newly generated token, enforcing rotation invariants.
// If an already-used token is presented, it flags active token reuse, permanently compromises the family,
// and revokes all active tokens.
func (f *TokenFamily) Rotate(
	presentedTokenHash string,
	newTokenHash string,
	newJTI string,
	newExpiresAt time.Time,
	now time.Time,
) (RefreshToken, error) {
	if f.state == StateCompromised {
		return RefreshToken{}, ErrFamilyCompromised
	}
	if f.state == StateRevoked {
		return RefreshToken{}, ErrFamilyRevoked
	}
	if newTokenHash == "" {
		return RefreshToken{}, ErrEmptyTokenHash
	}
	if newJTI == "" {
		return RefreshToken{}, ErrEmptyJTI
	}
	if !newExpiresAt.After(now) {
		return RefreshToken{}, ErrInvalidExpiry
	}

	var foundIdx = -1
	for i, tok := range f.tokens {
		if subtle.ConstantTimeCompare([]byte(tok.tokenHash), []byte(presentedTokenHash)) == 1 {
			foundIdx = i
			break
		}
	}

	if foundIdx == -1 {
		return RefreshToken{}, ErrTokenNotFound
	}

	presentedToken := &f.tokens[foundIdx]

	// Active token reuse detection: replaying a previously consumed token triggers immediate family compromise.
	if presentedToken.state == TokenStateUsed {
		f.markCompromised(
			presentedToken.jti,
			SecurityAlertTokenReuse,
			"A07_TOKEN_REUSE: used token presented for rotation",
			now,
		)
		return RefreshToken{}, ErrTokenReused
	}

	if presentedToken.IsExpired(now) {
		return RefreshToken{}, ErrTokenExpired
	}

	if presentedToken.state != TokenStateActive {
		return RefreshToken{}, ErrFamilyCompromised
	}

	// Validate collision prevention for new token
	for _, tok := range f.tokens {
		if tok.jti == newJTI || subtle.ConstantTimeCompare([]byte(tok.tokenHash), []byte(newTokenHash)) == 1 {
			return RefreshToken{}, ErrTokenConflict
		}
	}

	// Rotate presented token to USED
	usedTimestamp := now
	presentedToken.state = TokenStateUsed
	presentedToken.usedAt = &usedTimestamp

	// Register newly issued token
	newToken := RefreshToken{
		jti:       newJTI,
		tokenHash: newTokenHash,
		state:     TokenStateActive,
		issuedAt:  now,
		expiresAt: newExpiresAt,
	}
	f.tokens = append(f.tokens, newToken)

	oldJTI := f.currentJTI
	f.currentJTI = newJTI
	f.currentHash = newTokenHash
	f.generation++
	f.state = StateRotated
	f.updatedAt = now

	f.events = append(f.events, TokenFamilyRotatedEvent{
		FamilyID:    f.id,
		TenantID:    f.tenantID,
		UserID:      f.userID,
		OldTokenJTI: oldJTI,
		NewTokenJTI: newJTI,
		Generation:  f.generation,
		RotatedAt:   now,
	})

	return newToken, nil
}

// Compromise marks the family as compromised due to an external security signal or administrative action.
func (f *TokenFamily) Compromise(reason string, now time.Time) error {
	if f.state == StateCompromised {
		return nil
	}
	f.markCompromised("", "", reason, now)
	return nil
}

// Revoke terminates the token family upon normal user sign-out or session invalidation.
func (f *TokenFamily) Revoke(reason string, now time.Time) error {
	if f.state == StateCompromised {
		return ErrFamilyCompromised
	}
	if f.state == StateRevoked {
		return nil
	}

	f.state = StateRevoked
	f.updatedAt = now

	for i := range f.tokens {
		if f.tokens[i].state == TokenStateActive {
			f.tokens[i].state = TokenStateRevoked
		}
	}

	f.events = append(f.events, TokenFamilyRevokedEvent{
		FamilyID:  f.id,
		TenantID:  f.tenantID,
		UserID:    f.userID,
		Reason:    reason,
		RevokedAt: now,
	})

	return nil
}

func (f *TokenFamily) markCompromised(replayedJTI, alertCode, reason string, now time.Time) {
	f.state = StateCompromised
	f.compromisedAt = &now
	f.compromiseReason = reason
	f.updatedAt = now

	for i := range f.tokens {
		if f.tokens[i].state == TokenStateActive {
			f.tokens[i].state = TokenStateRevoked
		}
	}

	f.events = append(f.events, TokenFamilyCompromisedEvent{
		FamilyID:         f.id,
		TenantID:         f.tenantID,
		UserID:           f.userID,
		ReplayedTokenJTI: replayedJTI,
		AlertCode:        alertCode,
		Reason:           reason,
		CompromisedAt:    now,
	})
}

// ReconstituteTokenFamily reconstructs a TokenFamily from persistent storage without emitting domain events.
func ReconstituteTokenFamily(
	id string,
	tenantID string,
	userID string,
	state State,
	generation int,
	currentJTI string,
	currentHash string,
	tokens []RefreshToken,
	compromisedAt *time.Time,
	compromiseReason string,
	createdAt time.Time,
	updatedAt time.Time,
) (*TokenFamily, error) {
	if id == "" {
		return nil, ErrEmptyFamilyID
	}
	if tenantID == "" {
		return nil, ErrEmptyTenantID
	}
	if userID == "" {
		return nil, ErrEmptyUserID
	}

	tokensCopy := make([]RefreshToken, len(tokens))
	copy(tokensCopy, tokens)

	return &TokenFamily{
		id:               id,
		tenantID:         tenantID,
		userID:           userID,
		state:            state,
		generation:       generation,
		currentJTI:       currentJTI,
		currentHash:      currentHash,
		tokens:           tokensCopy,
		compromisedAt:    compromisedAt,
		compromiseReason: compromiseReason,
		createdAt:        createdAt,
		updatedAt:        updatedAt,
		events:           nil,
	}, nil
}
