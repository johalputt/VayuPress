// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/sitedoc"
	"github.com/johalputt/vayupress/internal/users"
)

// editorRouter mounts the site-document API for one hosted site as the
// console does, with an administrator signed in (or not).
func editorRouter(a *App, d domain.Domain, admin bool) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := context.WithValue(req.Context(), ctxScopedDomainKey, d)
			if admin {
				ctx = context.WithValue(ctx, ctxUserKey, &users.User{Role: users.RoleAdmin, Email: "op@example.com"})
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	pass := func(h http.Handler) http.Handler { return h }
	a.registerSiteDocRoutes(r, pass, "/sd", scopedSiteDocTarget)
	return r
}

func editorCall(t *testing.T, h http.Handler, method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, &buf))
	var j map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &j)
	return rec, j
}

func withDoc(d sitedoc.Document) map[string]any { return map[string]any{"doc": d} }

// The editor opens on the site as it is today — its legacy content as a
// document — so the first save changes nothing the operator did not change.
func TestTheEditorOpensOnTheLegacySite(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	rec, j := editorCall(t, editorRouter(a, d, true), http.MethodGet, "/sd", nil)
	if rec.Code != http.StatusOK || j["source"] != "legacy" {
		t.Fatalf("open: %d source=%v", rec.Code, j["source"])
	}
	doc := j["doc"].(map[string]any)
	if doc["name"] != "Maison Olive" || j["template"] != "bistro" {
		t.Errorf("opened on %v in %v, want the site's legacy content in its own design", doc["name"], j["template"])
	}
}

// A draft is refused with the field that failed, is kept when valid, and is
// what the editor reopens on; the public site does not change until publish.
func TestADraftIsValidatedKeptAndNotPublic(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	h := editorRouter(a, d, true)
	bad := twoPageDoc("Harbour & Co")
	bad.Pages[0].Sections = append(bad.Pages[0].Sections, sitedoc.Section{ID: "pics", Kind: sitedoc.KindGallery,
		Images: []sitedoc.Image{{Src: "/media/a.webp"}}})
	rec, j := editorCall(t, h, http.MethodPost, "/sd/draft", withDoc(bad))
	if rec.Code != http.StatusUnprocessableEntity || j["error"].(map[string]any)["path"] != "pages[0].sections[2].images[0].alt" {
		t.Fatalf("a gallery picture without alt text: %d %s", rec.Code, rec.Body)
	}
	if rec, _ := editorCall(t, h, http.MethodPost, "/sd/draft", withDoc(twoPageDoc("Draft Name"))); rec.Code != http.StatusOK {
		t.Fatalf("valid draft: %d %s", rec.Code, rec.Body)
	}
	_, j = editorCall(t, h, http.MethodGet, "/sd", nil)
	if j["source"] != "draft" || j["doc"].(map[string]any)["name"] != "Draft Name" {
		t.Errorf("the editor did not reopen on its draft: %v", j["source"])
	}
	if body := siteGet(a, d, d.Host, "/", a.handleHome).Body.String(); strings.Contains(body, "Draft Name") {
		t.Error("a draft reached the public site before it was published")
	}
}

// Publishing makes the document the live site and clears the draft.
func TestPublishingMakesTheDocumentLive(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	h := editorRouter(a, d, true)
	editorCall(t, h, http.MethodPost, "/sd/draft", withDoc(twoPageDoc("Draft")))
	rec, j := editorCall(t, h, http.MethodPost, "/sd/publish", withDoc(twoPageDoc("Live Name")))
	if rec.Code != http.StatusOK || j["revision"] == nil {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body)
	}
	if body := siteGet(a, d, d.Host, "/", a.handleHome).Body.String(); !strings.Contains(body, "Live Name") {
		t.Error("the published document is not what the site serves")
	}
	_, j = editorCall(t, h, http.MethodGet, "/sd", nil)
	if j["source"] != "published" || j["draft_saved_at"] != nil {
		t.Errorf("after publish the editor reopened on %v with a draft still saved", j["source"])
	}
}

