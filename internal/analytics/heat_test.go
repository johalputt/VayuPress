// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"strconv"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// An element is kept as tags and their order alone: anything carrying the
// page's text, a class, an id or an attribute is refused, and so is a path
// too deep or an order written two ways.
func TestAClickIsKeptAsTagsAndOrderAlone(t *testing.T) {
	for e, want := range map[string]bool{
		"article>p:3>a:2":                true,
		"my-widget>svg>path":             true,
		"h2":                             true,
		"":                               false,
		"a.button":                       false, // a class
		"a#signup":                       false, // an id
		"a[href]":                        false, // an attribute
		"p>Hello":                        false, // text, or a tag not lower case
		"p>a:1":                          false, // the first is written bare
		"p>a:02":                         false, // one order, one spelling
		"p>a:x":                          false,
		"p>>a":                           false,
		"2p":                             false,
		strings.Repeat("div>", 16) + "a": false, // 17 steps
		strings.Repeat("div>", 15) + "a": true,
		strings.Repeat("a", 25):          false,
		"p>a:1000":                       false,
	} {
		if got := ValidElement(e); got != want {
			t.Errorf("ValidElement(%q) = %v, want %v", e, got, want)
		}
	}
}

// A page's click map is its own clicks, at the width asked, added up by
// element and by tenth, with the bursts and the clicks on nothing counted;
// what the map does not keep is dropped, one beacon carries fifty clicks at
// most, and another site's or page's clicks are in none of it.
func TestAPagesClickMapIsItsClicksAddedUp(t *testing.T) {
	openTrendingDB(t)
	s := New(dbpkg.DB)
	ctx := context.Background()
	s.RecordClicks("s1", "/post/?x=1", "phone", []Click{
		{Element: "article>p:3>a", X: 2, Y: 5},
		{Element: "article>p:3>a", X: 2, Y: 5, Rage: true},
		{Element: "article>img", X: 9, Y: 0, Dead: true},
		{Element: "article>p.lead", X: 1, Y: 1}, // a class
		{Element: "article>p", X: 10, Y: 1},     // no tenth
		{Element: "article>p", X: 1, Y: -1},
	})
	s.RecordClicks("s1", "/post", "phone", []Click{{Element: "article>p:3>a", X: 2, Y: 5}})
	s.RecordClicks("s1", "/post", "tablet", []Click{{Element: "nav", X: 0, Y: 0}})
	s.RecordClicks("s1", "/post", "watch", []Click{{Element: "footer", X: 0, Y: 0}})
	s.RecordClicks("s1", "/other", "phone", []Click{{Element: "footer", X: 0, Y: 0}})
	s.RecordClicks("s2", "/post", "phone", []Click{{Element: "footer", X: 0, Y: 0}})
	many := make([]Click, MaxClicks+10)
	for i := range many {
		many[i] = Click{Element: "aside", X: 0, Y: 0}
	}
	s.RecordClicks("s1", "/post", "phone", many)
	if err := s.FlushEvents(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.ClickMapScoped(ctx, "s1", "/post/", "phone", 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("elements = %+v, want the aside, the link and the image", got)
	}
	if got[0].Element != "aside" || got[0].Clicks != MaxClicks {
		t.Errorf("first = %s %d, want the aside's %d: one beacon's fifty", got[0].Element, got[0].Clicks, MaxClicks)
	}
	link := got[1]
	if link.Element != "article>p:3>a" || link.Clicks != 3 || link.Rage != 1 || link.Dead != 0 || link.Cells[25] != 3 {
		t.Errorf("the link = %+v, want 3 clicks, all in the tenth 2 across and 5 down, 1 a burst", link)
	}
	if img := got[2]; img.Element != "article>img" || img.Dead != 1 || img.Cells[90] != 1 {
		t.Errorf("the image = %+v, want 1 dead click 9 across, 0 down", img)
	}
	var odd int
	_ = dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM analytics_heat WHERE width NOT IN ('phone','tablet','desktop')`).Scan(&odd)
	if odd != 0 {
		t.Errorf("%d counts kept at a width the map does not have", odd)
	}
	if tab, _ := s.ClickMapScoped(ctx, "s1", "/post", "tablet", 7, 10); len(tab) != 1 || tab[0].Element != "nav" {
		t.Errorf("the tablet map = %+v, want the nav alone", tab)
	}
	if two, _ := s.ClickMapScoped(ctx, "s2", "/post", "phone", 7, 10); len(two) != 1 || two[0].Element != "footer" {
		t.Errorf("the other site's map = %+v, want its footer alone", two)
	}
}

// Clicks are held up to a cap between flushes, past which a click opening a
// new count is dropped and one adding to a count is kept; counts the
// database refuses are kept for the next flush.
func TestClickCountsAreHeldToACapAndKeptThroughARefusal(t *testing.T) {
	openTrendingDB(t)
	s := New(dbpkg.DB)
	ctx := context.Background()
	batch := make([]Click, 0, MaxClicks)
	opened := 0
	for el := 0; opened < heatCap; el++ {
		for c := 0; c < 100 && opened < heatCap; c++ {
			batch = append(batch, Click{Element: "x" + strconv.Itoa(el), X: c / 10, Y: c % 10})
			opened++
			if len(batch) == MaxClicks {
				s.RecordClicks("s1", "/p", "desktop", batch)
				batch = batch[:0]
			}
		}
	}
	s.RecordClicks("s1", "/p", "desktop", batch)
	s.RecordClicks("s1", "/p", "desktop", []Click{{Element: "past-the-cap", X: 0, Y: 0}, {Element: "x0", X: 0, Y: 0}})
	if s.evq.heatDropped != 1 {
		t.Errorf("%d clicks dropped at the cap, want the one opening a new count", s.evq.heatDropped)
	}
	if _, err := dbpkg.DB.Exec(`ALTER TABLE analytics_heat RENAME TO heat_away`); err != nil {
		t.Fatal(err)
	}
	if err := s.FlushEvents(ctx); err == nil {
		t.Fatal("a flush the database refused reported no error")
	}
	if _, err := dbpkg.DB.Exec(`ALTER TABLE heat_away RENAME TO analytics_heat`); err != nil {
		t.Fatal(err)
	}
	if err := s.FlushEvents(ctx); err != nil {
		t.Fatal(err)
	}
	var rows, x0 int
	_ = dbpkg.DB.QueryRow(`SELECT COUNT(1), SUM(CASE WHEN element='x0' AND x=0 AND y=0 THEN clicks END) FROM analytics_heat`).Scan(&rows, &x0)
	if rows != heatCap || x0 != 2 {
		t.Errorf("%d counts written, x0's first tenth %d; want %d and 2", rows, x0, heatCap)
	}
}
