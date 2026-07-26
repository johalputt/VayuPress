// SPDX-License-Identifier: Apache-2.0

package db

// indexnow_status.go — per-post IndexNow submission status (migration 067).
//
// Every time a published post's URL is announced to IndexNow, the outcome is
// upserted here keyed by slug, so the Posts manager can show whether each post
// was submitted and offer a one-click manual re-ping when it was not. Only real
// submission attempts (accepted or rejected) are recorded; skips (draft, no key,
// read-only mode) leave the last-known row untouched.

import (
	"strings"
	"time"
)

// IndexNowState is the recorded outcome of the most recent submission attempt.
const (
	IndexNowSubmitted = "submitted" // the endpoint accepted the URL (2xx)
	IndexNowFailed    = "failed"    // network error or a non-2xx response
)

// IndexNowStatus is one post's last IndexNow submission outcome.
type IndexNowStatus struct {
	State       string    // IndexNowSubmitted, IndexNowFailed, or "" when never attempted
	HTTPCode    int       // HTTP status of the attempt (0 for a transport error)
	Detail      string    // short human-readable reason (error text or status)
	SubmittedAt time.Time // when the attempt was made
}

// RecordIndexNow upserts the outcome of a submission attempt for one slug.
// Best-effort: a write failure here never blocks publishing or the ping itself.
func RecordIndexNow(slug, state string, httpCode int, detail string) {
	if DB == nil || strings.TrimSpace(slug) == "" {
		return
	}
	if len(detail) > 500 {
		detail = detail[:500]
	}
	_, _ = WDB.Exec(
		`INSERT INTO indexnow_submissions(slug,state,http_code,detail,submitted_at) VALUES(?,?,?,?,?)
		 ON CONFLICT(slug) DO UPDATE SET state=excluded.state, http_code=excluded.http_code, detail=excluded.detail, submitted_at=excluded.submitted_at`,
		slug, state, httpCode, detail, time.Now().Unix())
}

// IndexNowStatusOf returns the recorded status for a single slug. ok is false
// when there is no row (never attempted).
func IndexNowStatusOf(slug string) (st IndexNowStatus, ok bool) {
	if DB == nil {
		return IndexNowStatus{}, false
	}
	var ts int64
	err := Reader().QueryRow(
		`SELECT state, http_code, detail, submitted_at FROM indexnow_submissions WHERE slug=?`, slug).
		Scan(&st.State, &st.HTTPCode, &st.Detail, &ts)
	if err != nil {
		return IndexNowStatus{}, false
	}
	if ts > 0 {
		st.SubmittedAt = time.Unix(ts, 0).UTC()
	}
	return st, true
}

// IndexNowStatuses batch-loads statuses for the given slugs in one query, so the
// Posts list avoids an N+1. Slugs with no recorded attempt are simply absent
// from the returned map.
func IndexNowStatuses(slugs []string) map[string]IndexNowStatus {
	out := make(map[string]IndexNowStatus, len(slugs))
	if DB == nil || len(slugs) == 0 {
		return out
	}
	ph := make([]string, len(slugs))
	args := make([]interface{}, len(slugs))
	for i, s := range slugs {
		ph[i] = "?"
		args[i] = s
	}
	rows, err := Reader().Query(
		`SELECT slug, state, http_code, detail, submitted_at FROM indexnow_submissions WHERE slug IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		var st IndexNowStatus
		var ts int64
		if rows.Scan(&slug, &st.State, &st.HTTPCode, &st.Detail, &ts) == nil {
			if ts > 0 {
				st.SubmittedAt = time.Unix(ts, 0).UTC()
			}
			out[slug] = st
		}
	}
	_ = rows.Err() // best-effort batch load; partial results are acceptable
	return out
}
