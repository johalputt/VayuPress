// SPDX-License-Identifier: Apache-2.0

package main

// site_editor_api.go — the console's site-document API (ADR-0161). One set of
// handlers serves the primary site (/os/api/site-doc) and every hosted site
// (/os/d/{id}/api/site-doc); only the scope differs.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/sitedoc"
)

// siteDocTarget resolves the site an editor request is about: its scope as
// stored, and a request that renders as that site.
type siteDocTarget func(r *http.Request) (scope string, view *http.Request, ok bool)

func primarySiteDocTarget(r *http.Request) (string, *http.Request, bool) { return "", r, true }

func scopedSiteDocTarget(r *http.Request) (string, *http.Request, bool) {
	d, ok := osScopedDomain(r)
	if !ok {
		return "", nil, false
	}
	return d.ID, r.WithContext(context.WithValue(r.Context(), ctxKeyDomain{}, d)), true
}

func (a *App) siteDocTargetOr404(w http.ResponseWriter, r *http.Request, target siteDocTarget) (string, *http.Request, bool) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return "", nil, false
	}
	scope, view, ok := target(r)
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, "unknown-domain", "no such site", "")
		return "", nil, false
	}
	return scope, view, true
}

// siteDocAuthor names who saved or published, for the history list.
func siteDocAuthor(r *http.Request) string {
	if u := currentUser(r); u != nil {
		return u.Email
	}
	return "api key"
}

// writeSiteDocError answers a refused document with the field that failed,
// so the editor can point at it.
func writeSiteDocError(w http.ResponseWriter, r *http.Request, err error) {
	var fe *sitedoc.FieldError
	var taken errSiteSlugTaken
	var gate errSiteChecks
	switch {
	case errors.As(err, &gate):
		writeJSON(w, r, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]string{"code": "publish-check", "message": gate.Error(), "path": gate.first.Path},
		})
	case errors.As(err, &fe):
		writeJSON(w, r, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]string{"code": "invalid-document", "message": fe.Error(), "path": fe.Path},
		})
	case errors.As(err, &taken):
		writeAPIError(w, r, http.StatusConflict, "slug-taken", taken.Error(), "")
	case errors.Is(err, errNoSuchRevision):
		writeAPIError(w, r, http.StatusNotFound, "unknown-revision", err.Error(), "")
	default:
		writeAPIError(w, r, http.StatusInternalServerError, "site-doc-failed", err.Error(), "")
	}
}

