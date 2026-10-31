// SPDX-License-Identifier: Apache-2.0

package signins

import (
	"context"
	"sort"
	"strings"
	"time"
)

// failed is the stages that are failures, for the queries.
const failed = `stage IN ('password', 'code', 'paused')`

// Report is what the Sign-ins page shows for a period.
type Report struct {
	Failures, Sources, Accounts, PasswordKnown int
	Top                                        []Source
	ByAccount, ByDoor                          []Count
	Hours                                      [24]int // failures by hour of the day, UTC
	Countries                                  map[string]int
}

// Count is a name and how many.
type Count struct {
	Name string
	N    int
}

// Report reads the attempts since since: for account, its own; for "", the
// install's. At most limit sources are listed, those that failed most first.
func (s *Store) Report(ctx context.Context, since time.Time, account string, limit int) (Report, error) {
	var r Report
	r.Countries = map[string]int{}
	from := since.Unix() / 60
	base := `minute >= ?`
	args := []any{from}
	if account != "" {
		base += ` AND account = ?`
		args = append(args, account)
	}
	where := base + ` AND ` + failed
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(n), 0), COUNT(DISTINCT ip_fp), COUNT(DISTINCT account) FROM signin_attempts WHERE `+where, args...).
		Scan(&r.Failures, &r.Sources, &r.Accounts); err != nil {
		return r, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT ip_fp) FROM signin_attempts WHERE stage = 'code' AND `+base, args...).Scan(&r.PasswordKnown); err != nil {
		return r, err
	}
	src, err := s.db.QueryContext(ctx, `SELECT ip_fp, MAX(ip_masked), MAX(country), SUM(n), COUNT(DISTINCT account), GROUP_CONCAT(DISTINCT door), MAX(minute), MAX(stage = 'code')
		FROM signin_attempts WHERE `+where+` GROUP BY ip_fp ORDER BY SUM(n) DESC, MAX(minute) DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return r, err
	}
	for src.Next() {
		var x Source
		var doors string
		var last int64
		if err := src.Scan(&x.FP, &x.Masked, &x.Country, &x.Failures, &x.Accounts, &doors, &last, &x.Known); err != nil {
			src.Close()
			return r, err
		}
		x.Doors = strings.Split(doors, ",")
		sort.Strings(x.Doors)
		x.Last = time.Unix(last*60, 0)
		r.Top = append(r.Top, x)
	}
	src.Close()
	if err := src.Err(); err != nil {
		return r, err
	}
	s.decorate(r.Top, account)
	for _, q := range []struct {
		col string
		to  *[]Count
	}{{"account", &r.ByAccount}, {"door", &r.ByDoor}} {
		rows, err := s.db.QueryContext(ctx, `SELECT `+q.col+`, SUM(n) FROM signin_attempts WHERE `+where+` GROUP BY `+q.col+` ORDER BY SUM(n) DESC LIMIT 8`, args...)
		if err != nil {
			return r, err
		}
		for rows.Next() {
			var c Count
			if err := rows.Scan(&c.Name, &c.N); err != nil {
				rows.Close()
				return r, err
			}
			*q.to = append(*q.to, c)
		}
		rows.Close()
	}
	rows, err := s.db.QueryContext(ctx, `SELECT (minute / 60) % 24, SUM(n) FROM signin_attempts WHERE `+where+` GROUP BY 1`, args...)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var h, n int
		if err := rows.Scan(&h, &n); err != nil {
			rows.Close()
			return r, err
		}
		r.Hours[h] = n
	}
	rows.Close()
	rows, err = s.db.QueryContext(ctx, `SELECT country, SUM(n) FROM signin_attempts WHERE `+where+` AND country != '' GROUP BY country`, args...)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var c string
		var n int
		if err := rows.Scan(&c, &n); err != nil {
			rows.Close()
			return r, err
		}
		r.Countries[c] = n
	}
	rows.Close()
	return r, rows.Err()
}

// decorate marks each source's block and whether a holder said it was them.
func (s *Store) decorate(list []Source, account string) {
	now := s.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range list {
		for acct, fps := range s.known {
			if fps[list[i].FP] && (account == "" || acct == account) {
				list[i].Mine = true
			}
		}
		for _, b := range s.blocks {
			if b.Kind == "address" && b.Key == list[i].FP && b.Active(now) && (b.Account == "" || b.Account == account) {
				c := *b
				c.Hits += s.hits[b.ID]
				list[i].Block = &c
				break
			}
		}
	}
}

// Row is one count of attempts.
type Row struct {
	At                                    time.Time
	Account, Door, Stage, Masked, Country string
	FP                                    string
	N                                     int
}

// Attempts lists attempts newest first: of source fp when fp is set, of
// account when account is set, at most limit.
func (s *Store) Attempts(ctx context.Context, fp, account string, limit int) ([]Row, error) {
	where, args := `1 = 1`, []any{}
	if fp != "" {
		where += ` AND ip_fp = ?`
		args = append(args, fp)
	}
	if account != "" {
		where += ` AND account = ?`
		args = append(args, account)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT minute, account, door, stage, ip_masked, country, ip_fp, n FROM signin_attempts WHERE `+where+` ORDER BY minute DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var x Row
		var m int64
		if err := rows.Scan(&m, &x.Account, &x.Door, &x.Stage, &x.Masked, &x.Country, &x.FP, &x.N); err != nil {
			return nil, err
		}
		x.At = time.Unix(m*60, 0)
		out = append(out, x)
	}
	return out, rows.Err()
}
