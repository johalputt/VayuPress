// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/sha1" //nolint:gosec // git's own file naming, as the sync checks it
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/customsite"
	"github.com/johalputt/vayupress/internal/domain"
)

// siteFilesApp is an install with its data under a temporary root, so bundles
// and working copies land somewhere the test owns.
func siteFilesApp(t *testing.T) *App {
	t.Helper()
	a := siteApp(t)
	old, oldDisk := config.Cfg.MediaDir, bundleDisk
	config.Cfg.MediaDir = filepath.Join(t.TempDir(), "media")
	// A deploy is bounded by the machine's free space less a reserve; these
	// tests are about what is published, not about this machine's disk.
	bundleDisk = func(string) (uint64, uint64) { return 100 << 30, 50 << 30 }
	t.Cleanup(func() {
		a.bgWG.Wait()
		config.Cfg.MediaDir, bundleDisk = old, oldDisk
	})
	return a
}

func liveFile(t *testing.T, d domain.Domain, p string) string {
	t.Helper()
	b, err := customsite.ReadFile(scopedBundleDir(d), p)
	if err != nil {
		return "(" + err.Error() + ")"
	}
	return string(b)
}

func serves(t *testing.T, a *App, d domain.Domain) string {
	t.Helper()
	d, err := a.domains.ByID(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return scopedSiteMode(d)
}

var logoPNG = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0xff, 0x00}

// An assistant builds a site file by file, pictures included, changes one
// page without resending the rest, and can undo it.
func TestTheConnectorEditsALiveSiteFileByFile(t *testing.T) {
	a := siteFilesApp(t)
	d := hostedSite(t, a, "harbour.example")

	j, err := runTool(t, a, "list_site_files", `{"host":"harbour.example"}`)
	if err != nil || j["count"].(float64) != 0 || !strings.Contains(j["note"].(string), "no uploaded site") {
		t.Fatalf("listing a domain with no site: %v %v", err, j)
	}
	j, err = runTool(t, a, "edit_site_files", `{"host":"harbour.example","files":{"index.html":"V1","a.css":"body{}"},`+
		`"files_base64":{"img/logo.png":"`+base64.StdEncoding.EncodeToString(logoPNG)+`"}}`)
	if err != nil || j["status"] != "published" || j["files"].(float64) != 3 {
		t.Fatalf("a first site written file by file: %v %v", err, j)
	}
	if got := serves(t, a, d); got != "custom" {
		t.Errorf("after publishing, the domain serves %q, not the site", got)
	}
	j, _ = runTool(t, a, "read_site_file", `{"host":"harbour.example","path":"img/logo.png"}`)
	if got, _ := base64.StdEncoding.DecodeString(j["content"].(string)); j["encoding"] != "base64" || string(got) != string(logoPNG) {
		t.Errorf("a picture read back as %v %q", j["encoding"], got)
	}
	j, _ = runTool(t, a, "read_site_file", `{"host":"harbour.example","path":"index.html"}`)
	if j["encoding"] != "text" || j["content"] != "V1" {
		t.Errorf("a page read back as %v %v", j["encoding"], j["content"])
	}

	if _, err := runTool(t, a, "edit_site_files", `{"host":"harbour.example","files":{"index.html":"V2"},"delete":["a.css"]}`); err != nil {
		t.Fatal(err)
	}
	if liveFile(t, d, "index.html") != "V2" || liveFile(t, d, "img/logo.png") != string(logoPNG) || !strings.Contains(liveFile(t, d, "a.css"), "no file") {
		t.Error("the second edit did not change exactly the files it named")
	}
	if _, err := runTool(t, a, "restore_previous_site", `{"host":"harbour.example"}`); err != nil {
		t.Fatal(err)
	}
	if liveFile(t, d, "index.html") != "V1" || liveFile(t, d, "a.css") != "body{}" {
		t.Error("restore_previous_site did not bring back the site before the edit")
	}

	// One seed per refusal, each by its own reason.
	for args, why := range map[string]string{
		`{"host":"harbour.example","files_base64":{"x.png":"not base64!"}}`:           "not valid base64",
		`{"host":"harbour.example","files":{"x.png":""},"files_base64":{"x.png":""}}`: "both files and files_base64",
		`{"host":"harbour.example","files":{"notes.md":"x"}}`:                         "only static web files",
	} {
		if _, err := runTool(t, a, "edit_site_files", args); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("edit_site_files %s = %v, want %q", args, err, why)
		}
	}
	if _, err := runTool(t, a, "read_site_file", `{"host":"harbour.example","path":"../manifest.json"}`); err == nil || !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("reading outside the site = %v", err)
	}
}

