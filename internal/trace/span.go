package trace

import (
	"context"
	"sync"
	"time"
)

// Status mirrors the OpenTelemetry span status codes so spans are export-ready.
type Status int

const (
	StatusUnset Status = iota
	StatusOK
	StatusError
)

// Span represents a single timed operation within a trace (ADR-0054).
// Fields are named to match OpenTelemetry semantics for future export compatibility.
type Span struct {
	TraceID      string // = correlation_id of the originating request
	SpanID       string // unique per operation
	ParentSpanID string // empty for root spans
	Operation    string // e.g. "ArticleService.Create"
	StartTime    time.Time
	EndTime      time.Time
	DurationMS   int64
	Status       Status
	ErrorMsg     string
	Attributes   map[string]string // arbitrary key=value pairs
	mu           sync.Mutex
	ended        bool
	recorder     *Recorder
}

// SetAttribute attaches a key/value pair to the span. Safe to call concurrently.
func (s *Span) SetAttribute(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Attributes == nil {
		s.Attributes = make(map[string]string)
	}
	s.Attributes[key] = value
}

// SetError marks the span as errored and records the message.
func (s *Span) SetError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = StatusError
	s.ErrorMsg = err.Error()
}

// End finalises the span timing and records it into the Recorder. Idempotent.
func (s *Span) End() {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	s.EndTime = time.Now().UTC()
	s.DurationMS = s.EndTime.Sub(s.StartTime).Milliseconds()
	if s.Status == StatusUnset {
		s.Status = StatusOK
	}
	s.mu.Unlock()
	if s.recorder != nil {
		s.recorder.record(s)
	}
}

// contextKey types for span context storage.
type spanKey struct{}

// startSpan creates a child span under any span already in ctx.
// The new span is placed into the returned context.
func startSpan(ctx context.Context, operation string, rec *Recorder) (context.Context, *Span) {
	traceID := CorrelationID(ctx)
	if traceID == "" {
		traceID = NewID()
	}
	parentID := ""
	if parent, ok := ctx.Value(spanKey{}).(*Span); ok && parent != nil {
		parentID = parent.SpanID
	}
	s := &Span{
		TraceID:      traceID,
		SpanID:       NewID(),
		ParentSpanID: parentID,
		Operation:    operation,
		StartTime:    time.Now().UTC(),
		recorder:     rec,
	}
	return context.WithValue(ctx, spanKey{}, s), s
}

// CurrentSpan returns the innermost active span in ctx, or nil.
func CurrentSpan(ctx context.Context) *Span {
	s, _ := ctx.Value(spanKey{}).(*Span)
	return s
}
