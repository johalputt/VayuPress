package members

// export.go — member list export.
//
// Operators migrating from Substack/Ghost (or just keeping their own backups)
// need a clean, spreadsheet-friendly dump of every member with their plan,
// status, monthly value, labels, and signup date. ExportCSV streams that as
// RFC 4180 CSV so it works in Excel, Google Sheets, and re-imports elsewhere.

import (
	"context"
	"encoding/csv"
	"io"
	"strconv"
	"strings"
)

// ExportCSV writes every member as a CSV document to w. Columns:
// email, name, tier, status, newsletter_opt_in, mrr_cents, currency, labels,
// created_at, last_seen_at. Labels are joined with a pipe so the cell stays a
// single CSV field.
func (s *Store) ExportCSV(ctx context.Context, w io.Writer) error {
	list, err := s.List(ctx, 100000)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{
		"email", "name", "tier", "status", "newsletter_opt_in",
		"mrr_cents", "currency", "labels", "created_at", "last_seen_at",
	}); err != nil {
		return err
	}
	for i := range list {
		m := list[i]
		mrr, currency := 0, ""
		if sub, _ := s.ActiveSubscription(ctx, m.ID); sub != nil {
			mrr = sub.MonthlyValueCents()
			currency = sub.Currency
		}
		lastSeen := ""
		if m.LastSeenAt != nil {
			lastSeen = m.LastSeenAt.UTC().Format("2006-01-02")
		}
		if err := cw.Write([]string{
			m.Email,
			m.Name,
			m.Tier,
			m.Status,
			boolStr(m.NewsletterOptIn),
			strconv.Itoa(mrr),
			currency,
			strings.Join(m.Labels, "|"),
			m.CreatedAt.UTC().Format("2006-01-02"),
			lastSeen,
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
