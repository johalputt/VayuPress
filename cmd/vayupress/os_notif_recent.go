// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/mode"
)

// osRecentEvent is something that happened, and when: the bell's second half
// (render 09). Needs-action items are conditions that last until fixed, so
// they carry no time; these are events, so they always do, and each is read
// from a row that recorded it rather than inferred.
type osRecentEvent struct {
	Title, Detail, Href, Kind string
	At                        time.Time
}

// osRecentWindow is how far back the bell looks. The server does not know the
// viewer's timezone, so it sends a day of events and the page labels them
// Today or Yesterday by the viewer's own clock.
const osRecentWindow = 24 * time.Hour

// osRecentLimit keeps the bell a glance: the newest few, not a log.
const osRecentLimit = 8

// osRecentEvents gathers the last day's events the viewer could open, newest
// first. Each is gated by the page it links to, the same rule as the
// needs-action list. Each source applies the window itself, the database in
// its query: one check per source, so each is seen to hold on its own.
func (a *App) osRecentEvents(ctx context.Context, s *osSettings, now time.Time) []osRecentEvent {
	since := now.Add(-osRecentWindow)
	var out []osRecentEvent
	add := func(e osRecentEvent) {
		if s.AccessLevel >= osPathMinLevel(e.Href) {
			out = append(out, e)
		}
	}

	for _, t := range mode.Global.History() {
		if t.OccurredAt.After(since) {
			add(osRecentEvent{Title: "Mode changed to " + saModeLabel(t.To), Detail: t.Reason, Href: "/os/modes", Kind: "mode", At: t.OccurredAt})
		}
	}
	if st := a.vayuKeepStatus(); st.LastSuccess.After(since) {
		add(osRecentEvent{Title: "Backup copied to the replica", Href: "/os/vayukeep", Kind: "backup", At: st.LastSuccess})
	}

	if dbpkg.DB != nil {
		// datetime() on both sides: rows written by CURRENT_TIMESTAMP and rows
		// written from Go differ in format, and compared as text they misorder.
		cut := since.UTC().Format("2006-01-02 15:04:05")
		query := func(q string, row func(*sql.Rows) (osRecentEvent, error)) {
			rows, err := dbpkg.Reader().QueryContext(ctx, q, cut)
			if err != nil {
				return // a table missing on an old schema just yields no events
			}
			defer rows.Close()
			for rows.Next() {
				if e, err := row(rows); err == nil {
					add(e)
				}
			}
			_ = rows.Err() // a read cut short just yields fewer events
		}
		query(`SELECT name, created_at FROM contact_messages WHERE datetime(created_at) >= datetime(?) ORDER BY created_at DESC LIMIT 8`,
			func(r *sql.Rows) (osRecentEvent, error) {
				var name string
				var at time.Time
				err := r.Scan(&name, &at)
				return osRecentEvent{Title: "Message from " + name, Href: "/os/messages", Kind: "message", At: at}, err
			})
		query(`SELECT author, status, created_at FROM comments WHERE datetime(created_at) >= datetime(?) ORDER BY created_at DESC LIMIT 8`,
			func(r *sql.Rows) (osRecentEvent, error) {
				var author, status string
				var at time.Time
				err := r.Scan(&author, &status, &at)
				e := osRecentEvent{Title: "Comment from " + author, Href: "/os/comments", Kind: "comment", At: at}
				if status == "pending" {
					e.Detail = "Held for review"
				}
				return e, err
			})
		query(`SELECT email, created_at FROM members WHERE datetime(created_at) >= datetime(?) ORDER BY created_at DESC LIMIT 8`,
			func(r *sql.Rows) (osRecentEvent, error) {
				var email string
				var at time.Time
				err := r.Scan(&email, &at)
				return osRecentEvent{Title: email + " joined", Href: "/os/members", Kind: "member", At: at}, err
			})
		query(`SELECT slug, title, created_at FROM articles WHERE COALESCE(status,'published')='published' AND datetime(created_at) >= datetime(?) ORDER BY created_at DESC LIMIT 8`,
			func(r *sql.Rows) (osRecentEvent, error) {
				var slug, title string
				var at time.Time
				err := r.Scan(&slug, &title, &at)
				return osRecentEvent{Title: "Published “" + title + "”", Href: "/os/editor/" + slug, Kind: "post", At: at}, err
			})
		query(`SELECT COALESCE(to_version,''), status, COALESCE(detail,''), completed_at FROM update_history WHERE completed_at IS NOT NULL AND datetime(completed_at) >= datetime(?) ORDER BY completed_at DESC LIMIT 3`,
			func(r *sql.Rows) (osRecentEvent, error) {
				var to, status, detail string
				var at time.Time
				err := r.Scan(&to, &status, &detail, &at)
				e := osRecentEvent{Title: "Updated to " + to, Href: "/os/update", Kind: "update", At: at}
				if status != "success" {
					e.Title, e.Detail = "Update to "+to+" did not complete", detail
				}
				return e, err
			})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if len(out) > osRecentLimit {
		out = out[:osRecentLimit]
	}
	return out
}

// notifSeenAt is when the viewer last marked the bell's events read; the zero
// time when they never have.
func notifSeenAt(ctx context.Context, userID string) time.Time {
	var at time.Time
	if dbpkg.DB != nil {
		_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT seen_at FROM notification_seen WHERE user_id = ?`, userID).Scan(&at)
	}
	return at
}

// handleOSNotificationsSeen marks every event up to now read for the viewer.
// Needs-action items are untouched: a condition that still needs someone is
// not read by being looked at.
func (a *App) handleOSNotificationsSeen(w http.ResponseWriter, r *http.Request) {
	if dbpkg.DB == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "no-database", "the database is not available", "")
		return
	}
	userID := currentUserIDOf(r)
	if _, err := dbpkg.DB.ExecContext(r.Context(),
		`INSERT INTO notification_seen(user_id, seen_at) VALUES(?, ?) ON CONFLICT(user_id) DO UPDATE SET seen_at = excluded.seen_at`,
		userID, time.Now().UTC()); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "seen-failed", "could not mark notifications read", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
