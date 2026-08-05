// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RunStatus is where a run ended up.
type RunStatus string

const (
	// StatusRunning — in flight. A row left in this state by a process that
	// died is what RecoverInterrupted converts.
	StatusRunning RunStatus = "running"
	// StatusSucceeded — every step completed.
	StatusSucceeded RunStatus = "succeeded"
	// StatusFailed — a step returned an error and the run short-circuited.
	StatusFailed RunStatus = "failed"
	// StatusRefused — the run never executed: the condition did not hold, the
	// owner no longer held the authority its actions require, or a rate ceiling
	// was already spent. A refusal is a normal outcome and is recorded as one,
	// because a refusal that looked like a failure would train an operator to
	// ignore failures.
	StatusRefused RunStatus = "refused"
	// StatusInterrupted — the process died mid-flight. NOT a failure and NOT a
	// success, and never retried automatically: a step that already sent mail
	// must not be replayed by a ticker. Retry is an operator decision.
	StatusInterrupted RunStatus = "interrupted"
)

// StepRecord is one step's execution, kept so a run can be replayed by a reader
// even when it cannot be replayed by the engine.
type StepRecord struct {
	Action   string            `json:"action"`
	Params   map[string]string `json:"params,omitempty"`
	Output   string            `json:"output,omitempty"`
	Error    string            `json:"error,omitempty"`
	Duration string            `json:"duration"`
	// Refused records an effect the capability boundary declined — in a dry run
	// this is the normal case, and it is what the diff view renders.
	Refused string `json:"refused,omitempty"`
	// Did records what the step actually performed that was not refused. A model
	// call is the case this exists for: a dry run really does generate, and a
	// reader needs to see that rather than assume it was stubbed.
	Did string `json:"did,omitempty"`
}

// Run is one execution of one flow, at one version, with one cause.
type Run struct {
	ID          string
	FlowID      string
	FlowVersion int
	IdemKey     string
	Cause       string
	Mode        RunMode
	Status      RunStatus
	Owner       string
	OwnerRole   string
	Budget      Budget
	Spend       Spend
	Steps       []StepRecord
	Error       string
	StartedAt   time.Time
	FinishedAt  time.Time
}

// ErrDuplicateRun is returned when a run with the same idempotency key already
// exists. It is not an error condition in the ordinary sense — it is the
// mechanism working, and callers treat it as "already handled".
var ErrDuplicateRun = errors.New("vayuflow: a run with this idempotency key already exists")

// isDuplicate reports whether err is the idempotency collision. It exists so
// callers do not each reach for errors.Is and get it subtly wrong.
func isDuplicate(err error) bool { return errors.Is(err, ErrDuplicateRun) }

// RunStore persists the run trail.
type RunStore struct{ db *sql.DB }

// NewRunStore creates a RunStore.
func NewRunStore(db *sql.DB) *RunStore { return &RunStore{db: db} }

// IdempotencyKey derives the key for one (flow, trigger identity) pair.
//
// The identity is supplied by the trigger rather than invented here: a schedule
// trigger uses its fire time truncated to the minute, an event trigger uses its
// inbox row ID, a manual trigger uses a fresh unique token because pressing Run
// twice IS two runs. Getting this wrong in either direction is bad — too coarse
// and a legitimate second run is swallowed, too fine and redelivery duplicates.
func IdempotencyKey(flowID string, version int, identity string) string {
	return fmt.Sprintf("%s@%d:%s", flowID, version, identity)
}

