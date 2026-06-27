package newsletter

// console.go — the data layer behind the VayuOS Newsletter console.
//
// The original newsletter.go store only exposed Subscribe / Confirm /
// Unsubscribe / ListActive / Count. The operator console needs to *manage* the
// audience and broadcasts: list & search across all states, hard-delete a
// record (GDPR erasure / spam cleanup), export the list, chart growth, and keep
// a persisted history of broadcasts with their delivery tallies. Those queries
// live here so newsletter.go stays focused on the public double-opt-in flow.

import (
	"context"
	"encoding/csv"
	"io"
	"strconv"
	"strings"
	"time"
)

// Stats is a snapshot of newsletter audience health.
type Stats struct {
	Total        int     `json:"total"`        // every row, any state
	Active       int     `json:"active"`       // status=active AND confirmed (the reachable list)
	Pending      int     `json:"pending"`      // status=active but not yet confirmed (double opt-in)
	Unsubscribed int     `json:"unsubscribed"` // status=inactive
	NewLast30    int     `json:"new_last_30"`  // subscribed in the last 30 days
	ConfirmRate  float64 `json:"confirm_rate"` // confirmed / (confirmed+pending), 0..1
}

// Stats computes the audience snapshot in two cheap aggregate queries.
func (s *Store) Stats(ctx context.Context) (*Stats, error) {
	st := &Stats{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT status,confirmed,COUNT(*) FROM newsletter_subscribers GROUP BY status,confirmed`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var status string
		var confirmed, n int
		if err := rows.Scan(&status, &confirmed, &n); err != nil {
			rows.Close()
			return nil, err
		}
		st.Total += n
		switch {
		case status == "inactive":
			st.Unsubscribed += n
		case confirmed != 0:
			st.Active += n
		default:
			st.Pending += n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02 15:04:05")
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM newsletter_subscribers WHERE subscribed_at>=?`, cutoff).Scan(&st.NewLast30)
	if denom := st.Active + st.Pending; denom > 0 {
		st.ConfirmRate = float64(st.Active) / float64(denom)
	}
	return st, nil
}

// List returns subscribers filtered by audience segment and an optional email
// search term, newest first. filter is one of "all", "active", "pending",
// "unsubscribed" (anything else means "all").
func (s *Store) List(ctx context.Context, filter, search string, limit int) ([]Subscriber, error) {
	if limit <= 0 {
		limit = 500
	}
	where := []string{}
	args := []interface{}{}
	switch filter {
	case "active":
		where = append(where, "status='active' AND confirmed=1")
	case "pending":
		where = append(where, "status='active' AND confirmed=0")
	case "unsubscribed":
		where = append(where, "status='inactive'")
	}
	if q := strings.TrimSpace(strings.ToLower(search)); q != "" {
		where = append(where, "LOWER(email) LIKE ?")
		args = append(args, "%"+q+"%")
	}
	sql := `SELECT id,email,status,confirmed,token,subscribed_at,unsubscribed_at FROM newsletter_subscribers`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	sql += " ORDER BY subscribed_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subscriber
	for rows.Next() {
		sub, err := scanSubscriberRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sub)
	}
	return out, rows.Err()
}

// scanSubscriberRow scans a full subscriber row (shared by List).
func scanSubscriberRow(sc interface{ Scan(...interface{}) error }) (*Subscriber, error) {
	var sub Subscriber
	var subsRaw string
	var unsubRaw *string
	if err := sc.Scan(&sub.ID, &sub.Email, &sub.Status, &sub.Confirmed, &sub.Token, &subsRaw, &unsubRaw); err != nil {
		return nil, err
	}
	sub.SubscribedAt, _ = parseTime(subsRaw)
	if unsubRaw != nil && *unsubRaw != "" {
		if t, err := parseTime(*unsubRaw); err == nil {
			sub.UnsubscribedAt = &t
		}
	}
	return &sub, nil
}