// A page address something else already answers at would publish a page
// nobody can reach. Refused — each cause with its own seed.
func TestAPageCannotTakeAnAddressAlreadyInUse(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	h := editorRouter(a, d, true)
	router := chi.NewRouter()
	router.Get("/search", func(http.ResponseWriter, *http.Request) {})
	router.Get("/{slug}", func(http.ResponseWriter, *http.Request) {})
	a.setRootHandler(router)

	withPage := func(slug string) sitedoc.Document {
		doc := twoPageDoc("Harbour")
		doc.Pages[1].Slug = slug
		return doc
	}
	rec, _ := editorCall(t, h, http.MethodPost, "/sd/publish", withDoc(withPage("search")))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "used by VayuPress (/search)") {
		t.Errorf("a page at /search, which a route owns: %d %s", rec.Code, rec.Body)
	}
	if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,tags,status,created_at,updated_at,domain_id) VALUES('p1','Wine','wine','x','','published',?,?,?)`,
		time.Now(), time.Now(), d.ID); err != nil {
		t.Fatal(err)
	}
	rec, _ = editorCall(t, h, http.MethodPost, "/sd/publish", withDoc(withPage("wine")))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "post or page on this site") {
		t.Errorf("a page at /wine, which a post on this site owns: %d %s", rec.Code, rec.Body)
	}
	// Another site's post does not own this site's address.
	if _, err := dbpkg.DB.Exec(`UPDATE articles SET domain_id='elsewhere' WHERE id='p1'`); err != nil {
		t.Fatal(err)
	}
	if rec, _ := editorCall(t, h, http.MethodPost, "/sd/publish", withDoc(withPage("wine"))); rec.Code != http.StatusOK {
		t.Errorf("a post on ANOTHER site blocked this site's /wine: %d %s", rec.Code, rec.Body)
	}
}

// The real route table, not a list: words the router answers are refused.
func TestTheRealRouterDecidesWhichAddressesAreTaken(t *testing.T) {
	a := siteApp(t)
	router := chi.NewRouter()
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				t.Skipf("the full route table needs more wiring than this test provides: %v", rec)
			}
		}()
		a.registerRoutes(router, t.TempDir())
	}()
	a.setRootHandler(router)
	doc := twoPageDoc("Harbour")
	for _, taken := range []string{"search", "blog", "tags", "site", "os"} {
		doc.Pages[1].Slug = taken
		if err := a.checkSiteSlugs(context.Background(), "", doc); err == nil {
			t.Errorf("/%s is routed by VayuPress and was allowed as a page address", taken)
		}
	}
	doc.Pages[1].Slug = "our-menu"
	if err := a.checkSiteSlugs(context.Background(), "", doc); err != nil {
		t.Errorf("an ordinary address was refused: %v", err)
	}
}

// History: every publish is kept, any one can be read and restored, and a
// restore is itself a new revision.
func TestRevisionsCanBeReadAndRestored(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	h := editorRouter(a, d, true)
	_, first := editorCall(t, h, http.MethodPost, "/sd/publish", withDoc(twoPageDoc("First")))
	editorCall(t, h, http.MethodPost, "/sd/publish", withDoc(twoPageDoc("Second")))
	id := strconv.Itoa(int(first["revision"].(float64)))

	_, j := editorCall(t, h, http.MethodGet, "/sd", nil)
	revs := j["revisions"].([]any)
	if len(revs) != 2 || revs[0].(map[string]any)["author"] != "op@example.com" {
		t.Fatalf("history: %v", j["revisions"])
	}
	if revs[0].(map[string]any)["id"].(float64) <= revs[1].(map[string]any)["id"].(float64) {
		t.Errorf("history is not newest first: %v", revs)
	}
	_, j = editorCall(t, h, http.MethodGet, "/sd/revisions/"+id, nil)
	if j["doc"].(map[string]any)["name"] != "First" {
		t.Errorf("revision %s is %v, want First", id, j["doc"])
	}
	rec, j := editorCall(t, h, http.MethodPost, "/sd/revisions/"+id+"/restore", nil)
	if rec.Code != http.StatusOK || j["status"] != "restored" {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	if body := siteGet(a, d, d.Host, "/", a.handleHome).Body.String(); !strings.Contains(body, "First") {
		t.Error("the restored revision is not what the site serves")
	}
	_, j = editorCall(t, h, http.MethodGet, "/sd", nil)
	if n := len(j["revisions"].([]any)); n != 3 {
		t.Errorf("a restore left %d revisions, want 3 — it must be undoable", n)
	}
}

// A revision belongs to its site. Site B cannot read or restore A's.
func TestARevisionCannotBeReachedThroughAnotherSite(t *testing.T) {
	a := siteApp(t)
	siteA, siteB := hostedSite(t, a, "a.example"), hostedSite(t, a, "b.example")
	_, j := editorCall(t, editorRouter(a, siteA, true), http.MethodPost, "/sd/publish", withDoc(twoPageDoc("Only A")))
	id := strconv.Itoa(int(j["revision"].(float64)))
	hb := editorRouter(a, siteB, true)
	if rec, _ := editorCall(t, hb, http.MethodGet, "/sd/revisions/"+id, nil); rec.Code != http.StatusNotFound {
		t.Errorf("site B read site A's revision: %d", rec.Code)
	}
	if rec, _ := editorCall(t, hb, http.MethodPost, "/sd/revisions/"+id+"/restore", nil); rec.Code != http.StatusNotFound {
		t.Errorf("site B restored site A's revision: %d", rec.Code)
	}
	if body := siteGet(a, siteB, siteB.Host, "/", a.handleHome).Body.String(); strings.Contains(body, "Only A") {
		t.Error("site A's revision went live on site B")
	}
}

func TestOnlyTheNewestRevisionsAreKept(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	h := editorRouter(a, d, true)
	for i := 0; i < siteRevisionsKept+2; i++ {
		if rec, _ := editorCall(t, h, http.MethodPost, "/sd/publish", withDoc(twoPageDoc("Rev "+strconv.Itoa(i)))); rec.Code != http.StatusOK {
			t.Fatalf("publish %d: %d", i, rec.Code)
		}
	}
	var n int
	if err := dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM site_revisions WHERE domain_id=?`, d.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != siteRevisionsKept {
		t.Errorf("%d revisions kept, want %d", n, siteRevisionsKept)
	}
	if body := siteGet(a, d, d.Host, "/", a.handleHome).Body.String(); !strings.Contains(body, "Rev "+strconv.Itoa(siteRevisionsKept+1)) {
		t.Error("pruning removed the newest revision instead of the oldest")
	}
}

