// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// A site's own code for its public pages: what an operator pastes for an
// analytics tag, a chat widget or a verification tag, put in at the end of
// <head> and at the end of <body>. Each inline script runs by its own hash
// in that site's policy (render.ParseSnippet), so nothing else inline is let
// in; a <script src> runs only from an origin the site's outside services
// allow. Only public pages carry it: strictPath keeps it off the console,
// sign-in, member, checkout and mail pages, as it does the policy.

// parsedSnippets keeps each snippet as parsed, by its text: every public
// page asks for its site's two, and they change only when saved.
var parsedSnippets sync.Map

// parsedSnippet is raw as ParseSnippet reads it. A stored snippet that no
// longer parses (written before a rule, or by hand) is left out, never
// served half-checked.
func parsedSnippet(raw string) render.Snippet {
	if strings.TrimSpace(raw) == "" {
		return render.Snippet{}
	}
	if v, ok := parsedSnippets.Load(raw); ok {
		return v.(render.Snippet)
	}
	s, err := render.ParseSnippet(raw)
	if err != nil {
		s = render.Snippet{}
	}
	parsedSnippets.Store(raw, s)
	return s
}

// siteSnippetsIn is the site's head and footer snippets.
func (a *App) siteSnippetsIn(ctx context.Context, sc settings.Scope) (head, foot render.Snippet) {
	if a.siteSettings == nil {
		return
	}
	return parsedSnippet(a.siteSettings.Get(ctx, sc, settings.KeySnippetHead)),
		parsedSnippet(a.siteSettings.Get(ctx, sc, settings.KeySnippetFoot))
}

// snippetWriter puts a site's snippets into each HTML page it serves, fresh
// or from the page cache, as it goes out. It holds back only a successful
// HTML page that is not VayuShield's own (a challenge is not the site's
// page) and not already encoded; everything else passes straight through.
type snippetWriter struct {
	http.ResponseWriter
	head, foot string
	decided    bool
	holding    bool
	code       int
	buf        bytes.Buffer
}

func (s *snippetWriter) decide(code int) {
	if s.decided {
		return
	}
	s.decided = true
	h := s.Header()
	if code == http.StatusOK && strings.HasPrefix(strings.ToLower(h.Get("Content-Type")), "text/html") &&
		h.Get("X-VayuShield") == "" && h.Get("Content-Encoding") == "" {
		s.holding, s.code = true, code
		// The page grows by the snippets, so a length set for it is wrong.
		h.Del("Content-Length")
	}
}

func (s *snippetWriter) WriteHeader(code int) {
	s.decide(code)
	if !s.holding {
		s.ResponseWriter.WriteHeader(code)
	}
}

func (s *snippetWriter) Write(b []byte) (int, error) {
	if !s.decided {
		s.WriteHeader(http.StatusOK)
	}
	if s.holding {
		return s.buf.Write(b)
	}
	return forwardBody(s.ResponseWriter, b)
}

// Flush is a no-op while a page is held: it goes out whole, with its
// snippets, when the handler returns.
func (s *snippetWriter) Flush() {
	if s.holding {
		return
	}
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the connection's own writer.
func (s *snippetWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// finish sends a held page with its snippets.
func (s *snippetWriter) finish() {
	if !s.holding {
		return
	}
	s.ResponseWriter.WriteHeader(s.code)
	_, _ = forwardBody(s.ResponseWriter, withSnippets(s.buf.Bytes(), s.head, s.foot))
}

// withSnippets puts head at the end of the page's <head> and foot at the end
// of its <body>: before the first </head> and the last </body>, or at the end
// of a page that has none.
func withSnippets(page []byte, head, foot string) []byte {
	out := make([]byte, 0, len(page)+len(head)+len(foot))
	rest := page
	if head != "" {
		if i := bytes.Index(rest, []byte("</head>")); i >= 0 {
			out = append(out, rest[:i]...)
			out = append(out, head...)
			rest = rest[i:]
		}
	}
	if foot != "" {
		if i := bytes.LastIndex(rest, []byte("</body>")); i >= 0 {
			out = append(out, rest[:i]...)
			out = append(out, foot...)
			return append(out, rest[i:]...)
		}
		out = append(out, rest...)
		return append(out, foot...)
	}
	return append(out, rest...)
}

// handleOSSnippetsSave stores a site's two snippets after reading each as a
// page will. What a snippet loads from outside is the policy's to allow, so
// the answer names any script origin the site does not allow yet.
func (a *App) handleOSSnippetsSave(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "settings-error", "settings not initialised", "")
		return
	}
	var body struct {
		Head string `json:"head"`
		Foot string `json:"foot"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*render.MaxSnippetBytes+1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	head, err := render.ParseSnippet(body.Head)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid-snippet", "The head snippet: "+err.Error(), "")
		return
	}
	foot, err := render.ParseSnippet(body.Foot)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid-snippet", "The footer snippet: "+err.Error(), "")
		return
	}
	sc, _, _, name := a.servicesSite(r)
	if err := a.siteSettings.SetMany(r.Context(), sc, map[string]string{
		settings.KeySnippetHead: head.HTML, settings.KeySnippetFoot: foot.HTML,
	}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-error", err.Error(), "")
		return
	}
	// A page a browser kept answers 304 on its Last-Modified; redrawn pages
	// carry a new one, so the next visit gets the page with what was saved.
	render.CachePurgeAll()
	dbpkg.AuditLog("site.snippets", dbpkg.AuditActor(r), name, "head "+itoaSafe(len(head.HTML))+" bytes, footer "+itoaSafe(len(foot.HTML))+" bytes")
	detail := "Saved. " + name + "'s public pages carry this code from the next visit."
	if missing := unallowedScriptOrigins(a.sitePolicyIn(r.Context(), sc), head, foot); len(missing) > 0 {
		detail = "Saved, but " + strings.Join(missing, ", ") + " is not allowed on " + name + " yet, so its script will not run. Turn on the service that covers it above."
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "detail": detail})
}

// unallowedScriptOrigins are the origins the snippets load scripts from that
// the site's policy, as enforced, does not allow.
func unallowedScriptOrigins(p render.SitePolicy, snippets ...render.Snippet) []string {
	allowed := map[string]bool{}
	if p.Mode != render.PolicyStrict {
		for _, src := range p.ScriptSources() {
			allowed[src] = true
		}
	}
	var out []string
	for _, s := range snippets {
		for _, o := range s.ScriptOrigins {
			if !allowed[o] && !allowedByWildcard(o, allowed) {
				out = append(out, o)
			}
		}
	}
	return out
}

// allowedByWildcard reports whether a "https://*.host" source covers origin.
func allowedByWildcard(origin string, allowed map[string]bool) bool {
	host := strings.TrimPrefix(origin, "https://")
	for i := strings.IndexByte(host, '.'); i >= 0; i = strings.IndexByte(host, '.') {
		host = host[i+1:]
		if allowed["https://*."+host] {
			return true
		}
	}
	return false
}
