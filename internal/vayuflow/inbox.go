// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Event names a flow can be triggered by. They are short, stable strings rather
// than Go type names: a trigger stored as
// "github.com/johalputt/vayupress/internal/events.ArticleCreated" would break on
// any package move, and an operator reading the panel should see something they
// could have typed.
const (
	EventArticleCreated = "article.created"
	EventArticleUpdated = "article.updated"
	EventArticleDeleted = "article.deleted"
)

// KnownEvents is every event a flow may subscribe to. A trigger naming anything
// else is refused at save time — an event that will never arrive produces a
// flow the operator believes is armed and which silently never runs, which is
// the failure this subsystem exists to make impossible to have unnoticed.
func KnownEvents() []string {
	return []string{EventArticleCreated, EventArticleUpdated, EventArticleDeleted}
}

func knownEvent(name string) bool {
	for _, e := range KnownEvents() {
		if e == name {
			return true
		}
	}
	return false
}

// InboxRow is one durable trigger waiting to be considered.
type InboxRow struct {
	ID        int64
	EventName string
	EventID   string
	Subject   Subject
	CreatedAt time.Time
}

// Inbox is the durable landing place for domain events a flow might care about.
type Inbox struct{ db *sql.DB }

// NewInbox creates an Inbox.
func NewInbox(db *sql.DB) *Inbox { return &Inbox{db: db} }

// Append records an event. It returns an error rather than swallowing one,
// because the caller is the outbox dispatch bridge: if this write fails, that
// dispatch must fail too, so the outbox row stays pending and is retried. A
// bridge that logged and returned nil would mark the outbox row delivered and
// lose the trigger — durable up to the last link and lossy at it.
func (ib *Inbox) Append(ctx context.Context, eventName, eventID string, subj Subject) error {
	if !knownEvent(eventName) {
		return fmt.Errorf("vayuflow: refusing to inbox unknown event %q", eventName)
	}
	payload, err := json.Marshal(subj)
	if err != nil {
		return fmt.Errorf("vayuflow: encode event subject: %w", err)
	}
	_, err = ib.db.ExecContext(ctx,
		`INSERT INTO vayuflow_inbox(event_name,event_id,subject_json,created_at) VALUES(?,?,?,?)`,
		eventName, eventID, string(payload), time.Now().UTC().Format(tsLayout))
	if err != nil {
		// The outbox retries a failed dispatch by re-running it whole, so the
		// same event can arrive twice. The partial UNIQUE index on event_id
		// turns that into a collision, and a collision means "already
		// recorded" — returning nil lets the retry complete instead of looping
		// forever on work that is already durably captured.
		if isUniqueViolation(err) {
			return nil
		}
		return fmt.Errorf("vayuflow: append to inbox: %w", err)
	}
	return nil
}

