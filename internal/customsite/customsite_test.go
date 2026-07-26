// SPDX-License-Identifier: Apache-2.0

package customsite

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestOSRootConfinesWrites documents the OS-level barrier the extractor and
// server now rely on (Go 1.24 os.Root): the kernel refuses any create that
// would escape the root — via traversal or an absolute path — so Zip Slip /
// path traversal is impossible even if a name slipped past the string checks.
func TestOSRootConfinesWrites(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	for _, escape := range []string{"../escape.txt", "../../escape.txt", "/tmp/vayu-escape.txt"} {
		if f, err := root.Create(escape); err == nil {
			_ = f.Close()
			t.Errorf("os.Root allowed an escaping write to %q", escape)
		}
	}
	// A normal in-root create still works.
	f, err := root.Create("ok.txt")
	if err != nil {
		t.Fatalf("root.Create(ok.txt): %v", err)
	}
	_ = f.Close()
}

// zipOf builds an in-memory .zip from name→content pairs.
func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestDeployAndServeHappyPath(t *testing.T) {
	base := t.TempDir()
	z := zipOf(t, map[string]string{
		"index.html":       "<!doctype html><title>Home</title>",
		"assets/app.css":   "body{color:red}",
		"about/index.html": "<title>About</title>",
	})
	m, err := Deploy(base, z)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if m.Files != 3 || m.Entry != "index.html" {
		t.Errorf("manifest = %+v", m)
	}
	if !Deployed(base) {
		t.Fatal("Deployed = false after a successful deploy")
	}

	// "/" → index.html
	if got := serveGet(t, base, "/"); got != "<!doctype html><title>Home</title>" {
		t.Errorf("/ served %q", got)
	}
	// asset by path
	if got := serveGet(t, base, "/assets/app.css"); got != "body{color:red}" {
		t.Errorf("/assets/app.css served %q", got)
	}
	// directory → its index.html
	if got := serveGet(t, base, "/about"); got != "<title>About</title>" {
		t.Errorf("/about served %q", got)
	}

	// Correct content types.
	rec := serveRec(base, "/assets/app.css")
	if ct := rec.Header().Get("Content-Type"); ct == "" || ct[:8] != "text/css" {
		t.Errorf("css content-type = %q", ct)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff header")
	}
}

func TestDeployRejectsHostileBundles(t *testing.T) {
	cases := map[string]map[string]string{
		"zip slip parent":  {"index.html": "x", "../evil.html": "pwn"},
		"nested traversal": {"index.html": "x", "a/../../evil.js": "pwn"},
		"absolute path":    {"index.html": "x", "/etc/passwd.txt": "pwn"},
		"disallowed ext":   {"index.html": "x", "shell.php": "<?php ?>"},
		"no index":         {"page.html": "x"},
		"empty":            {},
	}
	for name, files := range cases {
		base := t.TempDir()
		if _, err := Deploy(base, zipOf(t, files)); err == nil {
			t.Errorf("%s: Deploy accepted a bundle it should reject", name)
		}
		if Deployed(base) {
			t.Errorf("%s: a rejected deploy left a live site", name)
		}
	}
}

func TestServeIsTraversalSafeAndMissesGracefully(t *testing.T) {
	base := t.TempDir()
	if _, err := Deploy(base, zipOf(t, map[string]string{"index.html": "home", "a.css": "css"})); err != nil {
		t.Fatal(err)
	}
	// Nonexistent file → not served (caller 404s).
	if serveRec(base, "/nope.js").Code != 0 && servedTrue(base, "/nope.js") {
		t.Error("nonexistent path reported as served")
	}
	if servedTrue(base, "/../../etc/passwd") {
		t.Error("traversal path reported as served")
	}
	if !servedTrue(base, "/a.css") {
		t.Error("existing asset not served")
	}
}

func TestRollback(t *testing.T) {
	base := t.TempDir()
	if _, err := Deploy(base, zipOf(t, map[string]string{"index.html": "V1"})); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(base); err == nil {
		t.Error("rollback with no previous deployment should error")
	}
	if _, err := Deploy(base, zipOf(t, map[string]string{"index.html": "V2"})); err != nil {
		t.Fatal(err)
	}
	if got := serveGet(t, base, "/"); got != "V2" {
		t.Fatalf("after 2nd deploy / = %q, want V2", got)
	}
	if err := Rollback(base); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := serveGet(t, base, "/"); got != "V1" {
		t.Errorf("after rollback / = %q, want V1", got)
	}
}

// helpers

func serveRec(base, urlPath string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, urlPath, nil)
	rec := httptest.NewRecorder()
	Serve(rec, req, base, urlPath)
	return rec
}

func servedTrue(base, urlPath string) bool {
	req := httptest.NewRequest(http.MethodGet, urlPath, nil)
	rec := httptest.NewRecorder()
	return Serve(rec, req, base, urlPath)
}

func serveGet(t *testing.T, base, urlPath string) string {
	t.Helper()
	rec := serveRec(base, urlPath)
	return rec.Body.String()
}
