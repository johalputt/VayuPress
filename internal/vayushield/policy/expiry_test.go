// SPDX-License-Identifier: Apache-2.0

package policy

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// An entry with an end applies up to that moment and not after, and the end
// is reported as when the set next changes.
func TestAnEntryAppliesUntilItsEnd(t *testing.T) {
	c := Config{DenyCIDRs: []string{"198.51.100.0/24 until 2026-10-23T13:00"}}
	c.Now = t0
	r, bad := Compile(c)
	if len(bad) != 0 || r.Source("198.51.100.9") != VerdictDeny {
		t.Fatalf("before its end: verdict %v, issues %v; want deny", r.Source("198.51.100.9"), bad)
	}
	if want := t0.Add(time.Hour); !r.NextExpiry().Equal(want) {
		t.Errorf("next expiry %v, want %v", r.NextExpiry(), want)
	}
	c.Now = t0.Add(time.Hour)
	r, _ = Compile(c)
	if r.Source("198.51.100.9") != VerdictNone {
		t.Error("the entry still applied at its end")
	}
	if len(r.Expired()) != 1 || !strings.Contains(r.Expired()[0], "198.51.100.0/24") || !r.NextExpiry().IsZero() {
		t.Errorf("expired %v, next %v; want the entry named and nothing ahead", r.Expired(), r.NextExpiry())
	}
}

// Every list takes an end, a date alone is midnight UTC, and the soonest end
// is the one reported.
func TestEveryListTakesAnEndAndTheSoonestIsNext(t *testing.T) {
	c := Config{
		AllowCIDRs:         []string{"203.0.113.7 until 2026-10-25"},
		DenyCountries:      []string{"ZZ until 2026-10-24 06:30"},
		ChallengeCountries: []string{"YY until 2026-10-23T11:00"},
		Now:                t0,
	}
	r, bad := Compile(c)
	if len(bad) != 0 {
		t.Fatal(bad)
	}
	if r.Source("203.0.113.7") != VerdictAllow || r.Country("ZZ") != VerdictDeny {
		t.Error("an entry before its end did not apply")
	}
	if r.Country("YY") != VerdictNone || len(r.Expired()) != 1 {
		t.Errorf("an ended country entry applied, or was not named: %v", r.Expired())
	}
	if want := time.Date(2026, 10, 24, 6, 30, 0, 0, time.UTC); !r.NextExpiry().Equal(want) {
		t.Errorf("next expiry %v, want %v", r.NextExpiry(), want)
	}
}

// An end that cannot be read leaves the entry out and names it; it is never
// taken as no end at all, which would make a temporary refusal permanent.
func TestAnUnreadableEndIsNamedAndNotApplied(t *testing.T) {
	r, bad := Compile(Config{DenyCIDRs: []string{"192.0.2.0/24 until next week"}, Now: t0})
	if r.Source("192.0.2.1") != VerdictNone {
		t.Error("an entry whose end cannot be read was applied")
	}
	if len(bad) != 1 || !strings.Contains(bad[0], "until next week") {
		t.Errorf("issues %v; want the entry named", bad)
	}
}
