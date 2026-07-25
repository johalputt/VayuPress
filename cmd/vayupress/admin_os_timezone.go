package main

// admin_os_timezone.go — the display-timezone picker for VayuOS → Settings →
// General.
//
// The list is a curated set of IANA zones covering the major offsets rather than
// the full ~600-entry database: a long list is unusable in a <select>, and the
// value is validated against the real zone database when it is applied
// (config.SetSiteTimeZone), so a zone outside this list still works if it is set
// via the API or carried over from another install — it is simply appended to the
// list so it round-trips instead of silently resetting to UTC.

import (
	"html"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

// commonTimezones are the offered zones, grouped roughly west→east so the list
// reads predictably. "" is UTC (the default for an unconfigured install).
var commonTimezones = []struct{ Value, Label string }{
	{"", "UTC (default)"},
	{"Pacific/Honolulu", "Pacific/Honolulu"},
	{"America/Anchorage", "America/Anchorage"},
	{"America/Los_Angeles", "America/Los_Angeles"},
	{"America/Denver", "America/Denver"},
	{"America/Chicago", "America/Chicago"},
	{"America/New_York", "America/New_York"},
	{"America/Toronto", "America/Toronto"},
	{"America/Mexico_City", "America/Mexico_City"},
	{"America/Bogota", "America/Bogota"},
	{"America/Sao_Paulo", "America/Sao_Paulo"},
	{"America/Argentina/Buenos_Aires", "America/Argentina/Buenos_Aires"},
	{"Atlantic/Reykjavik", "Atlantic/Reykjavik"},
	{"Europe/London", "Europe/London"},
	{"Europe/Dublin", "Europe/Dublin"},
	{"Europe/Lisbon", "Europe/Lisbon"},
	{"Europe/Paris", "Europe/Paris"},
	{"Europe/Madrid", "Europe/Madrid"},
	{"Europe/Berlin", "Europe/Berlin"},
	{"Europe/Amsterdam", "Europe/Amsterdam"},
	{"Europe/Rome", "Europe/Rome"},
	{"Europe/Warsaw", "Europe/Warsaw"},
	{"Europe/Athens", "Europe/Athens"},
	{"Europe/Kyiv", "Europe/Kyiv"},
	{"Europe/Istanbul", "Europe/Istanbul"},
	{"Europe/Moscow", "Europe/Moscow"},
	{"Africa/Casablanca", "Africa/Casablanca"},
	{"Africa/Lagos", "Africa/Lagos"},
	{"Africa/Cairo", "Africa/Cairo"},
	{"Africa/Johannesburg", "Africa/Johannesburg"},
	{"Africa/Nairobi", "Africa/Nairobi"},
	{"Asia/Jerusalem", "Asia/Jerusalem"},
	{"Asia/Riyadh", "Asia/Riyadh"},
	{"Asia/Dubai", "Asia/Dubai"},
	{"Asia/Karachi", "Asia/Karachi"},
	{"Asia/Kolkata", "Asia/Kolkata (IST)"},
	{"Asia/Colombo", "Asia/Colombo"},
	{"Asia/Kathmandu", "Asia/Kathmandu"},
	{"Asia/Dhaka", "Asia/Dhaka"},
	{"Asia/Bangkok", "Asia/Bangkok"},
	{"Asia/Jakarta", "Asia/Jakarta"},
	{"Asia/Singapore", "Asia/Singapore"},
	{"Asia/Hong_Kong", "Asia/Hong_Kong"},
	{"Asia/Shanghai", "Asia/Shanghai"},
	{"Asia/Manila", "Asia/Manila"},
	{"Asia/Seoul", "Asia/Seoul"},
	{"Asia/Tokyo", "Asia/Tokyo"},
	{"Australia/Perth", "Australia/Perth"},
	{"Australia/Adelaide", "Australia/Adelaide"},
	{"Australia/Brisbane", "Australia/Brisbane"},
	{"Australia/Sydney", "Australia/Sydney"},
	{"Pacific/Auckland", "Pacific/Auckland"},
}

// timezoneOffsetLabel renders the zone's current UTC offset (e.g. "UTC+05:30")
// so an operator can pick by offset without knowing IANA names. Returns "" when
// the zone cannot be resolved on this host.
func timezoneOffsetLabel(name string) string {
	if strings.TrimSpace(name) == "" {
		return "UTC+00:00"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return ""
	}
	_, offset := time.Now().In(loc).Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	h, m := offset/3600, (offset%3600)/60
	return "UTC" + sign + twoDigitPad(h) + ":" + twoDigitPad(m)
}

func twoDigitPad(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// timezoneOptionsHTML renders the <option> list with current selected. A
// configured zone outside the curated list is appended so it round-trips rather
// than silently resetting to UTC on the next save.
func timezoneOptionsHTML(selected string) string {
	selected = strings.TrimSpace(selected)
	known := false
	var b strings.Builder
	for _, tz := range commonTimezones {
		if tz.Value == selected {
			known = true
		}
		label := tz.Label
		if off := timezoneOffsetLabel(tz.Value); off != "" && tz.Value != "" {
			label += " · " + off
		}
		sel := ""
		if tz.Value == selected {
			sel = " selected"
		}
		b.WriteString(`<option value="` + html.EscapeString(tz.Value) + `"` + sel + `>` +
			html.EscapeString(label) + `</option>`)
	}
	if !known && selected != "" {
		label := selected
		if off := timezoneOffsetLabel(selected); off != "" {
			label += " · " + off
		}
		b.WriteString(`<option value="` + html.EscapeString(selected) + `" selected>` +
			html.EscapeString(label) + `</option>`)
	}
	return b.String()
}

// currentSiteTimeLine is a short "what time is it here" line for the settings UI.
func currentSiteTimeLine() string {
	return config.FormatSiteStamp(time.Now())
}
