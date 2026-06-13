package trace_test

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/johalputt/vayupress/internal/trace"
)

func TestStoreRoundTrip(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store, err := trace.NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Now()
	sp := trace.Span{
		TraceID:   "trace-1",
		SpanID:    "span-1",
		Operation: "testOp",
		StartTime: now,
		EndTime:   now.Add(50 * time.Millisecond),
		Status:    trace.StatusOK,
	}
	if err := store.SaveSpan(&sp); err != nil {
		t.Fatalf("SaveSpan: %v", err)
	}

	spans, err := store.Query(trace.QueryFilter{Name: "testOp"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(spans) != 1 {
		t.Errorf("expected 1 span, got %d", len(spans))
	}
}

func TestStoreMinDuration(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store, err := trace.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	for i, d := range []time.Duration{10, 100, 500} {
		sp := trace.Span{
			TraceID:   "trace-1",
			SpanID:    fmt.Sprintf("span-%d", i),
			Operation: "op",
			StartTime: now,
			EndTime:   now.Add(d * time.Millisecond),
			Status:    trace.StatusOK,
		}
		_ = store.SaveSpan(&sp)
	}

	spans, err := store.Query(trace.QueryFilter{MinDuration: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 2 {
		t.Errorf("expected 2 spans >= 100ms, got %d", len(spans))
	}
}
