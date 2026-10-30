// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"testing"
)

// Where a link led is kept as a host and a path: never its query, fragment,
// port or user name, and a file on the page's own host by its path alone.
func TestALinksTargetIsAHostAndAPath(t *testing.T) {
	for _, c := range []struct {
		raw      string
		download bool
		want     string
		ok       bool
	}{
		{"https://Example.COM/a/b/?token=s3cret#x", false, "example.com/a/b", true},
		{"https://user:pw@example.com:8443/a", false, "example.com/a", true},
		{"http://example.com", false, "example.com/", true},
		{"https://site.test/files/report.pdf?v=2", true, "/files/report.pdf", true},
		{"https://cdn.test/files/report.pdf", true, "cdn.test/files/report.pdf", true},
		{"ftp://files.test/a.zip", true, "", false},
		{"mailto:someone@example.com", false, "", false},
		{"javascript:alert(1)", false, "", false},
		{"/relative/path", false, "", false},
		{"", false, "", false},
	} {
		got, ok := LinkTarget(c.raw, "site.test", c.download)
		if got != c.want || ok != c.ok {
			t.Errorf("LinkTarget(%q, download %v) = %q, %v; want %q, %v", c.raw, c.download, got, ok, c.want, c.ok)
		}
	}
}

// A link out and a download are counted as their own events, never as
// custom events, and make their visit no bounce; one that is not an http(s)
// link is not recorded.
func TestLinksOutAndDownloadsAreTheirOwnEvents(t *testing.T) {
	s := newExtStore(t)
	ctx := context.Background()
	collect := func(et int, name string) {
		t.Helper()
		req := CollectRequest{URL: "/post", Hostname: "site.test", EventType: et, EventName: name}
		if err := s.Collect(ctx, req, "198.51.100.7", "Mozilla/5.0 Firefox/131.0", "s1"); err != nil {
			t.Fatal(err)
		}
	}
	collect(1, "")
	collect(eventOutbound, "https://example.com/page?ref=me")
	collect(eventDownload, "https://site.test/guide.pdf")
	collect(eventOutbound, "javascript:void(0)")
	got := map[int]string{}
	rows, err := s.db.Query(`SELECT event_type, event_name FROM analytics_pageviews WHERE domain_id='s1' AND event_type<>1`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var et int
		var name string
		if err := rows.Scan(&et, &name); err != nil {
			t.Fatal(err)
		}
		got[et] = name
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	if len(got) != 2 || got[eventOutbound] != "example.com/page" || got[eventDownload] != "/guide.pdf" {
		t.Errorf("the events kept = %v, want the link out and the download alone", got)
	}
	if ev, _ := s.CustomEventsScoped(ctx, "s1", 1); len(ev) != 0 {
		t.Errorf("a link or a download is listed as a custom event: %+v", ev)
	}
	ov, err := s.OverviewSinceScoped(ctx, "s1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if ov.TotalVisits != 1 || ov.BounceRate != 0 {
		t.Errorf("a visit that followed a link = %d visits, %.0f%% bounce; want 1 and no bounce", ov.TotalVisits, ov.BounceRate)
	}
}
