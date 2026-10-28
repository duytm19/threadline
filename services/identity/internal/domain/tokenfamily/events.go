package tokenfamily

import "time"

// DomainEvent represents a pure domain event emitted by an aggregate root.
type DomainEvent interface {
	EventName() string
	OccurredAt() time.Time
}

// TokenFamilyCreatedEvent is emitted when a new token family is initialized.
type TokenFamilyCreatedEvent struct {
	FamilyID   string
	TenantID   string
	UserID     string
	InitialJTI string
	CreatedAt  time.Time
}

// EventName returns the domain event name.
func (e TokenFamilyCreatedEvent) EventName() string {
	return "identity.token_family.created"
}

// OccurredAt returns the event occurrence timestamp.
func (e TokenFamilyCreatedEvent) OccurredAt() time.Time {
	return e.CreatedAt
}

// TokenFamilyRotatedEvent is emitted when an active token is rotated to the next generation.
type TokenFamilyRotatedEvent struct {
	FamilyID    string
	TenantID    string
	UserID      string
	OldTokenJTI string
	NewTokenJTI string
	Generation  int
	RotatedAt   time.Time
}

// EventName returns the domain event name.
func (e TokenFamilyRotatedEvent) EventName() string {
	return "identity.token_family.rotated"
}

// OccurredAt returns the event occurrence timestamp.
func (e TokenFamilyRotatedEvent) OccurredAt() time.Time {
	return e.RotatedAt
}

// TokenFamilyCompromisedEvent is emitted when an intrusion or token reuse is detected.
type TokenFamilyCompromisedEvent struct {
	FamilyID         string
	TenantID         string
	UserID           string
	ReplayedTokenJTI string
	AlertCode        string
	Reason           string
	CompromisedAt    time.Time
}

// EventName returns the domain event name.
func (e TokenFamilyCompromisedEvent) EventName() string {
	return "identity.token_family.compromised"
}

// OccurredAt returns the event occurrence timestamp.
func (e TokenFamilyCompromisedEvent) OccurredAt() time.Time {
	return e.CompromisedAt
}

// TokenFamilyRevokedEvent is emitted when a token family is explicitly terminated.
type TokenFamilyRevokedEvent struct {
	FamilyID  string
	TenantID  string
	UserID    string
	Reason    string
	RevokedAt time.Time
}

// EventName returns the domain event name.
func (e TokenFamilyRevokedEvent) EventName() string {
	return "identity.token_family.revoked"
}

// OccurredAt returns the event occurrence timestamp.
func (e TokenFamilyRevokedEvent) OccurredAt() time.Time {
	return e.RevokedAt
}
