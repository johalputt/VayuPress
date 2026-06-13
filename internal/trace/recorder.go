package trace

import (
	"encoding/json"
	"sync"
	"time"
)

const defaultRingSize = 2000

// SpanRecord is the serialisable snapshot of a finished span.
type SpanRecord struct {
	TraceID      string            `json:"trace_id"`
	SpanID       string            `json:"span_id"`
	ParentSpanID string            `json:"parent_span_id,omitempty"`
	Operation    string            `json:"operation"`
	StartTime    time.Time         `json:"start_time"`
	EndTime      time.Time         `json:"end_time"`
	DurationMS   int64             `json:"duration_ms"`
	Status       string            `json:"status"`
	ErrorMsg     string            `json:"error,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

func statusString(s Status) string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusError:
		return "error"
	default:
		return "unset"
	}
}

// Recorder stores finished spans in a fixed-size ring buffer. Safe for concurrent use.
type Recorder struct {
	mu   sync.Mutex
	ring []SpanRecord
	head int
	size int
}

// NewRecorder returns a Recorder with cap entries.
func NewRecorder(cap int) *Recorder {
	if cap <= 0 {
		cap = defaultRingSize
	}
	return &Recorder{ring: make([]SpanRecord, cap), size: cap}
}

func (r *Recorder) record(s *Span) {
	s.mu.Lock()
	rec := SpanRecord{
		TraceID:      s.TraceID,
		SpanID:       s.SpanID,
		ParentSpanID: s.ParentSpanID,
		Operation:    s.Operation,
		StartTime:    s.StartTime,
		EndTime:      s.EndTime,
		DurationMS:   s.DurationMS,
		Status:       statusString(s.Status),
		ErrorMsg:     s.ErrorMsg,
		Attributes:   s.Attributes,
	}
	s.mu.Unlock()

	r.mu.Lock()
	r.ring[r.head%r.size] = rec
	r.head++
	r.mu.Unlock()
}

// Recent returns up to n most-recent finished spans, newest first.
func (r *Recorder) Recent(n int) []SpanRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	total := r.head
	if total > r.size {
		total = r.size
	}
	if n > total {
		n = total
	}
	out := make([]SpanRecord, 0, n)
	for i := 0; i < n; i++ {
		idx := (r.head - 1 - i + r.size*2) % r.size
		out = append(out, r.ring[idx])
	}
	return out
}

// ByTraceID returns all recorded spans for the given trace ID.
func (r *Recorder) ByTraceID(traceID string) []SpanRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	total := r.head
	if total > r.size {
		total = r.size
	}
	var out []SpanRecord
	for i := 0; i < total; i++ {
		rec := r.ring[i]
		if rec.TraceID == traceID {
			out = append(out, rec)
		}
	}
	return out
}

// MarshalJSON serialises the recorder contents — for debug export.
func (r *Recorder) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.Recent(r.size))
}
