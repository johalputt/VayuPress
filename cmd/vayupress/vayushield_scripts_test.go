// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

// sendCSPReport posts a browser's report of a refused script from ip.
func sendCSPReport(a *App, ip, blocked string) {
	body := `{"csp-report":{"document-uri":"https://` + config.Cfg.Domain + `/a-post","violated-directive":"script-src-elem","blocked-uri":"` + blocked + `"}}`
	r := httptest.NewRequest(http.MethodPost, "/csp-report", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/csp-report")
	r.RemoteAddr = ip + ":4000"
	a.handleCSPReport(httptest.NewRecorder(), r)
}

func freshCSPBlocks(t *testing.T) {
	t.Helper()
	cspBlocksMu.Lock()
	old := cspBlocks
	cspBlocks = map[string]*cspBlock{}
	cspBlocksMu.Unlock()
	t.Cleanup(func() {
		cspBlocksMu.Lock()
		cspBlocks = old
		cspBlocksMu.Unlock()
	})
}

// A source is listed once however often it is reported, with how often;
// one visitor's reports alone list nothing, since anyone can send one.
func TestARefusedScriptIsListedOnceHoweverOftenItIsReported(t *testing.T) {
	freshCSPBlocks(t)
	a, _ := shieldSitesApp(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		sendCSPReport(a, "198.51.100.7", "https://skimmer.example/cdn/pay.js")
	}
	if got := a.refusedScripts(ctx); len(got) != 0 {
		t.Fatalf("one visitor's reports listed %d source(s)", len(got))
	}
	sendCSPReport(a, "198.51.100.8", "https://skimmer.example/cdn/other.js")
	for _, ip := range []string{"198.51.100.7", "198.51.100.8"} {
		recordCSPBlock("https://"+config.Cfg.Domain+"/a-post", "style-src", "https://fonts.example/a.css", ip, time.Now())
	}
	got := a.refusedScripts(ctx)
	if len(got) != 1 || got[0].Origin != "https://skimmer.example" || got[0].Count != 4 {
		t.Fatalf("reported four times from two visitors, listed as %+v", got)
	}
	if body := a.scriptsSectionBody(ctx); strings.Count(body, "https://skimmer.example") != 1 || !strings.Contains(body, `href="/os/website/services"`) {
		t.Fatalf("the Shield section does not list the source once with where to allow it: %s", body)
	}
}

// The bell says a source is new for a day after its first report, then
// stops.
func TestTheBellSaysANewScriptSourceForADay(t *testing.T) {
	freshCSPBlocks(t)
	a, _ := shieldSitesApp(t)
	ctx := context.Background()
	sendCSPReport(a, "198.51.100.7", "https://skimmer.example/pay.js")
	sendCSPReport(a, "198.51.100.8", "https://skimmer.example/pay.js")
	now := time.Now()
	n := a.scriptNotices(ctx, now)
	if len(n) != 1 || n[0].Count != 1 || n[0].Href != "/os/shield#vs-body-scripts" {
		t.Fatalf("a new source raised %+v", n)
	}
	// Reported again a day later, it is still the same source, not a new one.
	later := now.Add(scriptNewFor + time.Minute)
	recordCSPBlock("https://"+config.Cfg.Domain+"/a-post", "script-src", "https://skimmer.example/pay.js", "198.51.100.9", later)
	if n := a.scriptNotices(ctx, later); len(n) != 0 {
		t.Fatalf("a day later the bell still says %+v", n)
	}
}

// The console's bell carries it, for an administrator.
func TestTheBellListsANewScriptSource(t *testing.T) {
	freshCSPBlocks(t)
	a, _ := shieldSitesApp(t)
	sendCSPReport(a, "198.51.100.7", "https://skimmer.example/pay.js")
	sendCSPReport(a, "198.51.100.8", "https://skimmer.example/pay.js")
	for _, n := range a.osNotifications(context.Background(), &osSettings{AccessLevel: accessAdmin}) {
		if n.Href == "/os/shield#vs-body-scripts" {
			return
		}
	}
	t.Fatal("the bell does not list a newly refused script")
}
