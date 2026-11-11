package ports

import (
	"context"
	"time"
)

// AuditSeverity indicates the operational/security criticality of an audit event.
type AuditSeverity string

const (
	SeverityInfo     AuditSeverity = "INFO"
	SeverityWarn     AuditSeverity = "WARN"
	SeverityCritical AuditSeverity = "CRITICAL"
)

// AuditOutcome represents the final disposition of a token refresh attempt.
type AuditOutcome string

const (
	OutcomeSuccess       AuditOutcome = "SUCCESS"
	OutcomeReuseDetected AuditOutcome = "REUSE_DETECTED"
	OutcomeDenied        AuditOutcome = "DENIED"
	OutcomeFailed        AuditOutcome = "FAILED"
)

// GeoHint carries optional geographic attributes supplied by edge gateways or CDNs.
type GeoHint struct {
	Country   string
	Region    string
	City      string
	Latitude  *float64
	Longitude *float64
	RawGeo    string
}

// AuditEvent captures an immutable security audit record for token refresh operations.
type AuditEvent struct {
	EventID     string
	EventType   string
	Severity    AuditSeverity
	AlertCode   string
	TenantID    string
	UserID      string
	FamilyID    string
	OldTokenJTI string
	NewTokenJTI string
	Generation  int
	Outcome     AuditOutcome
	Reason      string
	ClientIP    string
	UserAgent   string
	GeoHash     string
	TraceID     string
	RequestID   string
	OccurredAt  time.Time
}

// AuditLogger defines the driven port for recording immutable refresh audit events.
type AuditLogger interface {
	LogRefreshAudit(ctx context.Context, event AuditEvent) error
}

// GeoResolver defines the driven port for computing a privacy-preserving geographic hash.
type GeoResolver interface {
	ResolveGeoHash(ctx context.Context, clientIP string, hint GeoHint) string
}
