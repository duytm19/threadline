package outbox

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/propagation"
)

// Event represents an atomic transactional outbox record stored in PostgreSQL.
type Event struct {
	ID            uuid.UUID         `json:"id"`
	AggregateType string            `json:"aggregate_type"`
	AggregateID   uuid.UUID         `json:"aggregate_id"`
	EventType     string            `json:"event_type"`
	Payload       json.RawMessage   `json:"payload"`
	Headers       map[string]string `json:"headers"`
	CreatedAt     time.Time         `json:"created_at"`
	PublishedAt   *time.Time        `json:"published_at,omitempty"`
}

// Writer defines the repository interface for appending outbox events in a DB transaction.
type Writer interface {
	WriteOutboxEvent(ctx context.Context, event Event) error
}

// NewEvent constructs an outbox event and automatically captures active W3C trace context.
func NewEvent(aggregateType string, aggregateID uuid.UUID, eventType string, payload any, ctx context.Context) (Event, error) {
	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New()
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}

	headers := make(map[string]string)
	if ctx != nil {
		// Propagate W3C traceparent and baggage into headers map per AD-9
		carrier := propagation.MapCarrier(headers)
		propagation.TraceContext{}.Inject(ctx, carrier)
		propagation.Baggage{}.Inject(ctx, carrier)
	}

	return Event{
		ID:            id,
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		EventType:     eventType,
		Payload:       payloadBytes,
		Headers:       headers,
		CreatedAt:     time.Now().UTC(),
	}, nil
}
