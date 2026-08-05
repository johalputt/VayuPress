// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Store persists flows in SQLite.
type Store struct{ db *sql.DB }

// NewStore creates a Store.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A flow ID that is not unique would let one flow's runs be attributed
		// to another in the audit trail, so there is no sensible fallback here.
		panic("vayuflow: system entropy unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

const tsLayout = "2006-01-02 15:04:05"

// Save writes a flow, refusing anything Complete() rejects.
//
// The refusal lives HERE, at the storage boundary, and not only in the handler.
// A handler-only check is one new call site away from being bypassed, and the
// invariant this package exists to hold — that no stored flow has an undeclared
// blast radius — has to be true of the table, not of one code path into it.
func (s *Store) Save(ctx context.Context, f *Flow) error {
	if err := f.Complete(); err != nil {
		return err
	}
	trigger := f.Trigger
	cond, err := json.Marshal(f.Condition)
	if err != nil {
		return fmt.Errorf("vayuflow: encode condition: %w", err)
	}
	steps, err := json.Marshal(f.Steps)
	if err != nil {
		return fmt.Errorf("vayuflow: encode steps: %w", err)
	}
	now := time.Now().UTC().Format(tsLayout)
	enabled := 0
	if f.Enabled {
		enabled = 1
	}
	if f.ID == "" {
		f.ID, f.Version = newID(), 1
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO vayuflow_flows(id,name,enabled,trigger_kind,trigger_cron,trigger_event,condition_json,steps_json,budget_max_steps,budget_max_runs_hour,budget_max_writes,budget_max_egress,budget_timeout_ms,owner,mode,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			f.ID, f.Name, enabled, trigger.Kind.String(), trigger.Cron, trigger.Event,
			string(cond), string(steps),
			f.Budget.MaxStepsPerRun, f.Budget.MaxRunsPerHour, f.Budget.MaxWritesPerRun,
			f.Budget.MaxEgressPerRun, f.Budget.Timeout.Milliseconds(),
			f.Owner, f.Mode.String(), f.Version, now, now)
		if err != nil {
			return fmt.Errorf("vayuflow: insert flow: %w", err)
		}
		return nil
	}
	// Every edit bumps the version, in the same statement that writes the edit,
	// so a run recorded against version N can always be read against the
	// document as it was at N.
	f.Version++
	res, err := s.db.ExecContext(ctx,
		`UPDATE vayuflow_flows SET name=?,enabled=?,trigger_kind=?,trigger_cron=?,trigger_event=?,condition_json=?,steps_json=?,budget_max_steps=?,budget_max_runs_hour=?,budget_max_writes=?,budget_max_egress=?,budget_timeout_ms=?,owner=?,mode=?,version=?,updated_at=? WHERE id=?`,
		f.Name, enabled, trigger.Kind.String(), trigger.Cron, trigger.Event,
		string(cond), string(steps),
		f.Budget.MaxStepsPerRun, f.Budget.MaxRunsPerHour, f.Budget.MaxWritesPerRun,
		f.Budget.MaxEgressPerRun, f.Budget.Timeout.Milliseconds(),
		f.Owner, f.Mode.String(), f.Version, now, f.ID)
	if err != nil {
		return fmt.Errorf("vayuflow: update flow: %w", err)
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return fmt.Errorf("vayuflow: no flow with id %q", f.ID)
	}
	return nil
}

func parseTriggerKind(s string) TriggerKind {
	switch s {
	case "schedule":
		return TriggerSchedule
	case "event":
		return TriggerEvent
	case "manual":
		return TriggerManual
	}
	return triggerUnset
}

func parseRunMode(s string) RunMode {
	switch s {
	case "dry-run":
		return RunDryRun
	case "live":
		return RunLive
	}
	return runUnset
}

func (s *Store) scan(rows *sql.Rows) (Flow, error) {
	var (
		f                    Flow
		enabled              int
		tKind, tCron, tEvent string
		condJSON, stepsJSON  string
		timeoutMS            int64
		mode                 string
		createdAt, updatedAt string
	)
	if err := rows.Scan(&f.ID, &f.Name, &enabled, &tKind, &tCron, &tEvent, &condJSON, &stepsJSON,
		&f.Budget.MaxStepsPerRun, &f.Budget.MaxRunsPerHour, &f.Budget.MaxWritesPerRun,
		&f.Budget.MaxEgressPerRun, &timeoutMS, &f.Owner, &mode, &f.Version,
		&createdAt, &updatedAt); err != nil {
		return Flow{}, err
	}
	f.Enabled = enabled == 1
	f.Trigger = Trigger{Kind: parseTriggerKind(tKind), Cron: tCron, Event: tEvent}
	f.Budget.Timeout = time.Duration(timeoutMS) * time.Millisecond
	f.Mode = parseRunMode(mode)
	if condJSON != "" {
		if err := json.Unmarshal([]byte(condJSON), &f.Condition); err != nil {
			return Flow{}, fmt.Errorf("vayuflow: decode condition for %q: %w", f.ID, err)
		}
	}
	if err := json.Unmarshal([]byte(stepsJSON), &f.Steps); err != nil {
		return Flow{}, fmt.Errorf("vayuflow: decode steps for %q: %w", f.ID, err)
	}
	return f, nil
}

const selectCols = `id,name,enabled,trigger_kind,trigger_cron,trigger_event,condition_json,steps_json,budget_max_steps,budget_max_runs_hour,budget_max_writes,budget_max_egress,budget_timeout_ms,owner,mode,version,created_at,updated_at`

// Get returns one flow by ID.
func (s *Store) Get(ctx context.Context, id string) (Flow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+selectCols+` FROM vayuflow_flows WHERE id=?`, id)
	if err != nil {
		return Flow{}, fmt.Errorf("vayuflow: get flow: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		_ = rows.Err()
		return Flow{}, fmt.Errorf("vayuflow: no flow with id %q", id)
	}
	f, err := s.scan(rows)
	if err != nil {
		return Flow{}, err
	}
	_ = rows.Err()
	return f, nil
}

// List returns every flow, newest first.
func (s *Store) List(ctx context.Context) ([]Flow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+selectCols+` FROM vayuflow_flows ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("vayuflow: list flows: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Flow
	for rows.Next() {
		f, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	_ = rows.Err()
	return out, nil
}

// Delete removes a flow.
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM vayuflow_flows WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("vayuflow: delete flow: %w", err)
	}
	return nil
}

// SetMode moves a flow between dry-run and live. It is separate from Save
// because going live is a distinct, logged operator decision rather than an
// incidental field edit, and the caller records the transition with the actor
// and the prior mode.
func (s *Store) SetMode(ctx context.Context, id string, mode RunMode) (prior RunMode, err error) {
	if mode == runUnset {
		return runUnset, fmt.Errorf("vayuflow: refusing to set a flow to an unset mode")
	}
	f, err := s.Get(ctx, id)
	if err != nil {
		return runUnset, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE vayuflow_flows SET mode=?,updated_at=? WHERE id=?`,
		mode.String(), time.Now().UTC().Format(tsLayout), id); err != nil {
		return runUnset, fmt.Errorf("vayuflow: set mode: %w", err)
	}
	return f.Mode, nil
}

