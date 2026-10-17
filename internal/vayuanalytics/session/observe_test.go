// SPDX-License-Identifier: Apache-2.0

package session

import (
	"testing"
	"time"
)

var day1 = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

func at(h, m, s int) time.Time {
	return day1.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second)
}

// A visit is 30 minutes of inactivity, not a clock bucket: 09:29:50 and
// 09:30:10 are one visit, and 40 minutes of silence opens the next.
func TestAVisitEndsAfterThirtyIdleMinutes(t *testing.T) {
	h := NewMasterHasher([]byte("k"))
	a := h.Observe("site", "1.1.1.1", "UA", "en", "/b", true, at(9, 29, 50))
	b := h.Observe("site", "1.1.1.1", "UA", "en", "/c", true, at(9, 30, 10))
	c := h.Observe("site", "1.1.1.1", "UA", "en", "/b", true, at(10, 10, 10))
	if b.Session != a.Session {
		t.Errorf("across the half-hour: %+v then %+v, want one visit", a, b)
	}
	if c.Session == a.Session {
		t.Errorf("after 40 idle minutes: %+v, want a new visit", c)
	}
}

// A visit that crosses midnight stays one visit, under the visitor hash of
// the day it began, though the day's salt has rotated under it.
func TestAVisitCrossingMidnightIsOneVisit(t *testing.T) {
	h := NewMasterHasher([]byte("k"))
	a := h.Observe("site", "1.1.1.1", "UA", "en", "/", true, at(23, 59, 50))
	b := h.Observe("site", "1.1.1.1", "UA", "en", "/a", true, at(24, 0, 10))
	if b.Session != a.Session || b.Visitor != a.Visitor {
		t.Errorf("past midnight: %+v, want the visit begun at 23:59:50 (%+v)", b, a)
	}
	if fresh := h.Visitor("1.1.1.1", "UA", "en", at(24, 0, 10)); fresh == a.Visitor {
		t.Error("the new day's visitor hash equals yesterday's: the salt did not rotate")
	}
}

// A visit belongs to one site: the same reader on a second site makes a
// second visit there, entered there.
func TestAVisitBelongsToOneSite(t *testing.T) {
	h := NewMasterHasher([]byte("k"))
	a := h.Observe("one", "1.1.1.1", "UA", "en", "/", true, at(9, 0, 0))
	b := h.Observe("two", "1.1.1.1", "UA", "en", "/", true, at(9, 0, 5))
	if b.Session == a.Session {
		t.Errorf("the second site: %+v, want its own visit (first: %+v)", b, a)
	}
}

// The same view twice within two seconds is one view delivered twice; the
// same page later, or another page, is a view of its own; an event that is
// not a view is never a repeat.
func TestTheSameViewTwiceIsARepeat(t *testing.T) {
	h := NewMasterHasher([]byte("k"))
	h.Observe("s", "1.1.1.1", "UA", "en", "/a", true, at(9, 4, 0))
	if r := h.Observe("s", "1.1.1.1", "UA", "en", "/a", true, at(9, 4, 0)); !r.Repeat {
		t.Error("the same view in the same second is not a repeat")
	}
	if r := h.Observe("s", "1.1.1.1", "UA", "en", "/a", true, at(9, 4, 3)); r.Repeat {
		t.Error("the same page three seconds later is a repeat")
	}
	if r := h.Observe("s", "1.1.1.1", "UA", "en", "/b", true, at(9, 4, 3)); r.Repeat {
		t.Error("another page is a repeat")
	}
	if r := h.Observe("s", "1.1.1.1", "UA", "en", "/b", false, at(9, 4, 3)); r.Repeat {
		t.Error("an event that is not a view is a repeat")
	}
}

// Without entropy no identifier is derived and the event is refused, rather
// than hashed under a salt anyone could reproduce.
func TestNoSaltNoIdentity(t *testing.T) {
	h := NewHasher()
	now := time.Now().UTC()
	h.mu.Lock()
	h.salt, h.day = nil, now.Format("2006-01-02")
	h.mu.Unlock()
	if hit := h.Observe("s", "1.1.1.1", "UA", "en", "/", true, now); hit != (Hit{}) {
		t.Errorf("with no salt: %+v, want the zero Hit", hit)
	}
	if v := h.Visitor("1.1.1.1", "UA", "en", now); v != "" {
		t.Errorf("with no salt the visitor hash is %q, want none", v)
	}
}
