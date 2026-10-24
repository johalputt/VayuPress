// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzCollect posts any body the beacon endpoint could receive through the
// collector the endpoint uses, and holds what is stored to the bounds a
// beacon is promised: no field longer than its cap, every cut on a character
// boundary, no event type a beacon may not send (a search is the server's
// alone), at most maxEventDataProps properties, and the site the server
// gave, never one the body names. Run as a fuzzer with
//
//	go test -run '^$' -fuzz FuzzCollect -fuzztime 60s ./internal/analytics/
func FuzzCollect(f *testing.F) {
	for _, seed := range []string{
		`{"u":"/a?x=1","r":"https://news.example/x","t":"Hello","h":"site.test","event_type":1}`,
		`{"u":"/a","event_type":2,"event_name":"signup","event_data":{"plan":"pro","seats":"3"}}`,
		`{"u":"/a","event_type":4,"event_name":"https://elsewhere.example/x","h":"site.test"}`,
		`{"u":"/files/a.pdf","event_type":5,"event_name":"/files/a.pdf","h":"site.test"}`,
		`{"u":"/a","event_type":3,"event_name":"a search"}`,
		`{"u":"` + strings.Repeat("é", 400) + `","t":"` + strings.Repeat("é", 200) + `","utm_source":"` + strings.Repeat("ü", 80) + `"}`,
		`{"u":"/a","event_type":2,"event_name":"x","event_data":{` + strings.Repeat(`"k":"v",`, 3) + `"z":"` + strings.Repeat("ñ", 300) + `"}}`,
		`{"u":"/a","h":"other.example","event_type":-7,"domain_id":"s2"}`,
	} {
		f.Add([]byte(seed))
	}
	s := newExtStore(f)
	f.Fuzz(func(t *testing.T, body []byte) {
		var req CollectRequest
		if json.Unmarshal(body, &req) != nil {
			return
		}
		if _, err := s.db.Exec(`DELETE FROM analytics_pageviews; DELETE FROM analytics_event_data; DELETE FROM analytics_sessions`); err != nil {
			t.Fatal(err)
		}
		if err := s.Collect(context.Background(), req, "198.51.100.1", "Mozilla/5.0 Firefox/131.0", ""); err != nil {
			t.Fatalf("a beacon that decodes was refused with an error: %v", err)
		}
		rows, err := s.db.Query(`SELECT url_path,url_query,page_title,referrer,hostname,utm_source,utm_medium,utm_campaign,utm_content,utm_term,event_type,event_name,domain_id FROM analytics_pageviews`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		caps := []int{512, 512, 300, 0, 200, 100, 100, 100, 100, 100}
		in := []string{req.URL, req.URL, req.PageTitle, req.Referrer, req.Hostname, req.UTMSource, req.UTMMedium, req.UTMCampaign, req.UTMContent, req.UTMTerm}
		for rows.Next() {
			var f [10]string
			var et int
			var name, site string
			if err := rows.Scan(&f[0], &f[1], &f[2], &f[3], &f[4], &f[5], &f[6], &f[7], &f[8], &f[9], &et, &name, &site); err != nil {
				t.Fatal(err)
			}
			for i, v := range f {
				if caps[i] > 0 && len(v) > caps[i] {
					t.Errorf("field %d stored %d bytes, over its %d", i, len(v), caps[i])
				}
				if utf8.ValidString(in[i]) && !utf8.ValidString(v) {
					t.Errorf("field %d cut inside a character: %q", i, v)
				}
			}
			if et != 1 && et != 2 && et != eventOutbound && et != eventDownload {
				t.Errorf("stored event type %d from a beacon", et)
			}
			if len(name) > 200 || (utf8.ValidString(req.EventName) && !utf8.ValidString(name)) {
				t.Errorf("event name stored as %q", name)
			}
			if site != "" {
				t.Errorf("stored for site %q; the server gave none", site)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		var props int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM analytics_event_data`).Scan(&props); err != nil {
			t.Fatal(err)
		}
		if props > maxEventDataProps {
			t.Errorf("%d properties stored, over %d", props, maxEventDataProps)
		}
		kv, err := s.db.Query(`SELECT property_key, property_value FROM analytics_event_data`)
		if err != nil {
			t.Fatal(err)
		}
		defer kv.Close()
		for kv.Next() {
			var k, v string
			if err := kv.Scan(&k, &v); err != nil {
				t.Fatal(err)
			}
			if len(k) > 100 || len(v) > 500 || !utf8.ValidString(k) && utf8.Valid(body) || !utf8.ValidString(v) && utf8.Valid(body) {
				t.Errorf("property stored as %q=%q", k, v)
			}
		}
		if err := kv.Err(); err != nil {
			t.Fatal(err)
		}
	})
}
