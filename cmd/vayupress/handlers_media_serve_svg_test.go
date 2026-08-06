// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
)

// The claim under test: the media library hands back what it stored.
//
// Three places in this tree say an uploaded SVG is a library asset. The upload
// path accepts it from an operator and sanitises it on the way in. The media
// quota charges it against MEDIA_QUOTA_GB. handleMediaUpload answers 201 with a
// "/media/<hash>.svg" URL, and the connector tool that shares that path tells
// its caller the returned URL is public and ready to paste into a post.
//
// /media answers 404 to every one of them. The serve route's allowlist was never
// widened past the rasters, so the whole SVG feature deposits a charged, listed,
// permanently unreachable file — and the operator's only symptom is a broken
// image in a published post, with the panel showing the asset present and the
// quota counting it.
//
// The sanitiser is the reason this matters rather than being cosmetic. Its
// entire justification, written into this file's own header, is that an uploaded
// file IS served from this origin, so SVG has to be cleaned before it lands. A
// serve route that refuses the format makes that argument describe a path that
// does not exist, and leaves the format accepted, stored and charged anyway.
func TestUploadedSVGIsServedBackFromMedia(t *testing.T) {
	dir := mediaQuotaDir(t, 1<<20)

	clean := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12">` +
		`<rect width="12" height="12" fill="#101010"/></svg>`)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "logo.svg")
	if err != nil {
		t.Fatalf("build multipart part: %v", err)
	}
	if _, err := part.Write(clean); err != nil {
		t.Fatalf("write multipart part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/media", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	(&App{}).handleMediaUpload(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("uploading a clean SVG returned %d: %s", rec.Code, rec.Body.String())
	}

	var uploaded struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("bad upload response: %v (%s)", err, rec.Body.String())
	}
	if !strings.HasSuffix(uploaded.Name, ".svg") {
		t.Fatalf("upload stored %q, expected an .svg — the rest of this test is about that name", uploaded.Name)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, uploaded.Name))
	if err != nil {
		t.Fatalf("the upload reported success but %s is not on disk: %v", uploaded.Name, err)
	}

	// Ask for exactly the URL the caller was handed.
	get := httptest.NewRequest(http.MethodGet, uploaded.URL, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("file", uploaded.Name)
	get = get.WithContext(context.WithValue(get.Context(), chi.RouteCtxKey, rctx))
	served := httptest.NewRecorder()
	(&App{}).serveMedia(served, get)

	if served.Code != http.StatusOK {
		t.Fatalf("GET %s returned %d.\n"+
			"The upload succeeded, the file is on disk, the Media page lists it and the quota "+
			"charges for it — and the URL handed back to the caller does not resolve. Every SVG "+
			"in the library is a broken image in a published post, and the ceiling counts bytes "+
			"nothing can display.", uploaded.URL, served.Code)
	}
	if got := served.Body.Bytes(); !bytes.Equal(got, onDisk) {
		t.Errorf("/media served %d bytes, the stored file is %d — what is served has to be the "+
			"sanitised bytes that were stored", len(got), len(onDisk))
	}
	if ct := served.Header().Get("Content-Type"); !strings.Contains(ct, "image/svg+xml") {
		t.Errorf("Content-Type = %q, want image/svg+xml — a browser that has to sniff the type of "+
			"an attacker-influenced document is the situation nosniff exists to avoid", ct)
	}

	// The second control, stated in this file's header as the reason accepting SVG
	// is defensible: the sanitiser is one, and a policy that refuses script is the
	// other. It has to be on THIS response. Relying on the page middleware means
	// the format that is a program is protected by a header the handler serving it
	// does not set and cannot see.
	csp := served.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Errorf("the SVG response carries no Content-Security-Policy. It is the one stored format " +
			"that is a program, served same-origin; if the sanitiser ever misses something there is " +
			"nothing behind it on this response.")
	}
	for _, want := range []string{"default-src 'none'", "sandbox"} {
		if !strings.Contains(csp, want) {
			t.Errorf("Content-Security-Policy = %q, missing %q — a policy that does not deny by "+
				"default and does not sandbox the document leaves direct navigation to the file "+
				"running in this origin", csp, want)
		}
	}
}

// A raster is not a program and must not acquire the SVG response's sandbox: the
// header is scoped to the one format that needs it, and a test that only ever
// looks at SVG would pass just as happily against a handler that sandboxes every
// media response.
func TestServedRasterKeepsThePlainMediaResponse(t *testing.T) {
	dir := mediaQuotaDir(t, 1<<20)

	stored, err := storeValidatedMedia(distinctPNG(t, 121), true)
	if err != nil {
		t.Fatalf("storing a PNG: %v", err)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, stored.Name))
	if err != nil {
		t.Fatalf("read stored png: %v", err)
	}

	get := httptest.NewRequest(http.MethodGet, stored.URL, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("file", stored.Name)
	get = get.WithContext(context.WithValue(get.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	(&App{}).serveMedia(rec, get)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s returned %d, want 200", stored.URL, rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), onDisk) {
		t.Errorf("served %d bytes for a stored file of %d", rec.Body.Len(), len(onDisk))
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp != "" {
		t.Errorf("a PNG response carries %q. The sandbox belongs to the format that is a program; "+
			"putting it on every media response would also make this test pass with no scoping at "+
			"all, and it overrides the site policy the middleware set for no reason.", csp)
	}
}

// Names this server never wrote are still refused, whatever their extension.
// Widening the serve allowlist from the rasters to everything the store writes
// must not widen it to everything: the name is joined onto MediaDir and handed
// to the file server, so this regexp is the whole path-traversal defence.
//
// Every name below is backed by a REAL file, and that is the point of the test
// rather than a detail of it. The first version seeded nothing and asserted only
// on a 404 — so replacing the whole check with `name == ""` survived, because
// http.ServeFile answers 404 for a path that does not exist and the assertion
// could not tell a refusal apart from a miss. An allowlist test whose fixtures
// are absent is testing the filesystem.
func TestServeMediaStillRefusesNamesThisServerDidNotWrite(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "media")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	prevDir := config.Cfg.MediaDir
	config.Cfg.MediaDir = dir
	t.Cleanup(func() { config.Cfg.MediaDir = prevDir })

	const secret = "BEGIN OPENSSH PRIVATE KEY"
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte(secret), 0o644); err != nil {
		t.Fatal(err)
	}

	// Names inside the directory that the allowlist must still refuse, seeded so
	// that a handler without the check would hand each of them back.
	inside := []string{
		"evil.svg",
		"x", // what "…svg/../x" cleans to
		strings.Repeat("a", 32) + ".SVG",
		strings.Repeat("a", 32) + ".exe",
		strings.Repeat("a", 31) + ".svg",
		strings.Repeat("g", 32) + ".svg",
	}
	for _, n := range inside {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(secret), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, name := range append([]string{
		"../secret.txt",
		"0123456789abcdef0123456789abcdef.svg/../x",
		"",
	}, inside...) {
		get := httptest.NewRequest(http.MethodGet, "/media/asset", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("file", name)
		get = get.WithContext(context.WithValue(get.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		(&App{}).serveMedia(rec, get)

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET /media/%q returned %d, want 404 — the name check in front of the file "+
				"server is the only thing confining this handler to the media directory", name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("GET /media/%q handed back the contents of a file this server never wrote.\n"+
				"The name is joined onto MediaDir and passed to the file server, so anything past "+
				"the allowlist is an unauthenticated read of whatever the process can open.", name)
		}
	}
}