// Begin records a run as started, and is the choke point that makes redelivery
// safe. It relies on the UNIQUE index rather than a check-then-insert, which
// would be a race with a window: two runners, or one runner and a retry, can
// both read "no such run" before either writes.
func (rs *RunStore) Begin(ctx context.Context, f Flow, cause, identity, ownerRole string) (*Run, error) {
	r := &Run{
		ID:          newID(),
		FlowID:      f.ID,
		FlowVersion: f.Version,
		IdemKey:     IdempotencyKey(f.ID, f.Version, identity),
		Cause:       cause,
		Mode:        f.Mode,
		Status:      StatusRunning,
		Owner:       f.Owner,
		OwnerRole:   ownerRole,
		Budget:      f.Budget,
		StartedAt:   time.Now().UTC(),
	}
	_, err := rs.db.ExecContext(ctx,
		`INSERT INTO vayuflow_runs(id,flow_id,flow_version,idempotency_key,trigger_cause,mode,status,owner,owner_role,budget_max_steps,budget_max_writes,budget_max_egress,started_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.FlowID, r.FlowVersion, r.IdemKey, r.Cause, r.Mode.String(), string(r.Status),
		r.Owner, r.OwnerRole, r.Budget.MaxStepsPerRun, r.Budget.MaxWritesPerRun, r.Budget.MaxEgressPerRun,
		r.StartedAt.Format(tsLayout))
	if err != nil {
		// SQLite reports a UNIQUE violation by message. Matching on the message
		// is unpleasant but the alternative — a driver-specific error type —
		// would tie this package to the driver, and a wrong guess here fails
		// CLOSED: an unrecognised error is returned as itself, so the run does
		// not proceed.
		if isUniqueViolation(err) {
			return nil, ErrDuplicateRun
		}
		return nil, fmt.Errorf("vayuflow: begin run: %w", err)
	}
	return r, nil
}

func isUniqueViolation(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique constraint failed") || strings.Contains(s, "constraint failed: unique")
}

// Finish writes the terminal state of a run.
func (rs *RunStore) Finish(ctx context.Context, r *Run) error {
	if r.Status == StatusRunning {
		return fmt.Errorf("vayuflow: refusing to finish run %s while its status is still %q", r.ID, r.Status)
	}
	steps, err := json.Marshal(r.Steps)
	if err != nil {
		return fmt.Errorf("vayuflow: encode step records: %w", err)
	}
	r.FinishedAt = time.Now().UTC()
	_, err = rs.db.ExecContext(ctx,
		`UPDATE vayuflow_runs SET status=?,spend_steps=?,spend_writes=?,spend_egress=?,steps_json=?,error=?,finished_at=? WHERE id=?`,
		string(r.Status), r.Spend.Steps, r.Spend.Writes, r.Spend.Egress, string(steps),
		r.Error, r.FinishedAt.Format(tsLayout), r.ID)
	if err != nil {
		return fmt.Errorf("vayuflow: finish run: %w", err)
	}
	return nil
}

// RecoverInterrupted converts every run still marked running into interrupted,
// and returns how many. It is called once at startup.
//
// It does NOT retry them. A run that died after its third step may have already
// sent mail, and there is no way from here to know; replaying it is the one
// thing that turns a crash into a duplicate delivery. The operator gets the
// fact and the decision.
func (rs *RunStore) RecoverInterrupted(ctx context.Context) (int, error) {
	res, err := rs.db.ExecContext(ctx,
		`UPDATE vayuflow_runs SET status=?,finished_at=?,error=? WHERE status=?`,
		string(StatusInterrupted), time.Now().UTC().Format(tsLayout),
		"the process ended while this run was in flight; it was not retried automatically",
		string(StatusRunning))
	if err != nil {
		return 0, fmt.Errorf("vayuflow: recover interrupted runs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return int(n), nil
}

const runCols = `id,flow_id,flow_version,idempotency_key,trigger_cause,mode,status,owner,owner_role,budget_max_steps,budget_max_writes,budget_max_egress,spend_steps,spend_writes,spend_egress,steps_json,error,started_at,finished_at`

func (rs *RunStore) scanRun(rows *sql.Rows) (Run, error) {
	var (
		r                     Run
		mode, status          string
		stepsJSON             string
		startedAt, finishedAt string
	)
	if err := rows.Scan(&r.ID, &r.FlowID, &r.FlowVersion, &r.IdemKey, &r.Cause, &mode, &status,
		&r.Owner, &r.OwnerRole, &r.Budget.MaxStepsPerRun, &r.Budget.MaxWritesPerRun, &r.Budget.MaxEgressPerRun,
		&r.Spend.Steps, &r.Spend.Writes, &r.Spend.Egress, &stepsJSON, &r.Error,
		&startedAt, &finishedAt); err != nil {
		return Run{}, err
	}
	r.Mode = parseRunMode(mode)
	r.Status = RunStatus(status)
	if stepsJSON != "" {
		if err := json.Unmarshal([]byte(stepsJSON), &r.Steps); err != nil {
			return Run{}, fmt.Errorf("vayuflow: decode step records for run %q: %w", r.ID, err)
		}
	}
	r.StartedAt, _ = time.Parse(tsLayout, startedAt)
	if finishedAt != "" {
		r.FinishedAt, _ = time.Parse(tsLayout, finishedAt)
	}
	return r, nil
}

// Recent returns the newest runs, optionally for one flow.
func (rs *RunStore) Recent(ctx context.Context, flowID string, limit int) ([]Run, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `SELECT ` + runCols + ` FROM vayuflow_runs`
	args := []any{}
	if flowID != "" {
		q += ` WHERE flow_id=?`
		args = append(args, flowID)
	}
	// rowid, not id, as the tiebreaker. Run IDs are random hex, so ordering by
	// them within a second is arbitrary — two runs started in the same second
	// would appear in the trail in a shuffled order, which is exactly when an
	// operator is trying to work out which happened first. rowid is SQLite's
	// insertion order and is monotonic.
	q += ` ORDER BY started_at DESC, rowid DESC LIMIT ?`
	args = append(args, limit)

	rows, err := rs.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("vayuflow: recent runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Run
	for rows.Next() {
		r, err := rs.scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	_ = rows.Err()
	return out, nil
}

// Stats is the window summary the panel's tiles read.
type Stats struct {
	Runs int
	// Refused counts runs the engine declined — a condition that did not hold,
	// an owner who no longer holds the authority, a step that could not run.
	Refused int
	// BudgetCapped counts runs that reached a ceiling. A ceiling reached is a
	// ceiling doing its job OR a ceiling set wrong, and either wants a human —
	// which is why this tile is toned for attention rather than reported as a
	// success.
	BudgetCapped int
	Interrupted  int
	Failed       int
}

// StatsSince summarises the run trail over a window.
func (rs *RunStore) StatsSince(ctx context.Context, since time.Time) (Stats, error) {
	var st Stats
	rows, err := rs.db.QueryContext(ctx,
		`SELECT status, error, spend_steps, spend_writes, spend_egress, budget_max_steps, budget_max_writes, budget_max_egress FROM vayuflow_runs WHERE started_at>=?`,
		since.UTC().Format(tsLayout))
	if err != nil {
		return st, fmt.Errorf("vayuflow: run stats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			status, errText        string
			ss, sw, se, bs, bw, be int
		)
		if err := rows.Scan(&status, &errText, &ss, &sw, &se, &bs, &bw, &be); err != nil {
			return st, err
		}
		st.Runs++
		switch RunStatus(status) {
		case StatusRefused:
			st.Refused++
		case StatusInterrupted:
			st.Interrupted++
		case StatusFailed:
			st.Failed++
		}
		// A run that reached ANY ceiling counts, whether it failed on it or
		// merely finished level with it — the point of the tile is "this flow is
		// operating at its limit", which is true either way.
		if ss >= bs || sw >= bw || se >= be {
			st.BudgetCapped++
		}
	}
	_ = rows.Err()
	return st, nil
}

// CountSince returns how many runs a flow started since t, which is what the
// MaxRunsPerHour ceiling is checked against.
//
// It counts runs REGARDLESS of outcome, deliberately. A trigger storm that
// produces a thousand refusals is still a thousand runs of work, and a ceiling
// that only counted successes would let the storm through.
func (rs *RunStore) CountSince(ctx context.Context, flowID string, since time.Time) (int, error) {
	var n int
	err := rs.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vayuflow_runs WHERE flow_id=? AND started_at>=?`,
		flowID, since.UTC().Format(tsLayout)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("vayuflow: count runs: %w", err)
	}
	return n, nil
}

