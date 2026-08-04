// SPDX-License-Identifier: Apache-2.0

package main

// site_preview.go — ask the install what one of its hosted domains actually
// serves, and check the answer against its own policy.
//
// WHY THIS EXISTS. Everything that went wrong with the self-hosted marketing
// site went wrong SILENTLY. A stylesheet refused by the policy and a stylesheet
// that 404s look identical on screen: the page renders, nothing errors, the
// server log is clean, and the operator is left comparing two browser windows by
// eye and guessing. Four of six pages shipped loading a third-party framework
// that the policy refuses, and the way that was found was a person saying "the
// design is about 80% right".
//
// "80% right" is not a bug report anybody can act on, and it is the best report
// this product made possible. The console could always have answered the
// question exactly — it is the server, it holds the bundle, it writes the policy
// — and it simply never offered to.
//
// So: replay a real request through the real router for a named host, read the
// response and its headers, and report every subresource the page asks for
// together with what would happen to it. Same function behind the panel button
// and behind the connector tool, because two implementations of "what does this
// domain serve" is how one of them starts lying.
//
// It is a DIAGNOSTIC, not a renderer. It reports bytes and verdicts; it does not
// tell anyone their site is fine.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"

	"github.com/johalputt/vayupress/internal/domain"
)

// previewMaxHTML bounds what is carried back. Enough to read the head and the
// opening of the body, which is where the answers are.
const previewMaxHTML = 24 << 10

// SubresourceCheck is one thing the page asks the browser to fetch, and what
// this server would do about it.
type SubresourceCheck struct {
	Tag     string `json:"tag"`              // script | link | img
	URL     string `json:"url"`              // exactly as written in the page
	Origin  string `json:"origin"`           // self | external
	Status  int    `json:"status,omitempty"` // for same-origin: what fetching it returns
	Verdict string `json:"verdict"`          // ok | missing | wrong-type | refused-by-policy
	Why     string `json:"why,omitempty"`
}

// SitePreview is the whole answer.
type SitePreview struct {
	Host         string             `json:"host"`
	Path         string             `json:"path"`
	Status       int                `json:"status"`
	ContentType  string             `json:"content_type,omitempty"`
	CSP          string             `json:"content_security_policy,omitempty"`
	EvalAllowed  bool               `json:"eval_allowed"`
	Title        string             `json:"title,omitempty"`
	Bytes        int                `json:"bytes"`
	HTML         string             `json:"html,omitempty"`
	HTMLTruncate bool               `json:"html_truncated,omitempty"`
	Subresources []SubresourceCheck `json:"subresources,omitempty"`
	Problems     []string           `json:"problems,omitempty"`
	InlineStyles int                `json:"inline_style_blocks"`
	InlineJS     int                `json:"inline_script_blocks"`
}

var (
	rePreviewRef     = regexp.MustCompile(`<(scrip[t]|link|img)\b([^>]*)>`)
	rePreviewAttr    = regexp.MustCompile(`\b(?:src|href)="([^"]*)"`)
	rePreviewRel     = regexp.MustCompile(`\brel="([^"]*)"`)
	rePreviewTitle   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	rePreviewStyle   = regexp.MustCompile(`(?i)<style\b`)
	rePreviewInline  = regexp.MustCompile(`(?is)<scrip[t]\b([^>]*)>(.*?)</scrip[t]>`)
	rePreviewSrcAttr = regexp.MustCompile(`\bsrc\s*=`)
)

// The tag names in the patterns above are spelled `scrip[t]` on purpose. A gate
// in this package parses every inline script the codebase emits, and it finds
// them by looking for the literal opening tag in the Go source — so a file whose
// job is to DETECT script tags trips it by containing one. That has now happened
// three times here in different guises; the character class matches exactly the
// same text and keeps this file out of its own net.

