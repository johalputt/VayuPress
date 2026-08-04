// SPDX-License-Identifier: Apache-2.0

package customsite

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// junkZip builds an in-memory bundle from name→contents.
func junkZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The zip a person actually produces. macOS writes a .DS_Store into every folder
// Finder has opened, so right-click → Compress yields exactly this — and before
// this change the whole upload was refused, which is how "upload your site"
// failed for most people on their first try.
func TestTheZipAPersonActuallyMakesCanBeDeployed(t *testing.T) {
	m, err := Deploy(t.TempDir(), junkZip(t, map[string]string{
		"index.html":            "<html><body>hi</body></html>",
		"assets/style.css":      "body{}",
		".DS_Store":             "\x00\x00binary junk",
		"assets/.DS_Store":      "\x00\x00binary junk",
		"__MACOSX/._index.html": "apple sidecar",
		"Thumbs.db":             "windows junk",
	}))
	if err != nil {
		t.Fatalf("a zip made by right-clicking a folder was refused: %v", err)
	}
	if m.Files != 2 {
		t.Errorf("deployed %d files, want 2 (index.html + style.css)", m.Files)
	}
	if m.Skipped != 4 {
		t.Errorf("skipped %d, want 4 — the operator must be told what was dropped, "+
			"or the deploy quietly differs from what they zipped", m.Skipped)
	}
	if len(m.SkippedNames) == 0 {
		t.Error("nothing named, so the panel can only say a number")
	}
}

// Junk is DROPPED, never served. Accepting these onto the allowlist instead
// would have fixed the upload and widened what the site exposes; this must not
// quietly become that.
func TestJunkIsDroppedRatherThanServed(t *testing.T) {
	base := t.TempDir()
	if _, err := Deploy(base, junkZip(t, map[string]string{
		"index.html": "<html></html>",
		".DS_Store":  "secret-ish",
	})); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.DS_Store", nil)
	if Serve(rec, req, base, "/.DS_Store") && rec.Code == http.StatusOK {
		t.Fatal(".DS_Store was written into the deployed site and is served to anyone who asks")
	}
}

// Real content in the wrong place must still be refused loudly. A .psd or a
// .mov is somebody's file, not metadata, and silently dropping it would mean a
// site missing something its author believed they had published.
func TestRealContentInTheWrongPlaceIsStillRefused(t *testing.T) {
	for _, bad := range []string{"design.psd", "clip.mov", "data.yaml", "backup.zip"} {
		_, err := Deploy(t.TempDir(), junkZip(t, map[string]string{
			"index.html": "<html></html>",
			bad:          "x",
		}))
		if err == nil {
			t.Errorf("%s was accepted; a file that will never be served must be refused, not ignored", bad)
			continue
		}
		if !strings.Contains(err.Error(), bad) {
			t.Errorf("%s: the refusal does not name the file, so the operator cannot fix it: %v", bad, err)
		}
	}
}

// A bundle of nothing but junk is not a site, and must not deploy as an empty one.
func TestAZipOfOnlyJunkIsRefused(t *testing.T) {
	if _, err := Deploy(t.TempDir(), junkZip(t, map[string]string{
		".DS_Store": "x", "Thumbs.db": "y",
	})); err == nil {
		t.Fatal("a zip containing no site at all was deployed")
	}
}
