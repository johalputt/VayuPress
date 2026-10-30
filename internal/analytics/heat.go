// SPDX-License-Identifier: Apache-2.0

package analytics

// heat.go — a site's click map, for the sites that turn it on.
//
// A click is never kept: it is added to a count, of the clicks on one page,
// one day, at one width class, on one element, in one tenth of that element
// across and down (migration 124). The counts wait in the event collector,
// added up in memory, and are written with its flush, so a busy page's clicks
// are one row's update each flush rather than one write each.

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/span"
)

// Widths are the width classes a click map is kept for, as the reader's
// window was: under 640 pixels a phone, under 1024 a tablet.
var Widths = []string{"phone", "tablet", "desktop"}

// heatCap bounds the click buckets held between flushes. Past it a click
// opening a new bucket is dropped and counted, as a beacon past the queue's
// cap is.
const heatCap = 16384

// MaxClicks is how many clicks one beacon may carry.
const MaxClicks = 50

// Click is one click as the reader's page sent it: the element's path, the
// tenth of it across and down, and whether it was the third of a burst on
// one element within a second (rage) or on what is no link or control
// (dead).
type Click struct {
	Element    string
	X, Y       int
	Rage, Dead bool
}

type heatBucket struct {
	site, path, day, width, element string
	x, y                            int
}

type heatCount struct{ clicks, rage, dead int64 }

// ValidElement reports whether e is an element path as the click map keeps
// it: up to 16 steps from the body, joined by ">", each a lower-case tag
// name, with ":n" when it is the nth of its tag among its siblings. Nothing
// else is taken, so no text, class or attribute of the page is ever stored.
func ValidElement(e string) bool {
	if e == "" || len(e) > 300 {
		return false
	}
	steps := strings.Split(e, ">")
	if len(steps) > 16 {
		return false
	}
	for _, st := range steps {
		tag, n, indexed := strings.Cut(st, ":")
		if tag == "" || len(tag) > 24 || tag[0] < 'a' || tag[0] > 'z' {
			return false
		}
		for _, r := range tag {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
		if indexed {
			i, err := strconv.Atoi(n)
			if err != nil || i < 2 || i > 999 || strconv.Itoa(i) != n {
				return false
			}
		}
	}
	return true
}

// widthOK reports whether w is one of Widths.
func widthOK(w string) bool {
	for _, x := range Widths {
		if w == x {
			return true
		}
	}
	return false
}

// RecordClicks adds a beacon's clicks on path, seen at width, to site's
// counts for today. A click whose element or tenth is not one the map keeps
// is dropped, and so is the whole beacon at an unknown width; past
// MaxClicks the rest are.
func (s *Store) RecordClicks(site, path, width string, clicks []Click) {
	if s == nil || s.evq == nil || !widthOK(width) {
		return
	}
	path = NormalizePath(path)
	if path == "" {
		return
	}
	if len(clicks) > MaxClicks {
		clicks = clicks[:MaxClicks]
	}
	day := s.now().UTC().Format("2006-01-02")
	c := s.evq
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.heat == nil {
		c.heat = map[heatBucket]heatCount{}
	}
	for _, k := range clicks {
		if !ValidElement(k.Element) || k.X < 0 || k.X > 9 || k.Y < 0 || k.Y > 9 {
			continue
		}
		b := heatBucket{site, path, day, width, k.Element, k.X, k.Y}
		n, ok := c.heat[b]
		if !ok && len(c.heat) >= heatCap {
			c.heatDropped++
			continue
		}
		n.clicks++
		if k.Rage {
			n.rage++
		}
		if k.Dead {
			n.dead++
		}
		c.heat[b] = n
	}
}

// flushHeat writes the counts held, in one transaction. Counts the database
// refuses are added back, to be tried with the next flush.
func (s *Store) flushHeat(ctx context.Context) error {
	c := s.evq
	c.mu.Lock()
	held := c.heat
	c.heat = nil
	c.mu.Unlock()
	if len(held) == 0 {
		return nil
	}
	err := s.writeHeat(ctx, held)
	if err != nil {
		c.mu.Lock()
		if c.heat == nil {
			c.heat = map[heatBucket]heatCount{}
		}
		for b, n := range held {
			m, ok := c.heat[b]
			if !ok && len(c.heat) >= heatCap {
				c.heatDropped++
				continue
			}
			m.clicks, m.rage, m.dead = m.clicks+n.clicks, m.rage+n.rage, m.dead+n.dead
			c.heat[b] = m
		}
		c.mu.Unlock()
	}
	return err
}

func (s *Store) writeHeat(ctx context.Context, held map[heatBucket]heatCount) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO analytics_heat(domain_id,path,day,width,element,x,y,clicks,rage,dead) VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(domain_id,path,day,width,element,x,y) DO UPDATE SET clicks=clicks+excluded.clicks, rage=rage+excluded.rage, dead=dead+excluded.dead`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for b, n := range held {
		if _, err := stmt.ExecContext(ctx, b.site, b.path, b.day, b.width, b.element, b.x, b.y, n.clicks, n.rage, n.dead); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return err
		}
	}
	_ = stmt.Close()
	return tx.Commit()
}

// HeatElement is one element's clicks on a page over a period: in all, in
// rage, dead, and by tenth of the element, Cells[x*10+y].
type HeatElement struct {
	Element            string
	Clicks, Rage, Dead int64
	Cells              [100]int64
}

// ClickMapScoped is path's most clicked elements on scope's site over the
// period at width, most clicked first, at most limit of them. Both
// spellings of the path, with and without a trailing slash, are the page.
func (s *Store) ClickMapScoped(ctx context.Context, scope, path, width string, days, limit int) ([]HeatElement, error) {
	from, until := span.Days(ctx, s.now(), days)
	trimmed := NormalizePath(path)
	clause, cargs := domainClause(scope)
	rows, err := s.readDB().QueryContext(ctx, `SELECT element, x, y, SUM(clicks), SUM(rage), SUM(dead) FROM analytics_heat
		WHERE path IN (?,?) AND width=? AND day>=? AND day<?`+clause+` GROUP BY element, x, y`,
		append([]any{trimmed, trimmed + "/", width, from, until}, cargs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string]*HeatElement{}
	for rows.Next() {
		var e string
		var x, y int
		var n, rage, dead int64
		if err := rows.Scan(&e, &x, &y, &n, &rage, &dead); err != nil {
			return nil, err
		}
		h := by[e]
		if h == nil {
			h = &HeatElement{Element: e}
			by[e] = h
		}
		h.Clicks, h.Rage, h.Dead = h.Clicks+n, h.Rage+rage, h.Dead+dead
		if x >= 0 && x <= 9 && y >= 0 && y <= 9 {
			h.Cells[x*10+y] += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]HeatElement, 0, len(by))
	for _, h := range by {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Clicks != out[j].Clicks {
			return out[i].Clicks > out[j].Clicks
		}
		return out[i].Element < out[j].Element
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
