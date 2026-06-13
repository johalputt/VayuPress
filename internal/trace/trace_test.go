package trace

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewID_Unique(t *testing.T) {
	a, b := NewID(), NewID()
	if a == b {
		t.Fatal("NewID must return distinct values")
	}
	if len(a) != 32 {
		t.Fatalf("NewID: want 32 hex chars, got %d", len(a))
	}
}

func TestCorrelationID_RoundTrip(t *testing.T) {
	ctx := context.Background()
	ctx = WithCorrelationID(ctx, "test-corr-1")
	if got := CorrelationID(ctx); got != "test-corr-1" {
		t.Fatalf("want test-corr-1, got %q", got)
	}
}

func TestCausationID_RoundTrip(t *testing.T) {
	ctx := WithCausationID(context.Background(), "cause-42")
	if got := CausationID(ctx); got != "cause-42" {
		t.Fatalf("want cause-42, got %q", got)
	}
}

func TestSpan_EndRecordsInRecorder(t *testing.T) {
	rec := NewRecorder(100)
	tr := &Tracer{Recorder: rec}
	ctx := WithCorrelationID(context.Background(), "trace-abc")
	ctx, span := tr.Start(ctx, "test.operation")
	span.SetAttribute("key", "val")
	span.End()

	recent := rec.Recent(10)
	if len(recent) != 1 {
		t.Fatalf("want 1 span, got %d", len(recent))
	}
	got := recent[0]
	if got.Operation != "test.operation" {
		t.Errorf("operation: want test.operation, got %q", got.Operation)
	}
	if got.TraceID != "trace-abc" {
		t.Errorf("trace_id: want trace-abc, got %q", got.TraceID)
	}
	if got.Status != "ok" {
		t.Errorf("status: want ok, got %q", got.Status)
	}
	if got.Attributes["key"] != "val" {
		t.Errorf("attribute key: want val, got %q", got.Attributes["key"])
	}
	if got.DurationMS < 0 {
		t.Error("duration_ms must be non-negative")
	}
	_ = ctx
}

func TestSpan_ErrorStatus(t *testing.T) {
	rec := NewRecorder(100)
	tr := &Tracer{Recorder: rec}
	_, span := tr.Start(context.Background(), "failing.op")
	span.SetError(errors.New("something broke"))
	span.End()

	recent := rec.Recent(1)
	if len(recent) == 0 {
		t.Fatal("no spans recorded")
	}
	got := recent[0]
	if got.Status != "error" {
		t.Errorf("want status=error, got %q", got.Status)
	}
	if got.ErrorMsg != "something broke" {
		t.Errorf("want error msg, got %q", got.ErrorMsg)
	}
}

func TestSpan_ParentChild(t *testing.T) {
	rec := NewRecorder(100)
	tr := &Tracer{Recorder: rec}
	ctx := WithCorrelationID(context.Background(), "parent-trace")

	ctx, parent := tr.Start(ctx, "parent.op")
	ctx, child := tr.Start(ctx, "child.op")
	child.End()
	parent.End()

	if child.ParentSpanID != parent.SpanID {
		t.Errorf("child.ParentSpanID=%q, want parent.SpanID=%q", child.ParentSpanID, parent.SpanID)
	}
	if child.TraceID != parent.TraceID {
		t.Errorf("child.TraceID=%q != parent.TraceID=%q", child.TraceID, parent.TraceID)
	}
	_ = ctx
}

func TestSpan_EndIdempotent(t *testing.T) {
	rec := NewRecorder(100)
	tr := &Tracer{Recorder: rec}
	_, span := tr.Start(context.Background(), "idempotent.op")
	span.End()
	firstEnd := span.EndTime
	time.Sleep(2 * time.Millisecond)
	span.End() // second End must be a no-op
	if !span.EndTime.Equal(firstEnd) {
		t.Error("second End() must not change EndTime")
	}
	if len(rec.Recent(10)) != 1 {
		t.Error("second End() must not add a second record")
	}
}

func TestRecorder_RingBuffer(t *testing.T) {
	rec := NewRecorder(3)
	tr := &Tracer{Recorder: rec}
	for i := 0; i < 5; i++ {
		_, s := tr.Start(context.Background(), "op")
		s.End()
	}
	// Only last 3 should be kept
	got := rec.Recent(10)
	if len(got) != 3 {
		t.Fatalf("ring buffer cap=3, recorded 5; want 3 recent, got %d", len(got))
	}
}

func TestRecorder_ByTraceID(t *testing.T) {
	rec := NewRecorder(100)
	tr := &Tracer{Recorder: rec}

	ctxA := WithCorrelationID(context.Background(), "trace-A")
	ctxB := WithCorrelationID(context.Background(), "trace-B")

	_, s1 := tr.Start(ctxA, "op1")
	s1.End()
	_, s2 := tr.Start(ctxB, "op2")
	s2.End()
	_, s3 := tr.Start(ctxA, "op3")
	s3.End()

	byA := rec.ByTraceID("trace-A")
	if len(byA) != 2 {
		t.Fatalf("want 2 spans for trace-A, got %d", len(byA))
	}
	byB := rec.ByTraceID("trace-B")
	if len(byB) != 1 {
		t.Fatalf("want 1 span for trace-B, got %d", len(byB))
	}
}

func TestCurrentSpan(t *testing.T) {
	ctx := context.Background()
	if s := CurrentSpan(ctx); s != nil {
		t.Fatal("want nil span in empty context")
	}
	rec := NewRecorder(10)
	tr := &Tracer{Recorder: rec}
	ctx, span := tr.Start(ctx, "my.op")
	if got := CurrentSpan(ctx); got != span {
		t.Error("CurrentSpan must return the active span")
	}
}
