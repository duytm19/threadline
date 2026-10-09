package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrPayloadMismatch   = errors.New("idempotency: key reused with mismatched payload (hash conflict)")
	ErrOperationInFlight = errors.New("idempotency: concurrent request in progress")
	ErrInvalidKey        = errors.New("idempotency: invalid key format (must be valid UUID)")
)

// State represents the current lifecycle of an idempotent operation.
type State string

const (
	StateInProgress State = "IN_PROGRESS"
	StateCompleted  State = "COMPLETED"
)

// Record represents an idempotency entry stored in PostgreSQL.
type Record struct {
	Key            string          `json:"key"`
	TenantID       string          `json:"tenant_id"`
	RequestHash    string          `json:"request_hash"`
	State          State           `json:"state"`
	ResponseStatus int             `json:"response_status,omitempty"`
	ResponseBody   json.RawMessage `json:"response_body,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	ExpiresAt      time.Time       `json:"expires_at"`
}

// ComputeHash computes a deterministic SHA-256 hash of the request body.
func ComputeHash(payload []byte) string {
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}

// ValidateKey asserts that the idempotency key is a valid UUIDv7 or UUID.
func ValidateKey(key string) error {
	if _, err := uuid.Parse(key); err != nil {
		return ErrInvalidKey
	}
	return nil
}

// Storage is the repository interface required to manage idempotency records.
type Storage interface {
	// Acquire attempts to claim the key in StateInProgress.
	// Returns ErrOperationInFlight if currently in progress.
	// Returns ErrPayloadMismatch if completed or in-progress with different hash.
	// Returns existing completed record if available.
	Acquire(ctx context.Context, tenantID, key, requestHash string, ttl time.Duration) (*Record, error)

	// Complete commits the final response status and body for this key.
	Complete(ctx context.Context, tenantID, key string, statusCode int, responseBody []byte) error
}
