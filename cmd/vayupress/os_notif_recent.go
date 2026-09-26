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

// notifClearedAt is when the viewer last cleared the bell's events; the zero
// time when they never have. Events up to it leave the list.
func notifClearedAt(ctx context.Context, userID string) time.Time {
	var at sql.NullTime
	if dbpkg.DB != nil {
		_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT cleared_at FROM notification_seen WHERE user_id = ?`, userID).Scan(&at)
	}
	return at.Time
}

// notifDismissed is the set of needs-action fingerprints the viewer cleared
// that are still hidden at now.
func notifDismissed(ctx context.Context, userID string, now time.Time) map[string]bool {
	out := map[string]bool{}
	if dbpkg.DB == nil {
		return out
	}
	rows, err := dbpkg.Reader().QueryContext(ctx, `SELECT fingerprint FROM notification_dismissed WHERE user_id = ? AND until > ?`, userID, now.UTC())
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var f string
		if rows.Scan(&f) == nil {
			out[f] = true
		}
	}
	_ = rows.Err() // a read cut short hides fewer items, never more
	return out
}

// notifDismissFor is how long Clear all hides a needs-action item that does
// not change. A day, so a condition nobody fixed is back the next morning.
const notifDismissFor = 24 * time.Hour

// fingerprint identifies a needs-action item as it is now: what it is and the
// line it shows, which carries a tally's count. Clear all hides the item while
// this stays the same; a new count or a new detail is new information, and the
// item comes back.
func (n osNotification) fingerprint() string {
	return n.Kind + "\x1f" + n.Title + "\x1f" + n.line()
}

// handleOSNotificationsClear empties the bell for the viewer: the recent
// events leave the list, and each needs-action item on show is hidden until it
// changes or a day passes. Home's attention list still shows every condition.
func (a *App) handleOSNotificationsClear(w http.ResponseWriter, r *http.Request) {
	if dbpkg.DB == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "no-database", "the database is not available", "")
		return
	}
	s := a.getOSSettings(r.Context())
	if err := clearNotifications(r.Context(), currentUserIDOf(r), s.Notifications, time.Now().UTC()); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "clear-failed", "could not clear notifications", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// clearNotifications records a Clear all by userID at now, hiding notifs.
func clearNotifications(ctx context.Context, userID string, notifs []osNotification, now time.Time) error {
	return dbpkg.RunInTx(ctx, dbpkg.DB, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO notification_seen(user_id, seen_at, cleared_at) VALUES(?, ?, ?) ON CONFLICT(user_id) DO UPDATE SET seen_at = excluded.seen_at, cleared_at = excluded.cleared_at`,
			userID, now, now); err != nil {
			return err
		}
		// Earlier dismissals are replaced: what this clear hides is what is on
		// show now.
		if _, err := tx.ExecContext(ctx, `DELETE FROM notification_dismissed WHERE user_id = ?`, userID); err != nil {
			return err
		}
		for _, n := range notifs {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO notification_dismissed(user_id, fingerprint, until) VALUES(?, ?, ?)`,
				userID, n.fingerprint(), now.Add(notifDismissFor)); err != nil {
				return err
			}
		}
		return nil
	})
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
