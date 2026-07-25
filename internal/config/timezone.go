package config

import (
	"strings"
	"sync/atomic"
	"time"
)

// timezone.go — the site's display timezone.
//
// Every timestamp VayuPress stores is UTC, which is correct: it is unambiguous,
// survives a server move and never shifts under daylight saving. But everything
// VayuPress *displayed* was UTC too, so an operator whose own clock is IST
// (UTC+5:30) read every admin timestamp 5½ hours behind their wall clock, and a
// post published at 01:00 local time showed the PREVIOUS day's date to readers —
// the "dates and times never match my system" mismatch.
//
// The fix is a single display timezone, resolved once and read everywhere:
// storage stays UTC, presentation is converted at the edge of rendering. An empty
// setting means UTC, so an install that never configures this behaves exactly as
// before.

// siteLoc holds the resolved display location. Read on nearly every rendered
// timestamp, written rarely (boot + a settings save), so an atomic pointer keeps
// reads lock-free. A nil value means "not configured" → UTC.
var siteLoc atomic.Pointer[time.Location]

// SetSiteTimeZone resolves an IANA timezone name (e.g. "Asia/Kolkata",
// "Europe/London") and installs it as the display timezone. An empty name — or
// "UTC" — selects UTC. An unresolvable name is rejected and the previous setting
// is kept, so a typo can never leave timestamps unrenderable.
//
// It returns the error from the zone database so a caller can surface "that isn't
// a valid timezone" instead of silently falling back.
func SetSiteTimeZone(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "utc") {
		siteLoc.Store(time.UTC)
		return nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return err
	}
	siteLoc.Store(loc)
	return nil
}

// SiteLocation returns the display timezone, never nil (UTC until configured).
func SiteLocation() *time.Location {
	if loc := siteLoc.Load(); loc != nil {
		return loc
	}
	return time.UTC
}

// InSite converts a stored (UTC) instant into the site's display timezone. It is
// the single conversion every renderer should go through, so there is exactly one
// place where "stored UTC" becomes "what the operator sees".
func InSite(t time.Time) time.Time { return t.In(SiteLocation()) }

// FormatSite formats a stored instant in the site timezone using layout.
func FormatSite(t time.Time, layout string) string {
	return InSite(t).Format(layout)
}

// FormatSiteStamp renders a full "when did this happen" stamp in the site
// timezone WITH its zone label, replacing the hard-coded `…Format(…) + " UTC"`
// pattern that made every admin timestamp read as UTC.
func FormatSiteStamp(t time.Time) string {
	lt := InSite(t)
	name, _ := lt.Zone()
	if name == "" {
		name = "UTC"
	}
	return lt.Format("2006-01-02 15:04") + " " + name
}
