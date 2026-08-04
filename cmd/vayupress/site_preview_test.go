// SPDX-License-Identifier: Apache-2.0

package main

// site_preview_test.go — the diagnostic has to be right, or it is worse than
// nothing.
//
// A preview that says "all good" about a broken page is not a neutral failure:
// it ends the investigation. The operator stops looking, and the thing that was
// actually wrong stays wrong with a clean bill of health attached to it. So
// these cases are mostly about the verdicts, not the plumbing — in particular
// the two states that look identical in a browser and must not look identical
// here: a subresource the policy refuses, and one that is simply not there.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
)

// previewTestHandler serves a tiny site so the checks run against real
// responses rather than a stub that agrees with them.
func previewTestHandler(page string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self'; script-src 'self' 'nonce-x'; img-src 'self' data: https:")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("/assets/present.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write([]byte("body{}"))
	})
	mux.HandleFunc("/sub/here.js", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("void 0;"))
	})
	return mux
}

func runPreview(t *testing.T, page string) *SitePreview {
	t.Helper()
	a := &App{}
	a.setRootHandler(previewTestHandler(page))
	p, err := a.previewSite(context.Background(), testDomainHost("site.example"), "/")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func verdictFor(p *SitePreview, url string) (string, bool) {
	for _, s := range p.Subresources {
		if s.URL == url {
			return s.Verdict, true
		}
	}
	return "", false
}

// The two states a browser renders identically. Telling them apart is the entire
// reason this exists.
func TestThePreviewSeparatesRefusedFromMissing(t *testing.T) {
	p := runPreview(t, `<!doctype html><html><head><title>T</title>
<link rel="stylesheet" href="/assets/present.css">
<link rel="stylesheet" href="/assets/gone.css">
<link rel="stylesheet" href="https://example.net/framework.css">
</head><body>hi</body></html>`)

	for _, c := range []struct{ url, want string }{
		{"/assets/present.css", "ok"},
		{"/assets/gone.css", "missing"},
		{"https://example.net/framework.css", "refused-by-policy"},
	} {
		got, ok := verdictFor(p, c.url)
		if !ok {
			t.Errorf("%s was not checked at all — a subresource the report never mentions is one "+
				"the operator will never look at", c.url)
			continue
		}
		if got != c.want {
			t.Errorf("%s: verdict %q, want %q. Reporting these two states the same way is exactly "+
				"the confusion this tool exists to end", c.url, got, c.want)
		}
	}
}

// Inline blocks are dropped in full and leave no trace anywhere. If the preview
// does not count them, nothing does.
func TestThePreviewCountsInlineBlocksThatCanNeverRun(t *testing.T) {
	p := runPreview(t, `<!doctype html><html><head><title>T</title>
<style>body{color:red}</style>
<script type="application/ld+json">{"@context":"x"}</script>
</head><body>
<script>console.log('never runs')</script>
<script src="/sub/here.js"></script>
</body></html>`)

	if p.InlineStyles != 1 {
		t.Errorf("inline <style> blocks counted: %d, want 1 — every rule in it is silently lost", p.InlineStyles)
	}
	if p.InlineJS != 1 {
		t.Errorf("inline script blocks counted: %d, want 1. JSON-LD must NOT be counted (it is data "+
			"the browser never executes) and the external script must NOT be counted", p.InlineJS)
	}
	joined := strings.Join(p.Problems, " ")
	if !strings.Contains(joined, "style") || !strings.Contains(joined, "script") {
		t.Errorf("the problems list does not mention both kinds of dead block: %v", p.Problems)
	}
}

// A clean page must come back clean, or the tool cries wolf and gets ignored —
// which costs more than not having it.
func TestAPageWithNothingWrongReportsNothingWrong(t *testing.T) {
	p := runPreview(t, `<!doctype html><html><head><title>Fine</title>
<link rel="stylesheet" href="/assets/present.css">
<link rel="canonical" href="https://elsewhere.example/page">
</head><body><script src="/sub/here.js"></script></body></html>`)

	if len(p.Problems) != 0 {
		t.Fatalf("a page with nothing wrong was reported as having problems: %v", p.Problems)
	}
	if p.Title != "Fine" {
		t.Errorf("title = %q", p.Title)
	}
	// canonical is an address, not something the browser fetches to build the
	// page. Flagging it would train the operator to ignore the report.
	if _, found := verdictFor(p, "https://elsewhere.example/page"); found {
		t.Error("a canonical link was reported as a refused subresource")
	}
}

// Whether eval is permitted is read from the header the server actually sent,
// not from the stored setting — those are the two things that must be compared,
// and a preview that reads the setting could never reveal a disagreement.
func TestEvalIsReportedFromTheHeaderThatWasActuallySent(t *testing.T) {
	a := &App{}
	a.setRootHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Security-Policy", "script-src 'self' 'unsafe-eval'")
		_, _ = w.Write([]byte("<html><head><title>x</title></head><body></body></html>"))
	}))
	p, err := a.previewSite(context.Background(), testDomainHost("site.example"), "/")
	if err != nil {
		t.Fatal(err)
	}
	if !p.EvalAllowed {
		t.Fatal("the served policy permits eval and the report says it does not")
	}
	if !strings.Contains(p.CSP, "unsafe-eval") {
		t.Error("the policy actually sent is not carried back, so the verdict cannot be checked")
	}
}