// fakeForge plays GitHub for the sync: a branch, its tree, its files.
type fakeForge struct {
	mu     sync.Mutex
	commit string
	files  map[string]string
}

func (f *fakeForge) set(commit string, files map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commit, f.files = commit, files
}

func (f *fakeForge) serve(t *testing.T, a *App) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/info/refs"):
			w.Write([]byte("003f" + f.commit + " refs/heads/main\n0000"))
		case strings.Contains(r.URL.Path, "/git/trees/"):
			var tree []map[string]string
			for p, c := range f.files {
				h := sha1.New() //nolint:gosec // see the import
				h.Write([]byte("blob " + strconv.Itoa(len(c)) + "\x00" + c))
				tree = append(tree, map[string]string{"path": p, "type": "blob", "sha": hex.EncodeToString(h.Sum(nil))})
			}
			json.NewEncoder(w).Encode(map[string]any{"tree": tree})
		default:
			c, ok := f.files[strings.TrimPrefix(r.URL.Path, "/o/r/"+f.commit+"/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Write([]byte(c))
		}
	}))
	t.Cleanup(srv.Close)
	a.siteForge = &siteForge{Git: srv.URL, API: srv.URL, Raw: srv.URL, Client: srv.Client()}
}

func follows(j map[string]any) map[string]any {
	f, _ := j["follows"].(map[string]any)
	return f
}

// A site that is a folder of a repository: followed, built, published; a push
// goes live with one call; a hand edit is refused while it follows, with the
// way to make the change; and it can stop following.
func TestASiteFollowsAFolderOfARepository(t *testing.T) {
	a := siteFilesApp(t)
	d := hostedSite(t, a, "harbour.example")
	f := &fakeForge{}
	f.set(strings.Repeat("1", 40), map[string]string{"site/index.html": "one", "site/README.md": "# notes", "cmd/main.go": "package main"})
	f.serve(t, a)

	j, err := runTool(t, a, "follow_repository", `{"host":"harbour.example","repo":"o/r","dir":"site/"}`)
	if err != nil || j["status"] != "following" || follows(j)["commit"] != strings.Repeat("1", 40) {
		t.Fatalf("follow_repository: %v %v", err, j)
	}
	if liveFile(t, d, "index.html") != "one" || serves(t, a, d) != "custom" {
		t.Fatalf("the followed folder is not what the domain serves: %q, %s", liveFile(t, d, "index.html"), serves(t, a, d))
	}
	if lo, _ := follows(j)["left_out"].([]any); len(lo) != 1 || lo[0] != "README.md" {
		t.Errorf("what the site could not carry is not named: %v", follows(j)["left_out"])
	}

	for _, tool := range []string{"edit_site_files", "build_site"} {
		if _, err := runTool(t, a, tool, `{"host":"harbour.example","files":{"index.html":"by hand"}}`); err == nil || !strings.Contains(err.Error(), "is built from o/r@main, folder site") {
			t.Errorf("%s on a followed site = %v; the next push would silently undo it", tool, err)
		}
	}

	f.set(strings.Repeat("2", 40), map[string]string{"site/index.html": "two"})
	j, err = runTool(t, a, "sync_site", `{"host":"harbour.example"}`)
	if err != nil || j["status"] != "synced" || liveFile(t, d, "index.html") != "two" {
		t.Fatalf("a push did not go live with sync_site: %v %v %q", err, j, liveFile(t, d, "index.html"))
	}
	j, _ = runTool(t, a, "get_site", `{"host":"harbour.example"}`)
	if follows(j)["commit"] != strings.Repeat("2", 40) || follows(j)["repo"] != "o/r" {
		t.Errorf("get_site does not report what the site is built from: %v", j["follows"])
	}

	j, err = runTool(t, a, "follow_repository", `{"host":"harbour.example","repo":""}`)
	if err != nil || j["status"] != "stopped" || liveFile(t, d, "index.html") != "two" {
		t.Fatalf("stopping: %v %v", err, j)
	}
	if _, err := runTool(t, a, "edit_site_files", `{"host":"harbour.example","files":{"index.html":"by hand"}}`); err != nil {
		t.Errorf("after stopping, the site cannot be edited by hand: %v", err)
	}
	if _, err := runTool(t, a, "sync_site", `{"host":"harbour.example"}`); err == nil || !strings.Contains(err.Error(), "does not follow") {
		t.Errorf("sync_site on a domain that follows nothing = %v", err)
	}
}