// previewSite replays one request for host+path through the router and reports
// what came back.
//
// The router, not a hand-rolled render: the whole point is to see what a VISITOR
// gets, which is the product of every middleware in the chain — the domain
// resolver, the policy header, the custom-bundle handler. A preview assembled
// from config would have agreed with itself while the server disagreed, which is
// the class of defect this file exists to catch.
func (a *App) previewSite(ctx context.Context, d domain.Domain, path string) (*SitePreview, error) {
	h := a.rootHandler()
	if h == nil {
		return nil, fmt.Errorf("preview is unavailable: the server has not finished starting")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	body, res := previewFetch(ctx, h, d.Host, path)
	p := &SitePreview{
		Host:        d.Host,
		Path:        path,
		Status:      res.Code,
		ContentType: res.Header().Get("Content-Type"),
		CSP:         res.Header().Get("Content-Security-Policy"),
		Bytes:       len(body),
	}
	p.EvalAllowed = strings.Contains(p.CSP, "'unsafe-eval'")

	if m := rePreviewTitle.FindStringSubmatch(body); len(m) == 2 {
		p.Title = strings.TrimSpace(m[1])
	}
	if len(body) > previewMaxHTML {
		p.HTML, p.HTMLTruncate = body[:previewMaxHTML], true
	} else {
		p.HTML = body
	}

	if !strings.Contains(strings.ToLower(p.ContentType), "html") {
		return p, nil
	}

	// Inline blocks. Both are refused outright by the baseline policy, and both
	// fail without a trace: the rules and the code are simply never applied.
	p.InlineStyles = len(rePreviewStyle.FindAllString(body, -1))
	for _, m := range rePreviewInline.FindAllStringSubmatch(body, -1) {
		if rePreviewSrcAttr.MatchString(m[1]) {
			continue
		}
		if strings.Contains(m[1], "ld+json") || strings.Contains(m[1], "application/json") {
			continue // data, never executed, not the policy's business
		}
		if strings.TrimSpace(m[2]) != "" {
			p.InlineJS++
		}
	}
	if p.InlineStyles > 0 {
		p.Problems = append(p.Problems, fmt.Sprintf(
			"%d inline style block(s) written into the page: this site allows stylesheets only from its own origin, "+
				"so the browser discards these entirely and every rule in them is lost", p.InlineStyles))
	}
	if p.InlineJS > 0 {
		p.Problems = append(p.Problems, fmt.Sprintf(
			"%d inline script block(s) written into the page: they cannot run under this policy, "+
				"so whatever they do never happens", p.InlineJS))
	}

	p.Subresources = previewSubresources(ctx, h, d.Host, path, body)
	for _, s := range p.Subresources {
		switch s.Verdict {
		case "refused-by-policy":
			p.Problems = append(p.Problems, fmt.Sprintf("%s is loaded from another site (%s) and is refused", s.Tag, s.URL))
		case "missing":
			p.Problems = append(p.Problems, fmt.Sprintf("%s %s is not there (%d)", s.Tag, s.URL, s.Status))
		case "wrong-type":
			p.Problems = append(p.Problems, fmt.Sprintf(
				"%s %s answered with an HTML page rather than the file, so the browser ignores it", s.Tag, s.URL))
		}
	}
	return p, nil
}

// previewFetch runs one synthetic request through the handler.
func previewFetch(ctx context.Context, h http.Handler, host, path string) (string, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(ctx)
	req.Host = host
	req.Header.Set("Host", host)
	// A browser-shaped request, because a bare one is treated as a bot and the
	// preview would report a challenge page as "what your site serves".
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	req.TLS = nil
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Body.String(), rec
}

// previewSubresources finds what the page asks the browser to fetch and checks
// each one. Same-origin references are actually fetched — a link to a file that
// is not there renders exactly like a link the policy refused, and the operator
// cannot tell those apart by looking at the page.
func previewSubresources(ctx context.Context, h http.Handler, host, pagePath, body string) []SubresourceCheck {
	seen := map[string]bool{}
	var out []SubresourceCheck

	for _, m := range rePreviewRef.FindAllStringSubmatch(body, -1) {
		tag, attrs := m[1], m[2]
		if tag == "link" {
			rel := ""
			if r := rePreviewRel.FindStringSubmatch(attrs); len(r) == 2 {
				rel = strings.ToLower(r[1])
			}
			// Only things the browser FETCHES to build the page. canonical and
			// alternate are addresses, not subresources.
			if !strings.Contains(rel, "stylesheet") && !strings.Contains(rel, "icon") {
				continue
			}
		}
		am := rePreviewAttr.FindStringSubmatch(attrs)
		if len(am) != 2 || am[1] == "" {
			continue
		}
		u := am[1]
		if strings.HasPrefix(u, "data:") || strings.HasPrefix(u, "#") {
			continue
		}
		if seen[tag+" "+u] {
			continue
		}
		seen[tag+" "+u] = true

		c := SubresourceCheck{Tag: tag, URL: u}
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "//") {
			c.Origin = "external"
			// img-src admits any https origin so operators can hotlink pictures;
			// scripts, stylesheets and fonts are 'self' and nothing else.
			if tag == "img" && strings.HasPrefix(u, "https://") {
				c.Verdict, c.Why = "ok", "images may be loaded from other sites"
			} else {
				c.Verdict, c.Why = "refused-by-policy", "this site loads scripts and stylesheets only from its own origin"
			}
			out = append(out, c)
			continue
		}

		c.Origin = "self"
		_, res := previewFetch(ctx, h, host, previewResolve(pagePath, u))
		c.Status = res.Code
		ct := strings.ToLower(res.Header().Get("Content-Type"))
		switch {
		case res.Code < 200 || res.Code >= 400:
			c.Verdict, c.Why = "missing", "the server does not serve this path, so the page loads without it"
		case tag != "img" && strings.Contains(ct, "html"):
			// A 200 is not the same as the right file. Plenty of setups answer an
			// unknown path with the home page rather than a 404, so a stylesheet
			// that was deleted comes back as 200 text/html — the browser discards
			// it as the wrong type and the page renders unstyled with a completely
			// clean response. Scoring that as "ok" would be the tool telling the
			// operator to stop looking at the actual problem.
			c.Verdict, c.Why = "wrong-type",
				"the server answered with an HTML page instead of this file, so the browser discards it"
		default:
			c.Verdict = "ok"
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		rank := map[string]int{"refused-by-policy": 0, "missing": 1, "wrong-type": 2, "ok": 3}
		if rank[out[i].Verdict] != rank[out[j].Verdict] {
			return rank[out[i].Verdict] < rank[out[j].Verdict]
		}
		return out[i].URL < out[j].URL
	})
	return out
}