// readSiteDoc decodes {"doc": …} through the document's own parser, so the
// size limit and unknown-field refusal apply to what the editor sends too.
func readSiteDoc(w http.ResponseWriter, r *http.Request) (sitedoc.Document, error) {
	var body struct {
		Doc json.RawMessage `json:"doc"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, sitedoc.MaxBytes+1024))
	if err := dec.Decode(&body); err != nil || len(body.Doc) == 0 {
		return sitedoc.Document{}, &sitedoc.FieldError{Path: "doc", Msg: "send the document as {\"doc\": …}"}
	}
	return sitedoc.Parse(body.Doc)
}

// handleSiteDocGet is everything the editor opens with: the document to edit
// (the draft, else the published revision, else the legacy site as a
// document), where it came from, and the history.
func (a *App) handleSiteDocGet(target siteDocTarget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, view, ok := a.siteDocTargetOr404(w, r, target)
		if !ok {
			return
		}
		_, tpl, current := a.siteDocument(view)
		source := "legacy"
		if _, ok := publishedSiteDoc(r.Context(), scope); ok {
			source = "published"
		}
		// The closed choices come from the validator's own tables, so the
		// editor never offers a value the server would refuse.
		resp := map[string]any{"template": tpl.Key, "ai": a.siteAssistAvailable(r.Context()), "choices": map[string]any{
			"variants": sitedoc.Variants, "fonts": sortedKeys(sitedoc.Fonts), "corners": sortedKeys(sitedoc.Corners),
		}}
		if d, at, ok := siteDraft(r.Context(), scope); ok {
			current, source = d, "draft"
			resp["draft_saved_at"] = at
		}
		revs, err := siteRevisions(r.Context(), scope)
		if err != nil {
			writeSiteDocError(w, r, err)
			return
		}
		resp["doc"], resp["source"], resp["revisions"] = current, source, revs
		writeJSON(w, r, http.StatusOK, resp)
	}
}

// handleSiteDocDraft saves the editor's work without publishing it.
func (a *App) handleSiteDocDraft(target siteDocTarget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, _, ok := a.siteDocTargetOr404(w, r, target)
		if !ok {
			return
		}
		d, err := readSiteDoc(w, r)
		if err != nil {
			writeSiteDocError(w, r, err)
			return
		}
		at, err := saveSiteDraft(r.Context(), scope, d, siteDocAuthor(r))
		if err != nil {
			writeSiteDocError(w, r, err)
			return
		}
		writeJSON(w, r, http.StatusOK, map[string]any{"status": "saved", "saved_at": at,
			"checks": nonNilChecks(a.checkSite(r.Context(), scope, d))})
	}
}

// handleSiteDocPublish makes the sent document the live site.
func (a *App) handleSiteDocPublish(target siteDocTarget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, _, ok := a.siteDocTargetOr404(w, r, target)
		if !ok {
			return
		}
		d, err := readSiteDoc(w, r)
		if err != nil {
			writeSiteDocError(w, r, err)
			return
		}
		a.publishAndAnswer(w, r, scope, d, "published")
	}
}

func (a *App) publishAndAnswer(w http.ResponseWriter, r *http.Request, scope string, d sitedoc.Document, verb string) {
	id, checks, err := a.publishSite(r.Context(), scope, d, siteDocAuthor(r))
	if err != nil {
		writeSiteDocError(w, r, err)
		return
	}
	target := scope
	if target == "" {
		target = "primary"
	}
	dbpkg.AuditLog("website.document", dbpkg.AuditActor(r), target, verb+" revision "+itoaSafe(int(id)))
	render.CachePurgeAll()
	writeJSON(w, r, http.StatusOK, map[string]any{"status": verb, "revision": id, "checks": nonNilChecks(checks)})
}

// handleSiteDocRevision returns one published revision, for the history view.
func (a *App) handleSiteDocRevision(target siteDocTarget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, _, ok := a.siteDocTargetOr404(w, r, target)
		if !ok {
			return
		}
		id, ok := parseRevisionID(chi.URLParam(r, "rev"))
		if !ok {
			writeSiteDocError(w, r, errNoSuchRevision)
			return
		}
		d, err := siteRevisionDoc(r.Context(), scope, id)
		if err != nil {
			writeSiteDocError(w, r, err)
			return
		}
		writeJSON(w, r, http.StatusOK, map[string]any{"id": id, "doc": d})
	}
}

// handleSiteDocRestore publishes an older revision again. It is a new
// revision rather than a rewind, so restoring is itself undoable.
func (a *App) handleSiteDocRestore(target siteDocTarget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, _, ok := a.siteDocTargetOr404(w, r, target)
		if !ok {
			return
		}
		id, ok := parseRevisionID(chi.URLParam(r, "rev"))
		if !ok {
			writeSiteDocError(w, r, errNoSuchRevision)
			return
		}
		d, err := siteRevisionDoc(r.Context(), scope, id)
		if err != nil {
			writeSiteDocError(w, r, err)
			return
		}
		a.publishAndAnswer(w, r, scope, d, "restored")
	}
}

// handleSiteDocPreview renders the draft (or the live document when there is
// none) as the site will look, for the editor's preview frame.
//
// Framable by the console only, as the theme preview is. The contact form is
// left out: a message sent from a preview would be stored as the console
// host's rather than the site's.
func (a *App) handleSiteDocPreview(target siteDocTarget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, view, ok := a.siteDocTargetOr404(w, r, target)
		if !ok {
			return
		}
		mode, tpl, doc := a.siteDocument(view)
		if d, _, ok := siteDraft(r.Context(), scope); ok {
			doc = d
		}
		o := a.siteRenderOptions(view, mode, tpl)
		// The draft's brand is not the live site's, and /site.css on the
		// console's host is not this site's at all, so the preview carries a
		// stylesheet built from the same draft it renders. Inline, because
		// the frame is sandboxed to an opaque origin and its requests carry
		// no session; admitted by its hash, so the policy grants that one
		// stylesheet and not inline styles in general.
		o.InlineCSS = siteCSS(tpl, doc)
		sum := sha256.Sum256([]byte(o.InlineCSS))
		csp := strings.Replace(render.BuildCSP(render.CSPNonce(r), nil), "frame-ancestors 'none'", "frame-ancestors 'self'", 1)
		csp = strings.Replace(csp, "style-src 'self';", "style-src 'self' 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"';", 1)
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Cache-Control", "no-store")
		if !writeSitePage(w, doc, r.URL.Query().Get("page"), o) {
			writeAPIError(w, r, http.StatusNotFound, "unknown-page", "the document has no such page", "")
		}
	}
}

// registerSiteDocRoutes mounts the editor API under base for one kind of site.
func (a *App) registerSiteDocRoutes(r chi.Router, csrf func(http.Handler) http.Handler, base string, target siteDocTarget) {
	r.Get(base, a.handleSiteDocGet(target))
	r.Get(base+"/preview", a.handleSiteDocPreview(target))
	r.Get(base+"/suggest-accent", a.handleSiteDocSuggestAccent(target))
	r.With(csrf).Post(base+"/assist", a.handleSiteDocAssist(target))
	r.Get(base+"/revisions/{rev}", a.handleSiteDocRevision(target))
	r.With(csrf).Post(base+"/draft", a.handleSiteDocDraft(target))
	r.With(csrf).Post(base+"/publish", a.handleSiteDocPublish(target))
	r.With(csrf).Post(base+"/revisions/{rev}/restore", a.handleSiteDocRestore(target))
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// nonNilChecks is checks as a JSON array even when there are none, so a
// client reads "[]" as "nothing found" rather than null as "not checked".
func nonNilChecks(c []siteCheck) []siteCheck {
	if c == nil {
		return []siteCheck{}
	}
	return c
}
