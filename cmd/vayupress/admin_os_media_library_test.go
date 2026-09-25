// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
)

// The Media library (render 07) names each file the way a person knows it and
// says where it is used, so a file can be found, and removed without breaking
// a page nobody remembered it was on.

const (
	libA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png"
	libB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.jpg"
	libC = "cccccccccccccccccccccccccccccccc.webp"
	libD = "dddddddddddddddddddddddddddddddd.png"
	libE = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee.svg"
	libF = "ffffffffffffffffffffffffffffffff.gif"
)

func TestMediaUsageFindsEachPlaceAFileIsUsed(t *testing.T) {
	openMigratedDB(t)
	now := time.Now().UTC()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		// A post using A twice in its body is one use, and B as its feature image.
		{`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at,status,feature_image,is_page) VALUES('p1','Harbour walk','harbour-walk',?,'',?,?,'published',?,0)`,
			[]any{`<img src="/media/` + libA + `"><img src="https://example.com/media/` + libA + `">`, now, now, "/media/" + libB}},
		// A page using A in its blocks.
		{`INSERT INTO articles(id,title,slug,content,blocks_json,tags,created_at,updated_at,status,is_page) VALUES('p2','About','about','','[{"src":"/media/` + libA + `"}]','',?,?,'published',1)`,
			[]any{now, now}},
		// The install's website: only its latest revision is what is live.
		{`INSERT INTO site_revisions(domain_id,doc,published_at) VALUES('','{"img":"/media/` + libD + `"}',?)`, []any{now}},
		{`INSERT INTO site_revisions(domain_id,doc,published_at) VALUES('','{"img":"/media/` + libC + `"}',?)`, []any{now}},
		{`INSERT INTO site_revisions(domain_id,doc,published_at) VALUES('d1','{"img":"/media/` + libC + `"}',?)`, []any{now}},
		{`INSERT INTO site_settings(scope,key,value) VALUES('','theme.logo','/media/` + libE + `')`, nil},
	} {
		if _, err := dbpkg.DB.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v\n%s", err, q.sql)
		}
	}
	uses := (&App{}).mediaUsage(context.Background())
	got := func(name string) string {
		var out []string
		for _, u := range uses[name] {
			out = append(out, u.Label+" → "+u.Href)
		}
		return strings.Join(out, " | ")
	}
	for name, want := range map[string]string{
		libA: "Post · Harbour walk → /os/editor/harbour-walk | Page · About → /os/editor/about",
		libB: "Post · Harbour walk → /os/editor/harbour-walk",
		libC: "Website → /os/website/editor | Website · a hosted site → /os/d/d1/website",
		libE: "Site settings → /os/settings/design",
	} {
		if got(name) != want {
			t.Errorf("%s is used in\n  %s\nwant\n  %s", name, got(name), want)
		}
	}
	if got(libD) != "" {
		t.Errorf("a file only an old website revision used is said to be in use: %s", got(libD))
	}
	if got(libF) != "" {
		t.Errorf("a file nothing references is said to be in use: %s", got(libF))
	}
}

func TestADisplayNameIsAFileNameAndNothingMore(t *testing.T) {
	for in, want := range map[string]string{
		"harbour at dawn.png":           "harbour at dawn.png",
		`C:\Users\priya\Desktop\me.jpg`: "me.jpg",
		"../../etc/passwd":              "passwd",
		"  spaced.png  ":                "spaced.png",
		"":                              "",
		"/":                             "",
		strings.Repeat("é", 200):        strings.Repeat("é", 120),
	} {
		if got := cleanMediaTitle(in); got != want {
			t.Errorf("cleanMediaTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAFileKeepsTheNameItWasFirstGiven(t *testing.T) {
	openMigratedDB(t)
	ctx := context.Background()
	a := &App{siteSettings: settings.New(dbpkg.DB)}
	a.nameMediaOnce(ctx, libA, "harbour.png")
	a.nameMediaOnce(ctx, libA, "IMG_2231.png")
	a.nameMediaOnce(ctx, "../"+libB, "sneaky.png")
	names := a.mediaNameMap(ctx)
	if names[libA] != "harbour.png" {
		t.Errorf("the same bytes uploaded again renamed the file: %q", names[libA])
	}
	if len(names) != 1 {
		t.Errorf("a name was stored for something that is not a stored file: %v", names)
	}
}

func TestRenameAndDeleteKeepTheNamesInStep(t *testing.T) {
	openMigratedDB(t)
	dir := mediaQuotaDir(t, 1<<20)
	if err := os.WriteFile(filepath.Join(dir, libA), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{siteSettings: settings.New(dbpkg.DB)}
	post := func(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		return rec
	}
	for _, tc := range []struct{ body, code string }{
		{`{"name":"../` + libA + `","title":"x"}`, "bad-name"},
		{`{"name":"` + libA + `","title":"   "}`, "empty-name"},
	} {
		rec := post(a.handleOSMediaName, tc.body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"`+tc.code+`"`) {
			t.Errorf("rename %s: %d %s, want 400 %s", tc.body, rec.Code, rec.Body.String(), tc.code)
		}
	}
	if rec := post(a.handleOSMediaName, `{"name":"`+libA+`","title":"Harbour at dawn.png"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}

	rec := httptest.NewRecorder()
	a.handleOSMediaList(rec, httptest.NewRequest(http.MethodGet, "/os/api/media", nil))
	var list struct{ Items []mediaItem }
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("list: %v %s", err, rec.Body.String())
	}
	if it := list.Items[0]; it.Title != "Harbour at dawn.png" || it.Kind != "PNG image" || it.Uses == nil {
		t.Errorf("the library lists %+v; want the new name, its kind, and uses as a list (never null)", it)
	}

	if rec := post(a.handleOSMediaDelete, `{"names":["`+libA+`"]}`); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if n := a.mediaNameMap(context.Background()); len(n) != 0 {
		t.Errorf("a deleted file's name outlives it: %v", n)
	}
}
