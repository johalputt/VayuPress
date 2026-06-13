package events

import (
	"encoding/json"
	"time"
)

// Envelope wraps every outbox event payload with identity and routing fields
// so consumers can deduplicate, version-route, and trace events (ADR-0052).
type Envelope struct {
	EventID       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	EventVersion  string          `json:"event_version"`
	CausationID   string          `json:"causation_id"`
	CorrelationID string          `json:"correlation_id"`
	OccurredAt    time.Time       `json:"occurred_at"`
	Payload       json.RawMessage `json:"payload"`
}
