// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/vayushield"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
)

// A site's Security shows that site's refusals alone, with the request id of
// each; events recorded before events named their site appear only in the
// install's trail.
func TestASitesSecurityIsThatSitesAlone(t *testing.T) {
	openMigratedDB(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO shield_events(action,path,reason,domain_id,request_id) VALUES('block','/s1-probe','scanner path','s1','req-s1')`,
		`INSERT INTO shield_events(action,path,reason,domain_id,request_id) VALUES('block','/s2-probe','scanner path','s2','req-s2')`,
		`INSERT INTO shield_events(action,path,reason) VALUES('block','/before-sites','old rule')`,
		`INSERT INTO shield_events(action,challenge_type,outcome,domain_id) VALUES('challenge','pow','issued','s1')`,
	} {
		if _, err := dbpkg.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	store := botdb.New(dbpkg.DB)
	a := &App{vayuShield: vayushield.New(vayushield.Config{Enabled: true, Bots: store})}
	page := a.renderSiteShieldBody(ctx, "s1", "s1", "one.test", 24)
	for _, want := range []string{"/s1-probe", "req-s1", "1 request refused on one.test", ">Challenged<"} {
		if !strings.Contains(page, want) {
			t.Errorf("site one's Security has no %q", want)
		}
	}
	for _, leak := range []string{"/s2-probe", "req-s2", "/before-sites"} {
		if strings.Contains(page, leak) {
			t.Errorf("site one's Security shows %q", leak)
		}
	}
	all, err := store.ReadTrailScoped(ctx, botdb.AllSites, 24, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if all.TotalBlocks != 3 {
		t.Errorf("the install's trail counts %d refusals, want all 3, the one with no site among them", all.TotalBlocks)
	}
	for _, not := range []string{`class="card`, "style="} {
		if strings.Contains(page, not) {
			t.Errorf("the page carries %q", not)
		}
	}
	// The one HTMX the page carries is the feed refreshing itself: a GET of
	// this site's feed, nothing that writes.
	const poller = `hx-get="/os/d/s1/shield/feed" hx-trigger="every 15s" hx-swap="innerHTML"`
	if strings.Count(page, "hx-") != 3 || !strings.Contains(page, poller) {
		t.Errorf("the page's HTMX is not the feed's poller alone")
	}
}

// The page says which gate acted, on what, what deciding cost, and the
// readers beside the refusals, and its feed refreshes alone.
func TestASitesSecurityNamesGatesCostAndReaders(t *testing.T) {
	openMigratedDB(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO shield_events(action,layer,rule_id,classification,eval_us,path,reason,domain_id,request_id) VALUES('block','operator_rule','line 1','BadBot',120,'/wp-login.php','operator rule line 1: block path^=/wp-','s1','req-1')`,
		`INSERT INTO shield_events(action,layer,classification,eval_us,path,reason,domain_id) VALUES('block','block','Headless',480,'/x','bot_score>=block_threshold','s1')`,
		`INSERT INTO shield_events(action,outcome,layer,eval_us,domain_id) VALUES('challenge','issued','challenge',300,'s1')`,
		`INSERT INTO analytics_sessions(id,visitor_id,domain_id) VALUES('v1','r1','s1')`,
		`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,domain_id,created_at) VALUES('p1','v1','/a',1,'s1',CURRENT_TIMESTAMP),('p2','v1','/b',1,'s1',CURRENT_TIMESTAMP),('p3','v1','/b',1,'s2',CURRENT_TIMESTAMP)`,
	} {
		if _, err := dbpkg.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	store := botdb.New(dbpkg.DB)
	a := &App{vayuShield: vayushield.New(vayushield.Config{Enabled: true, Bots: store}), analytics: analytics.New(dbpkg.DB)}
	page := a.renderSiteShieldBody(ctx, "s1", "s1", "one.test", 24)
	hour := time.Now().UTC().Format("2006-01-02 15:00")
	for _, want := range []string{
		"Which gate acted", "Your request rules", "The ladder&#39;s refusal", "The challenge ladder",
		"What was refused", "BadBot", "Headless",
		">480<span", "Deciding, p95",
		"Readers beside refusals", "<td class=\"muted\">" + hour + "</td><td>2</td><td>2</td><td>1</td>",
		"no list of networks here",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page has no %q", want)
		}
	}
	// A week is set out by day.
	week := a.renderSiteShieldBody(ctx, "s1", "s1", "one.test", 24*7)
	if day := time.Now().UTC().Format("2006-01-02"); !strings.Contains(week, "<td class=\"muted\">"+day+"</td><td>2</td><td>2</td><td>1</td>") {
		t.Errorf("a week's readers beside refusals is not by day")
	}
}

// The feed the page polls is that site's newest refusals alone.
func TestASitesFeedIsItsOwnRefusals(t *testing.T) {
	openMigratedDB(t)
	for _, q := range []string{
		`INSERT INTO shield_events(action,path,reason,domain_id,request_id) VALUES('block','/mine','r','s1','req-mine')`,
		`INSERT INTO shield_events(action,path,reason,domain_id,request_id) VALUES('block','/theirs','r','s2','req-theirs')`,
	} {
		if _, err := dbpkg.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{vayuShield: vayushield.New(vayushield.Config{Enabled: true, Bots: botdb.New(dbpkg.DB)})}
	r := httptest.NewRequest(http.MethodGet, "/os/d/s1/shield/feed", nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxScopedDomainKey, domain.Domain{ID: "s1", Host: "one.test"}))
	w := httptest.NewRecorder()
	a.handleOSScopedShieldFeed(w, r)
	if body := w.Body.String(); !strings.Contains(body, "req-mine") || strings.Contains(body, "req-theirs") {
		t.Errorf("the feed is not site one's alone:\n%s", body)
	}
}