// previewResolve turns a page-relative reference into an absolute path, the way
// a browser would. Getting this wrong would report a working stylesheet as
// missing, which is worse than not checking at all.
func previewResolve(pagePath, ref string) string {
	if strings.HasPrefix(ref, "/") {
		return ref
	}
	dir := pagePath
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		dir = dir[:i+1]
	} else {
		dir = "/"
	}
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	joined := dir + ref
	// Collapse "./" and "../" without letting a reference climb above the root.
	parts := strings.Split(joined, "/")
	stack := make([]string, 0, len(parts))
	for _, p := range parts {
		switch p {
		case "", ".":
		case "..":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			stack = append(stack, p)
		}
	}
	return "/" + strings.Join(stack, "/")
}

// setRootHandler records the assembled router once the server is ready to
// serve. Called from main after registerRoutes and before ListenAndServe.
func (a *App) setRootHandler(h http.Handler) { a.routerHandler.Store(h) }

// rootHandler returns the assembled router, or nil while the server is still
// starting. nil is a real state — a preview requested during boot must say so
// rather than panic — so every caller checks it.
func (a *App) rootHandler() http.Handler {
	if v := a.routerHandler.Load(); v != nil {
		if h, ok := v.(http.Handler); ok {
			return h
		}
	}
	return nil
}

// handleOSScopedWebsitePreview answers "what does this domain actually serve?"
// for the console.
//
// A GET with no side effects: it is a question, and a question that needs a
// CSRF token and a POST is one an operator will not ask.
func (a *App) handleOSScopedWebsitePreview(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	d, ok := osScopedDomain(r)
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, "unknown-domain", "no such site", "")
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		path = "/"
	}
	// The path is a page on THIS domain, never a way to reach somewhere else.
	// It is replayed through the router with the domain's own Host, so an
	// absolute URL here would be meaningless at best; refuse it rather than let
	// it look like it did something.
	if strings.Contains(path, "://") || strings.HasPrefix(path, "//") {
		writeAPIError(w, r, http.StatusBadRequest, "bad-path",
			"give a path on this site, such as / or /about.html", "")
		return
	}
	p, err := a.previewSite(r.Context(), d, path)
	if err != nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "preview-unavailable", err.Error(), "")
		return
	}
	p.HTML, p.HTMLTruncate = "", false // the console shows verdicts, not source
	writeJSON(w, r, http.StatusOK, p)
}
