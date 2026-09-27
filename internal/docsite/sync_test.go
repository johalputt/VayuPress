// SPDX-License-Identifier: Apache-2.0

package docsite

import (
	"context"
	"crypto/sha1" //nolint:gosec // git's own naming, as sync.go checks it
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// forge plays GitHub for one repository: its branch, its tree and its files.
type forge struct {
	commit  string
	files   map[string]string // path → contents
	tamper  string            // a path served with other contents than its name says
	fetches atomic.Int32
}

func blobSHA(s string) string {
	h := sha1.New() //nolint:gosec // see the import
	h.Write([]byte("blob " + strconv.Itoa(len(s)) + "\x00" + s))
	return hex.EncodeToString(h.Sum(nil))
}

func (f *forge) serve(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/info/refs"):
			// Pkt-lines, as git sends them: the first ref carries capabilities.
			w.Write([]byte("001e# service=git-upload-pack\n0000" +
				"0099" + f.commit + " HEAD\x00multi_ack side-band-64k\n" +
				"003f" + strings.Repeat("a", 40) + " refs/heads/main-old\n" +
				"003f" + f.commit + " refs/heads/main\n0000"))
		case strings.Contains(r.URL.Path, "/git/trees/"):
			var tree []map[string]string
			for p, c := range f.files {
				tree = append(tree, map[string]string{"path": p, "type": "blob", "sha": blobSHA(c)})
			}
			tree = append(tree, map[string]string{"path": "docs/../../escape", "type": "blob", "sha": blobSHA("x")},
				map[string]string{"path": "cmd/main.go", "type": "blob", "sha": blobSHA("package main")})
			json.NewEncoder(w).Encode(map[string]any{"tree": tree})
		default:
			f.fetches.Add(1)
			p := strings.TrimPrefix(r.URL.Path, "/o/r/"+f.commit+"/")
			c, ok := f.files[p]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if p == f.tamper {
				c += " (not what the commit holds)"
			}
			w.Write([]byte(c))
		}
	}))
}

func syncer(t *testing.T, srv *httptest.Server) *Syncer {
	return &Syncer{Dir: t.TempDir(), Client: srv.Client(), Git: srv.URL, API: srv.URL, Raw: srv.URL}
}

func TestASiteFollowsItsBranch(t *testing.T) {
	f := &forge{commit: strings.Repeat("1", 40), files: map[string]string{
		"docs/OPERATIONS.md": "# Operations Runbook — VayuPress\n", "docs/screenshots/a.png": "PNG", "CHANGELOG.md": "# Changelog\n",
	}}
	srv := f.serve(t)
	defer srv.Close()
	s := syncer(t, srv)
	src := Source{Repo: "o/r", Branch: "main"}
	published := 0
	var seen fs.FS
	publish := func(repo fs.FS, st *SyncState) error { published++; seen = repo; return nil }

	st, err := s.Sync(context.Background(), src, publish)
	if err != nil || published != 1 || st.Commit != f.commit || len(st.Files) != 3 {
		t.Fatalf("first sync: %v, published %d, %+v", err, published, st)
	}
	if b, _ := fs.ReadFile(seen, "docs/OPERATIONS.md"); string(b) != f.files["docs/OPERATIONS.md"] {
		t.Error("the working copy does not hold the branch's file")
	}
	if _, err := fs.Stat(seen, "cmd/main.go"); err == nil {
		t.Error("a file the site is not built from was fetched")
	}
	if len(st.Updates) != 0 {
		t.Errorf("a first sync reported every file as news: %+v", st.Updates)
	}

	// Nothing new on the branch: nothing fetched, nothing published.
	before := f.fetches.Load()
	if _, err := s.Sync(context.Background(), src, publish); err != nil || published != 1 || f.fetches.Load() != before {
		t.Fatalf("an unchanged branch was fetched or published again: %v, %d, %d", err, published, f.fetches.Load()-before)
	}

	// One guide changes and one screenshot is new: only those are fetched, and
	// the feed says so by the guide's own title.
	f.commit = strings.Repeat("2", 40)
	f.files["docs/OPERATIONS.md"] = "# Operations Runbook — VayuPress\n\nMore.\n"
	f.files["docs/screenshots/b.png"] = "PNG2"
	before = f.fetches.Load()
	st, err = s.Sync(context.Background(), src, publish)
	if err != nil || published != 2 {
		t.Fatalf("a moved branch was not published: %v", err)
	}
	if got := f.fetches.Load() - before; got != 2 {
		t.Errorf("fetched %d files, want only the 2 that changed", got)
	}
	if len(st.Updates) != 2 || st.Updates[0].Title != "Operations Runbook" || st.Updates[0].Note != "Updated" || st.Updates[1].Title != "1 screenshot" {
		t.Errorf("the feed says %+v", st.Updates)
	}
}