// parseTime accepts the two timestamp shapes SQLite hands back in this schema:
// the CURRENT_TIMESTAMP form ("2006-01-02 15:04:05") and the RFC3339 form
// written by Unsubscribe.
func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// Delete permanently removes a subscriber by id (GDPR erasure / spam cleanup).
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM newsletter_subscribers WHERE id=?`, id)
	return err
}

// GrowthByDay returns the count of new subscribers for each of the last n days,
// oldest first, with zero-filled gaps — ready for a sparkline.
func (s *Store) GrowthByDay(ctx context.Context, days int) ([]int, error) {
	if days <= 0 {
		days = 30
	}
	out := make([]int, days)
	rows, err := s.db.QueryContext(ctx,
		`SELECT date(subscribed_at) d,COUNT(*) c FROM newsletter_subscribers WHERE subscribed_at>=date('now',?) GROUP BY d`,
		"-"+strconv.Itoa(days-1)+" days")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	byDay := map[string]int{}
	for rows.Next() {
		var d string
		var c int
		if rows.Scan(&d, &c) == nil {
			byDay[d] = c
		}
	}
	now := time.Now().UTC()
	for i := 0; i < days; i++ {
		day := now.AddDate(0, 0, -(days - 1 - i)).Format("2006-01-02")
		out[i] = byDay[day]
	}
	return out, rows.Err()
}

// ExportCSV writes every subscriber as RFC 4180 CSV to w.
func (s *Store) ExportCSV(ctx context.Context, w io.Writer) error {
	list, err := s.List(ctx, "all", "", 1000000)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"email", "status", "confirmed", "subscribed_at", "unsubscribed_at"}); err != nil {
		return err
	}
	for _, sub := range list {
		confirmed := "false"
		if sub.Confirmed {
			confirmed = "true"
		}
		unsub := ""
		if sub.UnsubscribedAt != nil {
			unsub = sub.UnsubscribedAt.UTC().Format("2006-01-02")
		}
		if err := cw.Write([]string{
			sub.Email, sub.Status, confirmed,
			sub.SubscribedAt.UTC().Format("2006-01-02"), unsub,
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// =============================================================================
// Broadcast history
// =============================================================================

// Broadcast is one persisted newsletter send with its delivery tallies.
type Broadcast struct {
	ID          string     `json:"id"`
	Subject     string     `json:"subject"`
	Recipients  int        `json:"recipients"`
	Sent        int        `json:"sent"`
	Failed      int        `json:"failed"`
	Status      string     `json:"status"` // sending | complete
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// CreateBroadcast records a broadcast in the "sending" state and returns its id.
func (s *Store) CreateBroadcast(ctx context.Context, subject string, recipients int) (string, error) {
	id := "bc_" + newToken()[:16]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO newsletter_broadcasts(id,subject,recipients,status) VALUES(?,?,?,'sending')`,
		id, subject, recipients)
	return id, err
}

// FinishBroadcast records the final sent/failed tallies and marks it complete.
func (s *Store) FinishBroadcast(ctx context.Context, id string, sent, failed int) error {
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	_, err := s.db.ExecContext(ctx,
		`UPDATE newsletter_broadcasts SET sent=?,failed=?,status='complete',completed_at=? WHERE id=?`,
		sent, failed, now, id)
	return err
}

// ListBroadcasts returns the most recent broadcasts, newest first.
func (s *Store) ListBroadcasts(ctx context.Context, limit int) ([]Broadcast, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,subject,recipients,sent,failed,status,created_at,completed_at
		   FROM newsletter_broadcasts ORDER BY created_at DESC, rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Broadcast
	for rows.Next() {
		var b Broadcast
		var completed *string
		if err := rows.Scan(&b.ID, &b.Subject, &b.Recipients, &b.Sent, &b.Failed, &b.Status, &b.CreatedAt, &completed); err != nil {
			return nil, err
		}
		if completed != nil && *completed != "" {
			if t, err := parseTime(*completed); err == nil {
				b.CompletedAt = &t
			}
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
