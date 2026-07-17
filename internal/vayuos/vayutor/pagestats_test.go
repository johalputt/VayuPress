package vayutor

import (
	"context"
	"testing"
)

func newPageStatsEngine(on *bool) (*Engine, *memStore) {
	store := newMemStore()
	e := NewEngine(Config{
		Enabled:   true,
		Store:     store,
		PageStats: func() bool { return on != nil && *on },
	})
	return e, store
}

func TestIncPageDisabledIsNoop(t *testing.T) {
	off := false
	e, _ := newPageStatsEngine(&off)
	e.IncPage("johal.in", "/best-vpn")
	e.IncPage("johal.in", "/best-vpn")
	if got := e.topPages(10); len(got) != 0 {
		t.Fatalf("disabled page stats recorded hits: %v", got)
	}
	// Snapshot must not expose any pages while off.
	if st := e.Snapshot(); st.PageStatsOn || len(st.TopPages) != 0 {
		t.Fatalf("snapshot leaked page stats while off: on=%v pages=%v", st.PageStatsOn, st.TopPages)
	}
}

func TestIncPageAggregatesAndRanks(t *testing.T) {
	on := true
	e, _ := newPageStatsEngine(&on)
	for i := 0; i < 3; i++ {
		e.IncPage("johal.in", "/popular")
	}
	e.IncPage("johal.in", "/rare")
	e.IncPage("vayupress.com", "/docs")
	e.IncPage("vayupress.com", "/docs")

	top := e.topPages(10)
	if len(top) != 3 {
		t.Fatalf("want 3 tracked pages, got %d: %v", len(top), top)
	}
	if top[0].Host != "johal.in" || top[0].Path != "/popular" || top[0].Count != 3 {
		t.Errorf("top page = %+v, want johal.in /popular x3", top[0])
	}
	if top[1].Count != 2 || top[1].Host != "vayupress.com" {
		t.Errorf("second page = %+v, want vayupress.com /docs x2", top[1])
	}
	// topPages respects the cap.
	if got := e.topPages(1); len(got) != 1 || got[0].Path != "/popular" {
		t.Errorf("topPages(1) = %v, want just /popular", got)
	}
}

func TestPageKeyNormalisation(t *testing.T) {
	// Host is lowercased; a blank path becomes "/"; embedded whitespace collapses.
	if k := pageKey("Johal.IN", "/A B"); k != "johal.in /A B" {
		t.Errorf("pageKey host-lower/space = %q", k)
	}
	if k := pageKey("johal.in", ""); k != "johal.in /" {
		t.Errorf("pageKey empty path = %q, want 'johal.in /'", k)
	}
	if k := pageKey("", "/x"); k != "" {
		t.Errorf("pageKey empty host should be dropped, got %q", k)
	}
	h, p := splitPageKey("johal.in /best-vpn")
	if h != "johal.in" || p != "/best-vpn" {
		t.Errorf("splitPageKey = %q,%q", h, p)
	}
}

func TestPageHitsCardinalityBounded(t *testing.T) {
	on := true
	e, _ := newPageStatsEngine(&on)
	// Exceed the cap with unique paths; existing keys must still count, new ones
	// beyond the cap are dropped (aggregate popularity tolerates this).
	for i := 0; i < maxTrackedPages+50; i++ {
		e.IncPage("h", "/p"+itoa(i))
	}
	e.pageMu.Lock()
	n := len(e.pageHits)
	e.pageMu.Unlock()
	if n > maxTrackedPages {
		t.Fatalf("pageHits grew past cap: %d > %d", n, maxTrackedPages)
	}
	// A path recorded before the cap keeps incrementing.
	e.IncPage("h", "/p0")
	e.IncPage("h", "/p0")
	for _, ph := range e.topPages(maxTrackedPages) {
		if ph.Path == "/p0" && ph.Count < 3 {
			t.Errorf("/p0 count = %d, want >= 3", ph.Count)
		}
	}
}

func TestResetPageHits(t *testing.T) {
	on := true
	e, store := newPageStatsEngine(&on)
	e.IncPage("johal.in", "/x")
	e.flushVisits(context.Background()) // persist
	if got := store.LoadPageHits(context.Background()); len(got) != 1 {
		t.Fatalf("expected 1 persisted hit, got %v", got)
	}
	e.ResetPageHits()
	if got := e.topPages(10); len(got) != 0 {
		t.Errorf("topPages after reset = %v, want empty", got)
	}
	if got := store.LoadPageHits(context.Background()); len(got) != 0 {
		t.Errorf("store after reset = %v, want empty", got)
	}
}

// itoa avoids importing strconv just for the cardinality test.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
