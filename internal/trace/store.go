package trace

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Store persists spans to a SQLite database.
type Store struct {
	db *sql.DB
}

// NewStore creates a Store backed by db and ensures the spans table exists.
func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	if err := s.ensureSchema(); err != nil {
		return nil, err
	}
	return s, nil
}

// SaveSpan writes a span to the persistent store.
func (s *Store) SaveSpan(sp *Span) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO trace_spans
		  (trace_id, span_id, parent_span_id, name, start_time, end_time, duration_ms, status, attrs)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		sp.TraceID, sp.SpanID, sp.ParentSpanID,
		sp.Operation,
		sp.StartTime.UTC().Format(time.RFC3339Nano),
		sp.EndTime.UTC().Format(time.RFC3339Nano),
		sp.EndTime.Sub(sp.StartTime).Milliseconds(),
		statusString(sp.Status),
		attrsJSON(sp.Attributes),
	)
	return err
}

// QueryFilter defines filtering options for span queries.
type QueryFilter struct {
	Name        string        // substring match on operation name
	MinDuration time.Duration // only spans >= this duration
	Limit       int           // 0 = 100
	Offset      int
}

// Query returns spans matching the filter, ordered by start_time DESC.
func (s *Store) Query(f QueryFilter) ([]*Span, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT trace_id, span_id, COALESCE(parent_span_id,''), name,
	             start_time, end_time, status
	      FROM trace_spans
	      WHERE 1=1`
	args := []interface{}{}
	if f.Name != "" {
		q += " AND name LIKE ?"
		args = append(args, "%"+f.Name+"%")
	}
	if f.MinDuration > 0 {
		q += " AND duration_ms >= ?"
		args = append(args, f.MinDuration.Milliseconds())
	}
	q += " ORDER BY start_time DESC LIMIT ? OFFSET ?"
	args = append(args, limit, f.Offset)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("trace.Store.Query: %w", err)
	}
	defer rows.Close()

	var spans []*Span
	for rows.Next() {
		var traceID, spanID, parentSpanID, operation, startStr, endStr, statusStr string
		if err := rows.Scan(&traceID, &spanID, &parentSpanID, &operation,
			&startStr, &endStr, &statusStr); err != nil {
			return nil, err
		}
		startTime, _ := time.Parse(time.RFC3339Nano, startStr)
		endTime, _ := time.Parse(time.RFC3339Nano, endStr)
		spans = append(spans, &Span{
			TraceID:      traceID,
			SpanID:       spanID,
			ParentSpanID: parentSpanID,
			Operation:    operation,
			StartTime:    startTime,
			EndTime:      endTime,
			Status:       parseStatus(statusStr),
		})
	}
	return spans, rows.Err()
}

func (s *Store) ensureSchema() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS trace_spans (
		trace_id       TEXT NOT NULL,
		span_id        TEXT PRIMARY KEY,
		parent_span_id TEXT,
		name           TEXT NOT NULL,
		start_time     TEXT NOT NULL,
		end_time       TEXT NOT NULL,
		duration_ms    INTEGER NOT NULL,
		status         TEXT NOT NULL DEFAULT 'ok',
		attrs          TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_trace_spans_name ON trace_spans(name);
	CREATE INDEX IF NOT EXISTS idx_trace_spans_start ON trace_spans(start_time);
	CREATE INDEX IF NOT EXISTS idx_trace_spans_duration ON trace_spans(duration_ms);
	CREATE INDEX IF NOT EXISTS idx_trace_spans_trace_id ON trace_spans(trace_id);`)
	return err
}

func attrsJSON(attrs map[string]string) string {
	if len(attrs) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(attrs)
	return string(b)
}

func parseStatus(s string) Status {
	switch s {
	case "ok":
		return StatusOK
	case "error":
		return StatusError
	default:
		return StatusUnset
	}
}