// SetEnabled turns a flow on or off, returning what it was.
//
// Separate from Save for the reason SetMode is: it is a distinct operator
// decision with its own audit entry, and returning the prior value is what lets
// that entry distinguish a first enable from a re-enable. Editing a flow must
// not silently change whether it is running.
func (s *Store) SetEnabled(ctx context.Context, id string, on bool) (prior bool, err error) {
	f, err := s.Get(ctx, id)
	if err != nil {
		return false, err
	}
	v := 0
	if on {
		v = 1
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE vayuflow_flows SET enabled=?,updated_at=? WHERE id=?`,
		v, time.Now().UTC().Format(tsLayout), id); err != nil {
		return false, fmt.Errorf("vayuflow: set enabled: %w", err)
	}
	return f.Enabled, nil
}

// LoadableFlows returns the enabled flows whose stored document still satisfies
// Complete(). A row that no longer validates — because it was edited by hand,
// or because an action was removed from the registry by an upgrade — is
// reported rather than silently skipped: a flow an operator believes is armed
// and which the runner quietly ignores is worse than one that errors.
func (s *Store) LoadableFlows(ctx context.Context) (ok []Flow, rejected map[string]error, err error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	rejected = map[string]error{}
	for _, f := range all {
		if !f.Enabled {
			continue
		}
		if err := f.Complete(); err != nil {
			rejected[f.ID] = err
			continue
		}
		ok = append(ok, f)
	}
	return ok, rejected, nil
}

// DescribeMode is used by the panel and the audit trail so both name the modes
// identically; a panel that says "test mode" while the trail says "dry-run"
// makes an operator reconcile two vocabularies for one state.
func DescribeMode(m RunMode) string {
	switch m {
	case RunDryRun:
		return "dry-run — effects are captured and refused at the capability boundary"
	case RunLive:
		return "live — effects reach the world, bounded by the declared budget"
	}
	return "unset — this flow cannot run"
}

// TriggerSummary renders a trigger for display in one short phrase.
func TriggerSummary(t Trigger) string {
	switch t.Kind {
	case TriggerSchedule:
		return "schedule · " + t.Cron
	case TriggerEvent:
		return "event · " + t.Event
	case TriggerManual:
		return "manual"
	}
	return "unset"
}

// ActionSummary renders a step list as a short, stable phrase for the panel.
func ActionSummary(steps []Step) string {
	names := make([]string, 0, len(steps))
	for _, s := range steps {
		names = append(names, s.Action)
	}
	return strings.Join(names, " → ")
}
