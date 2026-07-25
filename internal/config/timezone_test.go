package config

import (
	"strings"
	"testing"
	"time"
)

// TestSiteTimeZoneDefaultsToUTC: an install that never configures a timezone must
// behave exactly as before (everything UTC).
func TestSiteTimeZoneDefaultsToUTC(t *testing.T) {
	t.Cleanup(func() { _ = SetSiteTimeZone("") })
	if err := SetSiteTimeZone(""); err != nil {
		t.Fatalf("empty name should select UTC, got %v", err)
	}
	if SiteLocation() != time.UTC {
		t.Errorf("SiteLocation = %v, want UTC", SiteLocation())
	}
}

// TestSiteTimeZoneConvertsDisplay is the core of the fix: a stored UTC instant
// must render as the operator's local wall-clock time. 20:00 UTC is 01:30 the
// NEXT day in IST — the exact case where a UTC render showed the wrong date.
func TestSiteTimeZoneConvertsDisplay(t *testing.T) {
	t.Cleanup(func() { _ = SetSiteTimeZone("") })
	if err := SetSiteTimeZone("Asia/Kolkata"); err != nil {
		t.Skipf("zone database unavailable: %v", err)
	}
	stored := time.Date(2026, 7, 25, 20, 0, 0, 0, time.UTC)

	if got := FormatSite(stored, "2006-01-02 15:04"); got != "2026-07-26 01:30" {
		t.Errorf("FormatSite = %q, want 2026-07-26 01:30 (IST is UTC+5:30)", got)
	}
	// The date itself rolls over — this is what readers saw as "wrong day".
	if got := FormatSite(stored, "2006-01-02"); got != "2026-07-26" {
		t.Errorf("date = %q, want 2026-07-26", got)
	}
	// The rendered stamp must carry the REAL zone label, never a hard-coded UTC.
	stamp := FormatSiteStamp(stored)
	if strings.HasSuffix(stamp, "UTC") {
		t.Errorf("stamp %q must not claim UTC when the site is on IST", stamp)
	}
	if !strings.HasPrefix(stamp, "2026-07-26 01:30") {
		t.Errorf("stamp = %q", stamp)
	}
}

// TestSiteTimeZoneRejectsInvalidAndKeepsPrevious: a typo must not leave the site
// unrenderable — the previous zone is kept and the error is returned to surface.
func TestSiteTimeZoneRejectsInvalidAndKeepsPrevious(t *testing.T) {
	t.Cleanup(func() { _ = SetSiteTimeZone("") })
	if err := SetSiteTimeZone("Asia/Kolkata"); err != nil {
		t.Skipf("zone database unavailable: %v", err)
	}
	before := SiteLocation().String()
	if err := SetSiteTimeZone("Not/AZone"); err == nil {
		t.Fatal("an unresolvable zone must be rejected")
	}
	if got := SiteLocation().String(); got != before {
		t.Errorf("location = %q, want the previous zone %q kept", got, before)
	}
}

// TestSiteTimeZoneStorageIsUntouched: converting for display must never change
// the instant itself — the stored value stays the same moment in time.
func TestSiteTimeZoneStorageIsUntouched(t *testing.T) {
	t.Cleanup(func() { _ = SetSiteTimeZone("") })
	if err := SetSiteTimeZone("Asia/Kolkata"); err != nil {
		t.Skipf("zone database unavailable: %v", err)
	}
	stored := time.Date(2026, 7, 25, 20, 0, 0, 0, time.UTC)
	if !InSite(stored).Equal(stored) {
		t.Error("InSite must only change the location, never the instant")
	}
}