// runRetention is how long a run stays in the trail.
//
// ADR-0151 §7 promises the trail is bounded. The runs-per-hour ceiling bounds
// the RATE — at most MaxRunsPerHour rows per flow per hour — which is what stops
// a trigger storm growing the table, and it was mistaken for a bound on the
// total. It is not one: ten runs an hour is eighty-seven thousand rows a year,
// per flow, kept forever.
//
// Ninety days rather than the inbox's thirty. The two tables answer different
// questions: the inbox is a queue whose rows stop mattering once drained, and
// the run trail is the record of what this install did on its own, which is
// exactly what someone goes looking for months later when they notice something
// odd.
const runRetention = 90 * 24 * time.Hour

// Prune deletes finished runs older than the retention window.
//
// Only FINISHED runs. A row still marked running is either a live run or one
// interrupted by a crash that RecoverInterrupted has not reconciled yet, and
// deleting either would lose the very evidence the trail exists for.
func (rs *RunStore) Prune(ctx context.Context, olderThan time.Duration) (int, error) {
	res, err := rs.db.ExecContext(ctx,
		`DELETE FROM vayuflow_runs WHERE status != ? AND started_at < ?`,
		string(StatusRunning), time.Now().UTC().Add(-olderThan).Format(tsLayout))
	if err != nil {
		return 0, fmt.Errorf("vayuflow: prune runs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return int(n), nil
}
