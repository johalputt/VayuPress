package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/johalputt/vayupress/internal/events"
)

// openTestDB opens an in-memory SQLite DB with the minimal schema required by
// the outbox relay tests.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	stmts := []string{
		`CREATE TABLE event_outbox (id INTEGER PRIMARY KEY AUTOINCREMENT, event_type TEXT NOT NULL, payload TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','delivered','dead_letter')), retry_at DATETIME, retries INTEGER NOT NULL DEFAULT 0, dead_reason TEXT, created_at DATETIME NOT NULL DEFAULT (datetime('now')), delivered_at DATETIME)`,
		`CREATE TABLE delivered_events (event_id TEXT PRIMARY KEY, event_type TEXT NOT NULL, delivered_at DATETIME NOT NULL DEFAULT (datetime('now')))`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return db
}

func makeEnvelopePayload(t *testing.T, eventID, eventType string) []byte {
	t.Helper()
	inner, _ := json.Marshal(events.ArticleCreated{ID: "a1", Slug: "hello", Tags: []string{"x"}})
	env := events.Envelope{
		EventID:      eventID,
		EventType:    eventType,
		EventVersion: "1",
		OccurredAt:   time.Now().UTC(),
		Payload:      json.RawMessage(inner),
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return b
}

func insertOutbox(t *testing.T, db *sql.DB, eventType string, payload []byte) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO event_outbox(event_type, payload) VALUES(?, ?)`, eventType, payload)
	if err != nil {
		t.Fatalf("insert outbox: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// TestIdempotentDispatch_SkipsDuplicate verifies that a second outbox record with
// the same event_id is marked delivered without invoking the dispatch function.
func TestIdempotentDispatch_SkipsDuplicate(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	const eid = "test-event-id-001"
	payload := makeEnvelopePayload(t, eid, "article.created.v1")

	dispatchCount := 0
	relay := NewRelay(db, func(_ context.Context, _ string, _ []byte) error {
		dispatchCount++
		return nil
	}, make(chan struct{}))

	// First record — should be dispatched.
	insertOutbox(t, db, "article.created.v1", payload)
	relay.processOne()

	if dispatchCount != 1 {
		t.Fatalf("expected 1 dispatch after first record, got %d", dispatchCount)
	}

	// Verify it was recorded in delivered_events.
	var cnt int
	db.QueryRow(`SELECT COUNT(1) FROM delivered_events WHERE event_id=?`, eid).Scan(&cnt)
	if cnt != 1 {
		t.Fatalf("expected event_id in delivered_events, got count=%d", cnt)
	}

	// Second record with same event_id — should be skipped.
	insertOutbox(t, db, "article.created.v1", payload)
	relay.processOne()

	if dispatchCount != 1 {
		t.Fatalf("expected dispatch to remain 1 after duplicate, got %d", dispatchCount)
	}

	// Both outbox records should be delivered.
	var delivered int
	db.QueryRow(`SELECT COUNT(1) FROM event_outbox WHERE status='delivered'`).Scan(&delivered)
	if delivered != 2 {
		t.Fatalf("expected both outbox records delivered, got %d", delivered)
	}
}

// TestEnvelopeRoundtrip verifies that all fields survive a marshal/unmarshal cycle.
func TestEnvelopeRoundtrip(t *testing.T) {
	inner, _ := json.Marshal(events.ArticleCreated{ID: "id1", Slug: "slug1", Tags: []string{"a", "b"}})
	now := time.Now().UTC().Truncate(time.Second)

	orig := events.Envelope{
		EventID:       "eid-123",
		EventType:     "article.created.v1",
		EventVersion:  "1",
		CausationID:   "job-42",
		CorrelationID: "req-xyz",
		OccurredAt:    now,
		Payload:       json.RawMessage(inner),
	}

	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got events.Envelope
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.EventID != orig.EventID {
		t.Errorf("EventID: got %q want %q", got.EventID, orig.EventID)
	}
	if got.EventType != orig.EventType {
		t.Errorf("EventType: got %q want %q", got.EventType, orig.EventType)
	}
	if got.EventVersion != orig.EventVersion {
		t.Errorf("EventVersion: got %q want %q", got.EventVersion, orig.EventVersion)
	}
	if got.CausationID != orig.CausationID {
		t.Errorf("CausationID: got %q want %q", got.CausationID, orig.CausationID)
	}
	if got.CorrelationID != orig.CorrelationID {
		t.Errorf("CorrelationID: got %q want %q", got.CorrelationID, orig.CorrelationID)
	}
	if !got.OccurredAt.Equal(orig.OccurredAt) {
		t.Errorf("OccurredAt: got %v want %v", got.OccurredAt, orig.OccurredAt)
	}
	if string(got.Payload) != string(orig.Payload) {
		t.Errorf("Payload: got %s want %s", got.Payload, orig.Payload)
	}
}

// TestPoisonEventDeadLetters verifies that ErrPoisonEvent causes immediate dead-lettering.
func TestPoisonEventDeadLetters(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	payload := makeEnvelopePayload(t, "poison-id-001", "article.created.v1")
	insertOutbox(t, db, "article.created.v1", payload)

	relay := NewRelay(db, func(_ context.Context, _ string, _ []byte) error {
		return ErrPoisonEvent
	}, make(chan struct{}))

	relay.processOne()

	var status string
	db.QueryRow(`SELECT status FROM event_outbox WHERE id=1`).Scan(&status)
	if status != "dead_letter" {
		t.Fatalf("expected dead_letter, got %q", status)
	}

	var retries int
	db.QueryRow(`SELECT retries FROM event_outbox WHERE id=1`).Scan(&retries)
	if retries != 0 {
		t.Fatalf("expected retries=0 for poison event, got %d", retries)
	}
}

// Ensure ErrPoisonEvent is a distinct sentinel value.
func TestErrPoisonEventSentinel(t *testing.T) {
	if !errors.Is(ErrPoisonEvent, ErrPoisonEvent) {
		t.Fatal("ErrPoisonEvent should match itself")
	}
	if errors.Is(ErrPoisonEvent, errors.New("other")) {
		t.Fatal("ErrPoisonEvent should not match a different error")
	}
}