// Pending returns undrained rows oldest first.
func (ib *Inbox) Pending(ctx context.Context, limit int) ([]InboxRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := ib.db.QueryContext(ctx,
		`SELECT id,event_name,event_id,subject_json,created_at FROM vayuflow_inbox WHERE drained_at IS NULL ORDER BY id ASC LIMIT ?`,
		limit)
	if err != nil {
		return nil, fmt.Errorf("vayuflow: read inbox: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []InboxRow
	for rows.Next() {
		var (
			r         InboxRow
			subjJSON  string
			createdAt string
		)
		if err := rows.Scan(&r.ID, &r.EventName, &r.EventID, &subjJSON, &createdAt); err != nil {
			return nil, err
		}
		if subjJSON != "" {
			if err := json.Unmarshal([]byte(subjJSON), &r.Subject); err != nil {
				// A row whose payload cannot be read is still drained below, so
				// it cannot wedge the queue; the flow simply sees an empty
				// subject and its condition decides.
				r.Subject = Subject{}
			}
		}
		r.CreatedAt, _ = time.Parse(tsLayout, createdAt)
		out = append(out, r)
	}
	_ = rows.Err()
	return out, nil
}

// MarkDrained records that a row has been offered to every flow that cares.
//
// It is set whatever those flows decided — including when NO flow matched. A
// row left pending because nothing wanted it would make the inbox grow forever
// on an install with no event flows, which is the trigger-storm failure wearing
// a different hat.
func (ib *Inbox) MarkDrained(ctx context.Context, id int64) error {
	_, err := ib.db.ExecContext(ctx,
		`UPDATE vayuflow_inbox SET drained_at=? WHERE id=?`,
		time.Now().UTC().Format(tsLayout), id)
	if err != nil {
		return fmt.Errorf("vayuflow: mark inbox row drained: %w", err)
	}
	return nil
}

// PruneDrained deletes drained rows older than the retention window and returns
// how many. The trail is bounded by policy rather than by hope.
func (ib *Inbox) PruneDrained(ctx context.Context, olderThan time.Duration) (int, error) {
	res, err := ib.db.ExecContext(ctx,
		`DELETE FROM vayuflow_inbox WHERE drained_at IS NOT NULL AND drained_at < ?`,
		time.Now().UTC().Add(-olderThan).Format(tsLayout))
	if err != nil {
		return 0, fmt.Errorf("vayuflow: prune inbox: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return int(n), nil
}

// PendingCount is what the panel shows: a backlog that is not shrinking is the
// symptom of a drainer that has stopped.
func (ib *Inbox) PendingCount(ctx context.Context) (int, error) {
	var n int
	err := ib.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vayuflow_inbox WHERE drained_at IS NULL`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("vayuflow: count pending inbox rows: %w", err)
	}
	return n, nil
}

// DrainResult reports what one drain pass did.
type DrainResult struct {
	Rows      int
	Fired     int
	Duplicate int
	NoFlow    int
	Broken    map[string]error
}

// Drain offers every pending inbox row to the flows triggered by its event.
//
// The identity passed to the runner is the INBOX ROW ID, which is what makes
// redelivery safe: the outbox may hand the same domain event over more than
// once (a crash between dispatch and the delivered mark), producing a second
// inbox row — but a row that has already been drained is never re-offered, and
// even if it were, the run's idempotency key is derived from the row id.
func (dr *Drainer) Drain(ctx context.Context, limit int) (DrainResult, error) {
	res := DrainResult{Broken: map[string]error{}}
	rows, err := dr.inbox.Pending(ctx, limit)
	if err != nil {
		return res, err
	}
	if len(rows) == 0 {
		return res, nil
	}
	loadable, rejected, err := dr.flows.LoadableFlows(ctx)
	if err != nil {
		return res, err
	}
	for id, e := range rejected {
		res.Broken[id] = e
	}

	for _, row := range rows {
		res.Rows++
		matched := false
		for _, f := range loadable {
			if f.Trigger.Kind != TriggerEvent || f.Trigger.Event != row.EventName {
				continue
			}
			matched = true
			identity := "inbox-" + strconv.FormatInt(row.ID, 10)
			_, err := dr.runner.Execute(ctx, f, "event · "+row.EventName, identity, row.Subject)
			switch {
			case err == nil:
				res.Fired++
			case isDuplicate(err):
				res.Duplicate++
			default:
				res.Broken[f.ID] = err
			}
		}
		if !matched {
			res.NoFlow++
		}
		// Drained whatever happened, including when a flow errored. A row that
		// stayed pending on error would be retried forever by the next pass and
		// would wedge every row behind it — and the run trail already records
		// what went wrong, so nothing is lost by moving on.
		if err := dr.inbox.MarkDrained(ctx, row.ID); err != nil {
			return res, err
		}
	}
	return res, nil
}

// Drainer turns inbox rows into runs.
type Drainer struct {
	inbox  *Inbox
	flows  *Store
	runner *Runner
}

// NewDrainer creates a Drainer.
func NewDrainer(inbox *Inbox, flows *Store, runner *Runner) *Drainer {
	return &Drainer{inbox: inbox, flows: flows, runner: runner}
}

// Run drains on an interval until ctx is cancelled.
func (dr *Drainer) Run(ctx context.Context, every time.Duration, onResult func(DrainResult)) {
	if every <= 0 {
		every = 5 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		res, err := dr.Drain(ctx, 100)
		if err == nil && onResult != nil {
			onResult(res)
		}
	}
}