// What cannot be followed is refused before anything is stored.
func TestFollowingRefusesWhatCannotBeFollowed(t *testing.T) {
	a := siteFilesApp(t)
	d := hostedSite(t, a, "harbour.example")
	for args, why := range map[string]string{
		`{"host":"harbour.example","repo":"o"}`:                              "owner/name",
		`{"host":"harbour.example","repo":"o/r","branch":"../x"}`:            "branch",
		`{"host":"harbour.example","repo":"o/r","dir":"../x"}`:               "a folder is a path",
		`{"host":"harbour.example","repo":"o/r","site":"blog"}`:              `"updates"`,
		`{"host":"harbour.example","repo":"o/r","dir":"s","site":"updates"}`: "one or the other",
	} {
		if _, err := runTool(t, a, "follow_repository", args); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("follow_repository %s = %v, want %q", args, err, why)
		}
	}
	d, _ = a.domains.ByID(context.Background(), d.ID)
	if _, ok := d.Follow(); ok {
		t.Error("a refused follow was stored")
	}
	old := config.Cfg.OnionMode
	config.Cfg.OnionMode = true
	defer func() { config.Cfg.OnionMode = old }()
	if _, err := runTool(t, a, "follow_repository", `{"host":"harbour.example","repo":"o/r"}`); err == nil || !strings.Contains(err.Error(), "Tor Space") {
		t.Errorf("following in a Tor Space = %v", err)
	}
	if err := a.domains.SetFollow(context.Background(), d.ID, &domain.Follow{Repo: "o/r", Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runTool(t, a, "sync_site", `{"host":"harbour.example"}`); err == nil || !strings.Contains(err.Error(), "Tor Space") {
		t.Errorf("syncing in a Tor Space = %v", err)
	}
}

// The two docs sites link each other through this install's own domains: the
// product site's Download opens the domain serving the mirror, and the
// mirror's page cannot be built until a domain follows the docs as the product
// site, because every link on it leads there.
func TestTheDocsSitesFindEachOtherOnTheInstall(t *testing.T) {
	a := siteFilesApp(t)
	product := hostedSite(t, a, "product.example")
	mirror := hostedSite(t, a, "mirror.example")
	if err := a.domains.SetReleaseMirror(context.Background(), mirror.ID, true); err != nil {
		t.Fatal(err)
	}
	f := &fakeForge{}
	f.set(strings.Repeat("3", 40), map[string]string{
		"CHANGELOG.md":         "# Changelog\n\n## [3.17.85] — 2026-09-27\n\nA fix.\n",
		"docs/INSTALLATION.md": "# Installation\n\nRun one command.\n",
	})
	f.serve(t, a)

	j, _ := runTool(t, a, "follow_repository", `{"host":"mirror.example","repo":"o/r","site":"updates"}`)
	if e, _ := follows(j)["error"].(string); !strings.Contains(e, "no domain on this install follows the docs as it") {
		t.Errorf("the mirror's page built with nothing to link back to: %v", follows(j))
	}
	if _, err := runTool(t, a, "follow_repository", `{"host":"product.example","repo":"o/r"}`); err != nil {
		t.Fatal(err)
	}
	if home := liveFile(t, product, "index.html"); !strings.Contains(home, `href="https://mirror.example/"`) {
		t.Error("the product site's Download does not open the domain serving the mirror")
	}
	if _, err := runTool(t, a, "sync_site", `{"host":"mirror.example"}`); err != nil {
		t.Fatal(err)
	}
	if page := liveFile(t, mirror, "index.html"); !strings.Contains(page, `href="https://product.example/docs/"`) {
		t.Errorf("the mirror's page does not link back to the product site:\n%.400s", page)
	}
}

// A domain the operator disabled is published to by none of the write tools,
// each checked on its own.
func TestNoSiteToolPublishesToADisabledDomain(t *testing.T) {
	a := siteFilesApp(t)
	d := hostedSite(t, a, "harbour.example")
	f := domain.Follow{Repo: "o/r", Branch: "main", Dir: "site"}
	if err := a.domains.SetFollow(context.Background(), d.ID, &f); err != nil {
		t.Fatal(err)
	}
	if err := a.domains.SetStatus(context.Background(), d.ID, domain.StatusDisabled); err != nil {
		t.Fatal(err)
	}
	for tool, args := range map[string]string{
		"edit_site_files":   `{"host":"harbour.example","files":{"index.html":"x"}}`,
		"follow_repository": `{"host":"harbour.example","repo":"o/r"}`,
		"sync_site":         `{"host":"harbour.example"}`,
	} {
		if _, err := runTool(t, a, tool, args); err == nil || !strings.Contains(err.Error(), "disabled") {
			t.Errorf("%s on a disabled domain = %v", tool, err)
		}
	}
}
