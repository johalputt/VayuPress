package policy

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"time"
)

// GlobalJournal is the process-wide policy journal, set by main after DB init.
var GlobalJournal *Journal

// EvalRow is a single policy evaluation row as stored in SQLite.
type EvalRow struct {
	ID                  int64
	RunID               string
	PolicyName          string
	Category            string
	Severity            string
	Result              string // "pass", "warn", "fail"
	Detail              string
	TriggeredTransition bool
	EvaluatedAt         time.Time
}

// Journal persists policy evaluation runs to a SQLite database.
type Journal struct {
	db *sql.DB
}

// NewJournal returns a Journal backed by db.
func NewJournal(db *sql.DB) *Journal {
	return &Journal{db: db}
}

// Record writes all results from a single evaluation run to the DB.
func (j *Journal) Record(report EvaluationReport) (runID string, err error) {
	b := make([]byte, 8)
	rand.Read(b) //nolint:errcheck
	runID = fmt.Sprintf("%x", b)
	tx, err := j.db.Begin()
	if err != nil {
		return "", fmt.Errorf("policy journal: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback() //nolint:errcheck
		}
	}()
	stmt, err := tx.Prepare(`INSERT INTO policy_evaluations(run_id,policy_name,category,severity,result,detail) VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return "", fmt.Errorf("policy journal: prepare: %w", err)
	}
	defer stmt.Close()
	for _, r := range report.Passed {
		if _, err = stmt.Exec(runID, r.Name, string(r.Category), string(r.Severity), "pass", r.Message); err != nil {
			return "", fmt.Errorf("policy journal: insert pass: %w", err)
		}
	}
	for _, r := range report.Warnings {
		if _, err = stmt.Exec(runID, r.Name, string(r.Category), string(r.Severity), "warn", r.Message); err != nil {
			return "", fmt.Errorf("policy journal: insert warn: %w", err)
		}
	}
	for _, r := range report.Failed {
		if _, err = stmt.Exec(runID, r.Name, string(r.Category), string(r.Severity), "fail", r.Message); err != nil {
			return "", fmt.Errorf("policy journal: insert fail: %w", err)
		}
	}
	return runID, tx.Commit()
}

// RecentRuns returns the last n distinct run IDs ordered by most recent.
func (j *Journal) RecentRuns(n int) ([]string, error) {
	rows, err := j.db.Query(
		`SELECT DISTINCT run_id FROM policy_evaluations ORDER BY evaluated_at DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// History returns the last limit evaluation rows ordered by most recent first.
func (j *Journal) History(limit int) ([]EvalRow, error) {
	rows, err := j.db.Query(
		`SELECT id,run_id,policy_name,category,severity,result,detail,triggered_transition,evaluated_at
		   FROM policy_evaluations ORDER BY evaluated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EvalRow
	for rows.Next() {
		var r EvalRow
		var ts string
		var trig int
		if err := rows.Scan(&r.ID, &r.RunID, &r.PolicyName, &r.Category, &r.Severity, &r.Result, &r.Detail, &trig, &ts); err != nil {
			return nil, err
		}
		r.TriggeredTransition = trig == 1
		r.EvaluatedAt, _ = time.Parse("2006-01-02T15:04:05Z", ts)
		if r.EvaluatedAt.IsZero() {
			r.EvaluatedAt, _ = time.Parse("2006-01-02 15:04:05", ts)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RunSummary holds pass/warn/fail counts for a single run.
type RunSummary struct {
	RunID       string
	EvaluatedAt time.Time
	Pass        int
	Warn        int
	Fail        int
}

// RunHistory returns summarized per-run statistics for the last n runs.
func (j *Journal) RunHistory(n int) ([]RunSummary, error) {
	rows, err := j.db.Query(`
		SELECT run_id, MAX(evaluated_at),
		       SUM(CASE WHEN result='pass' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN result='warn' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN result='fail' THEN 1 ELSE 0 END)
		FROM policy_evaluations
		GROUP BY run_id
		ORDER BY MAX(evaluated_at) DESC
		LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunSummary
	for rows.Next() {
		var s RunSummary
		var ts string
		if err := rows.Scan(&s.RunID, &ts, &s.Pass, &s.Warn, &s.Fail); err != nil {
			return nil, err
		}
		s.EvaluatedAt, _ = time.Parse("2006-01-02T15:04:05Z", ts)
		if s.EvaluatedAt.IsZero() {
			s.EvaluatedAt, _ = time.Parse("2006-01-02 15:04:05", ts)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