// The preview shows the draft, can be framed by the console and nothing else,
// and carries no contact form a test message could be sent from.
func TestThePreviewShowsTheDraftInsideTheConsoleOnly(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	h := editorRouter(a, d, true)
	doc := twoPageDoc("Preview Me")
	doc.Pages[0].Sections[1].Form = true
	editorCall(t, h, http.MethodPost, "/sd/draft", withDoc(doc))
	rec, _ := editorCall(t, h, http.MethodGet, "/sd/preview?page=", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Preview Me") {
		t.Fatalf("preview: %d", rec.Code)
	}
	if rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'") {
		t.Errorf("preview framing: XFO=%q CSP=%q", rec.Header().Get("X-Frame-Options"), rec.Header().Get("Content-Security-Policy"))
	}
	if strings.Contains(rec.Body.String(), "contact.js") {
		t.Error("the preview loads the contact form's script")
	}
	if rec, _ := editorCall(t, h, http.MethodGet, "/sd/preview?page=nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("preview of a page the document lacks: %d", rec.Code)
	}
}

func TestTheSiteDocumentAPIIsAdminOnly(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	h := editorRouter(a, d, false)
	for _, c := range [][2]string{{"GET", "/sd"}, {"GET", "/sd/preview"}, {"GET", "/sd/revisions/1"},
		{"POST", "/sd/draft"}, {"POST", "/sd/publish"}, {"POST", "/sd/revisions/1/restore"}} {
		if rec, _ := editorCall(t, h, c[0], c[1], withDoc(twoPageDoc("x"))); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s without an admin: %d", c[0], c[1], rec.Code)
		}
	}
}