// A file whose contents are not what the commit names is refused, by name;
// nothing is published and the live site stays the last one that built, with
// the state saying why.
func TestAFileThatIsNotWhatTheCommitNamesIsRefused(t *testing.T) {
	f := &forge{commit: strings.Repeat("1", 40), files: map[string]string{"docs/A.md": "# A\n"}}
	srv := f.serve(t)
	defer srv.Close()
	s := syncer(t, srv)
	src := Source{Repo: "o/r", Branch: "main"}
	if _, err := s.Sync(context.Background(), src, func(fs.FS, *SyncState) error { return nil }); err != nil {
		t.Fatal(err)
	}
	f.commit = strings.Repeat("2", 40)
	f.files["docs/A.md"] = "# A, changed\n"
	f.tamper = "docs/A.md"
	called := false
	st, err := s.Sync(context.Background(), src, func(fs.FS, *SyncState) error { called = true; return nil })
	if err == nil || !strings.Contains(err.Error(), "docs/A.md is not the file the commit names") {
		t.Fatalf("a tampered file was not refused by name: %v", err)
	}
	if called || st.Commit != strings.Repeat("1", 40) || st.Err == "" {
		t.Errorf("after a refusal: published %v, live commit %s, state error %q", called, st.Commit, st.Err)
	}
	// Put right, the next sync goes through even though the branch did not move.
	f.tamper = ""
	if st, err = s.Sync(context.Background(), src, func(fs.FS, *SyncState) error { return nil }); err != nil || st.Err != "" || st.Commit != f.commit {
		t.Errorf("the retry did not go live: %v %+v", err, st)
	}
}

func TestOnlyASourceGitHubWouldNameIsFollowed(t *testing.T) {
	for _, bad := range []Source{{"o", "main", ""}, {"o/r/x", "main", ""}, {"o/../r", "main", ""}, {"../r", "main", ""}, {"o/..", "main", ""},
		{"o/r", "../main", ""}, {"o/r", "ma in", ""}, {"o/r", "/main", ""},
		{"o/r", "main", "../site"}, {"o/r", "main", "/site"}, {"o/r", "main", "."}, {"o/r", "main", "site/"}} {
		if bad.Valid() == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	for _, good := range []Source{{"johalputt/vayupress", "main", ""}, {"johalputt/vayupress", "main", "docs/site/public"}} {
		if good.Valid() != nil {
			t.Errorf("%+v was refused", good)
		}
	}
}

// A site that is a folder: only that folder is fetched, and it is served as it
// is, less what a site cannot carry, which is named.
func TestASiteThatIsAFolderOfTheRepository(t *testing.T) {
	f := &forge{commit: strings.Repeat("3", 40), files: map[string]string{
		"site/index.html": "<h1>Home</h1>", "site/img/a.png": "\x89PNG", "site/README.md": "# notes",
		"docs/A.md": "# A\n",
	}}
	srv := f.serve(t)
	defer srv.Close()
	s := syncer(t, srv)
	var built Bundle
	st, err := s.Sync(context.Background(), Source{Repo: "o/r", Branch: "main", Dir: "site"}, func(repo fs.FS, st *SyncState) error {
		b, skipped, err := Static(repo, "site")
		built, st.Skipped = b, skipped
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Files) != 3 || f.fetches.Load() != 3 {
		t.Errorf("fetched %d files and recorded %v; only the folder's three were wanted", f.fetches.Load(), st.Files)
	}
	if len(built) != 2 || string(built["index.html"]) != "<h1>Home</h1>" || string(built["img/a.png"]) != "\x89PNG" {
		t.Errorf("the site is %v", built)
	}
	if len(st.Skipped) != 1 || st.Skipped[0] != "README.md" || s.State().Skipped[0] != "README.md" {
		t.Errorf("what was left out is not named in the state: %v", st.Skipped)
	}

	if _, err := s.Sync(context.Background(), Source{Repo: "o/r", Branch: "main", Dir: "web"}, func(fs.FS, *SyncState) error { return nil }); err == nil || !strings.Contains(err.Error(), "no web/ folder") {
		t.Errorf("a folder the branch lacks = %v", err)
	}
	f.commit = strings.Repeat("4", 40)
	delete(f.files, "site/index.html")
	if _, err := s.Sync(context.Background(), Source{Repo: "o/r", Branch: "main", Dir: "site"}, func(repo fs.FS, st *SyncState) error {
		_, _, err := Static(repo, "site")
		return err
	}); err == nil || !strings.Contains(err.Error(), "no index.html") {
		t.Errorf("a folder with no front page = %v", err)
	}
}