// Relative references have to resolve the way a browser resolves them. Getting
// this wrong reports working files as missing, which sends the operator hunting
// for a bug that is in the tool.
func TestRelativeReferencesResolveTheWayABrowserResolvesThem(t *testing.T) {
	cases := []struct{ page, ref, want string }{
		{"/", "assets/app.js", "/assets/app.js"},
		{"/about.html", "assets/app.js", "/assets/app.js"},
		{"/sponsors/index.html", "../assets/style.css", "/assets/style.css"},
		{"/sponsors/", "../assets/style.css", "/assets/style.css"},
		{"/vayumail/privacy/index.html", "/assets/style.css", "/assets/style.css"},
		{"/vayumail/privacy/index.html", "../../assets/x.css", "/assets/x.css"},
		{"/a/b/c.html", "./d.css", "/a/b/d.css"},
		// A reference cannot climb out of the site root.
		{"/", "../../../etc/passwd", "/etc/passwd"},
	}
	for _, c := range cases {
		if got := previewResolve(c.page, c.ref); got != c.want {
			t.Errorf("from %s, %q resolved to %q, want %q", c.page, c.ref, got, c.want)
		}
	}
}

// The preview must not pretend to work before the server is assembled — a
// report built from a nil handler would describe a site that is not running.
func TestThePreviewRefusesBeforeTheServerIsReady(t *testing.T) {
	a := &App{}
	if _, err := a.previewSite(context.Background(), testDomainHost("site.example"), "/"); err == nil {
		t.Fatal("a preview was produced with no router assembled")
	}
}

func testDomainHost(host string) domain.Domain { return domain.Domain{ID: "d1", Host: host} }

// The case the fixture accidentally created, which turned out to be a real and
// common server behaviour worth its own test: a site that answers an unknown
// path with its home page instead of a 404. The stylesheet request then returns
// 200 — and the browser throws the response away because it is HTML, leaving an
// unstyled page and a completely clean response log.
//
// Scoring that "ok" is the worst thing this tool could do: it would tell the
// operator to stop looking at the exact thing that is wrong.
func TestAnHTMLFallbackForAMissingAssetIsNotReportedAsWorking(t *testing.T) {
	a := &App{}
	a.setRootHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Everything, including /assets/gone.css, answers with the page.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>T</title>` +
			`<link rel="stylesheet" href="/assets/gone.css"></head><body></body></html>`))
	}))
	p, err := a.previewSite(context.Background(), testDomainHost("site.example"), "/")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := verdictFor(p, "/assets/gone.css")
	if !ok {
		t.Fatal("the stylesheet was not checked")
	}
	if got == "ok" {
		t.Fatal("a stylesheet that answered with an HTML page was reported as working. The page " +
			"renders unstyled, every response is a 200, and the operator has just been told to " +
			"look somewhere else.")
	}
	if got != "wrong-type" {
		t.Errorf("verdict %q — it should say what actually happened, not merely that something did", got)
	}
	if len(p.Problems) == 0 {
		t.Error("the summary does not mention it, so it is a verdict nobody reads")
	}
}
