// SPDX-License-Identifier: Apache-2.0

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/customsite"
	"github.com/johalputt/vayupress/internal/users"
)

// bundleTestEnv points the bundle root at a temp dir and stands in a disk
// with free bytes to spare beyond the reserve.
func bundleTestEnv(t *testing.T, spare int64) {
	t.Helper()
	old, oldDisk := config.Cfg.MediaDir, bundleDisk
	config.Cfg.MediaDir = filepath.Join(t.TempDir(), "media")
	const total = uint64(100) << 30                                                    // reserve is 5 GiB
	bundleDisk = func(string) (uint64, uint64) { return total, uint64(5<<30 + spare) } //nolint:gosec // test sizes
	t.Cleanup(func() { config.Cfg.MediaDir, bundleDisk = old, oldDisk })
}

func bundleRouter(site bundleSite, admin bool) http.Handler {
	a := &App{}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if admin {
				req = withUser(req, &users.User{Role: users.RoleAdmin})
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Post("/b/uploads", a.handleBundleUploadStart(site))
	r.Post("/b/uploads/{upload}", a.handleBundleUploadChunk(site))
	r.Post("/b/uploads/{upload}/deploy", a.handleBundleUploadDeploy(site))
	r.Get("/b/download", a.handleBundleDownload(site))
	r.Post("/b/generations/{gen}/restore", a.handleBundleRestore(site))
	return r
}

func dirSite(dir, name string) bundleSite {
	return func(*http.Request) (string, string, bool) { return dir, name, true }
}

func call(t *testing.T, h http.Handler, method, target string, body []byte) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, bytes.NewReader(body)))
	var j map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &j)
	return rec, j
}

func errCode(j map[string]any) string {
	if e, ok := j["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

func startUpload(t *testing.T, h http.Handler) string {
	t.Helper()
	rec, j := call(t, h, http.MethodPost, "/b/uploads?size=0", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	return j["id"].(string)
}

// sendAll uploads data in pieces of n and deploys it.
func sendAll(t *testing.T, h http.Handler, data []byte, n int) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	id := startUpload(t, h)
	for off := 0; off < len(data); off += n {
		end := min(off+n, len(data))
		rec, _ := call(t, h, http.MethodPost, "/b/uploads/"+id+"?offset="+strconv.Itoa(off), data[off:end])
		if rec.Code != http.StatusOK {
			t.Fatalf("piece at %d: %d %s", off, rec.Code, rec.Body)
		}
	}
	return call(t, h, http.MethodPost, "/b/uploads/"+id+"/deploy", nil)
}

func siteZip(t *testing.T, files map[string][]byte, method uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The whole point: an archive larger than the old 60 MiB request cap, sent the
// way the console sends it, deploys.
func TestABundleLargerThanTheOldUploadCapDeploys(t *testing.T) {
	bundleTestEnv(t, 1<<30)
	video := make([]byte, 64<<20) // random, so the archive stays 64 MiB
	if _, err := rand.Read(video); err != nil {
		t.Fatal(err)
	}
	data := siteZip(t, map[string][]byte{"index.html": []byte("<h1>big</h1>"), "intro.mp4": video}, zip.Store)
	if len(data) <= 60<<20 {
		t.Fatalf("fixture is %d bytes, not beyond the old cap", len(data))
	}
	dir := t.TempDir()
	h := bundleRouter(dirSite(dir, "big.example"), true)
	rec, j := sendAll(t, h, data, bundleChunkBytes)
	if rec.Code != http.StatusOK {
		t.Fatalf("deploy: %d %s", rec.Code, rec.Body)
	}
	if j["files"].(float64) != 2 {
		t.Errorf("deployed %v files, want 2", j["files"])
	}
	if !customsite.Has(dir, "/intro.mp4") {
		t.Error("the large file is not being served")
	}
	if m, _ := filepath.Glob(filepath.Join(dir, ".upload-*")); len(m) != 0 {
		t.Errorf("the finished upload was left on disk: %v", m)
	}
}

// A piece resent after its answer was lost is refused with the offset the
// server holds, and the console resumes from there — the archive is not
// corrupted by the duplicate.
func TestAResentPieceIsNotAppendedTwice(t *testing.T) {
	bundleTestEnv(t, 1<<30)
	data := siteZip(t, map[string][]byte{"index.html": []byte(strings.Repeat("<p>x</p>", 4000))}, zip.Store)
	dir := t.TempDir()
	h := bundleRouter(dirSite(dir, "a.example"), true)
	id := startUpload(t, h)
	half := len(data) / 2
	if rec, _ := call(t, h, http.MethodPost, "/b/uploads/"+id+"?offset=0", data[:half]); rec.Code != http.StatusOK {
		t.Fatalf("first piece: %d", rec.Code)
	}
	rec, j := call(t, h, http.MethodPost, "/b/uploads/"+id+"?offset=0", data[:half])
	if rec.Code != http.StatusConflict || errCode(j) != "offset-mismatch" {
		t.Fatalf("the resent piece got %d %s, want 409 offset-mismatch", rec.Code, rec.Body)
	}
	if got := int(j["offset"].(float64)); got != half {
		t.Fatalf("the 409 reported offset %d, want %d", got, half)
	}
	if rec, _ := call(t, h, http.MethodPost, "/b/uploads/"+id+"?offset="+strconv.Itoa(half), data[half:]); rec.Code != http.StatusOK {
		t.Fatalf("second piece: %d", rec.Code)
	}
	if rec, _ := call(t, h, http.MethodPost, "/b/uploads/"+id+"/deploy", nil); rec.Code != http.StatusOK {
		t.Fatalf("deploy after a resent piece: %d %s", rec.Code, rec.Body)
	}
}

// The size is required: without it the start cannot refuse an archive that
// will never fit, and the operator finds out a gigabyte later.
func TestAnUploadMustDeclareItsSize(t *testing.T) {
	bundleTestEnv(t, 1<<30)
	h := bundleRouter(dirSite(t.TempDir(), "a.example"), true)
	for _, q := range []string{"", "?size=", "?size=-1", "?size=big"} {
		if rec, j := call(t, h, http.MethodPost, "/b/uploads"+q, nil); rec.Code != http.StatusBadRequest || errCode(j) != "bad-size" {
			t.Errorf("start with %q got %d %s, want 400 bad-size", q, rec.Code, rec.Body)
		}
	}
}

func TestAPieceOverTheChunkLimitIsRefused(t *testing.T) {
	bundleTestEnv(t, 1<<30)
	h := bundleRouter(dirSite(t.TempDir(), "a.example"), true)
	id := startUpload(t, h)
	rec, j := call(t, h, http.MethodPost, "/b/uploads/"+id+"?offset=0", make([]byte, bundleChunkMax+1))
	if rec.Code != http.StatusRequestEntityTooLarge || errCode(j) != "chunk-too-large" {
		t.Errorf("an oversized piece got %d %s, want 413 chunk-too-large", rec.Code, rec.Body)
	}
}

// The disk bound, while uploading: a piece that would eat into the reserve is
// refused as no-space, and the partial upload is deleted so it stops holding
// the disk it did get.
func TestAnUploadThatWouldEatTheReserveIsRefused(t *testing.T) {
	bundleTestEnv(t, 10)
	dir := t.TempDir()
	h := bundleRouter(dirSite(dir, "a.example"), true)
	// Refused at the start when the archive alone cannot fit, before any piece
	// is sent and without leaving a placeholder behind.
	rec, j := call(t, h, http.MethodPost, "/b/uploads?size=11", nil)
	if rec.Code != http.StatusInsufficientStorage || errCode(j) != "no-space" {
		t.Fatalf("starting an 11-byte upload with 10 bytes of room got %d %s, want 507 no-space", rec.Code, rec.Body)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, ".upload-*")); len(m) != 0 {
		t.Errorf("the refused start left a placeholder: %v", m)
	}
	rec, j = call(t, h, http.MethodPost, "/b/uploads?size=10", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("starting a 10-byte upload with 10 bytes of room got %d %s, want 201", rec.Code, rec.Body)
	}
	id := j["id"].(string)
	// Exactly the room fits: the rule is "more than", not "at least".
	if rec, _ := call(t, h, http.MethodPost, "/b/uploads/"+id+"?offset=0", make([]byte, 10)); rec.Code != http.StatusOK {
		t.Fatalf("a 10-byte piece with 10 bytes of room was refused: %d %s", rec.Code, rec.Body)
	}
	// The declared size is the archive's word, not a reservation: a piece that
	// outruns the room is refused on its own.
	rec, j = call(t, h, http.MethodPost, "/b/uploads/"+id+"?offset=10", make([]byte, 11))
	if rec.Code != http.StatusInsufficientStorage || errCode(j) != "no-space" {
		t.Fatalf("an 11-byte piece with 10 bytes of room got %d %s, want 507 no-space", rec.Code, rec.Body)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, ".upload-*")); len(m) != 0 {
		t.Errorf("the refused upload was left on disk: %v", m)
	}
}

// The disk bound, while unpacking: a small archive that inflates past the
// room is refused as no-space at deploy, not as a malformed bundle.
func TestABundleThatUnpacksPastTheRoomIsRefusedAtDeploy(t *testing.T) {
	bundleTestEnv(t, 64<<10)
	data := siteZip(t, map[string][]byte{"index.html": make([]byte, 1<<20)}, zip.Deflate)
	if len(data) >= 64<<10 {
		t.Fatalf("fixture compresses to %d bytes; it must fit the room to reach the deploy", len(data))
	}
	dir := t.TempDir()
	rec, j := sendAll(t, bundleRouter(dirSite(dir, "a.example"), true), data, bundleChunkBytes)
	if rec.Code != http.StatusInsufficientStorage || errCode(j) != "no-space" {
		t.Fatalf("a 1 MiB unpack with 64 KiB of room got %d %s, want 507 no-space", rec.Code, rec.Body)
	}
	if customsite.Deployed(dir) {
		t.Error("the refused bundle went live")
	}
}

// Each rule of the budget, one seed each.
func TestBundleBudgetKeepsTheReserve(t *testing.T) {
	bundleTestEnv(t, 0)
	for _, c := range []struct {
		name        string
		total, free uint64
		want        int64
	}{
		{"5% of a large disk is kept", 100 << 30, 10 << 30, 5 << 30},
		{"at least 1 GiB is kept on a small disk", 2 << 30, 3 << 29, 1 << 29},
		{"less free than the reserve is no room, not negative", 100 << 30, 1 << 30, 0},
		{"at most 10 GiB is kept on a large disk", 2000 << 30, 50 << 30, 40 << 30},
		{"unknown disk is unbounded", 0, 0, 1<<63 - 1},
	} {
		bundleDisk = func(string) (uint64, uint64) { return c.total, c.free }
		if got := bundleBudget(); got != c.want {
			t.Errorf("%s: budget %d, want %d", c.name, got, c.want)
		}
	}
}

// The id becomes part of a path, so anything that could leave the site's
// directory is refused before a path is built — whatever the router allows.
func TestAnUploadIdCannotNameAPathOutsideTheSite(t *testing.T) {
	for _, id := range []string{
		"../../../../etc/cron.d/x", "..", "a/b", "", // separators, dots, empty
		"ABCDEF",                // uppercase is not the id alphabet
		strings.Repeat("a", 65), // past the bound
		"abc\n",                 // a trailing newline is not the end of the id
	} {
		if p, ok := uploadPath("/srv/site", id); ok {
			t.Errorf("uploadPath accepted %q as %q", id, p)
		}
	}
	if p, ok := uploadPath("/srv/site", strings.Repeat("a", 32)); !ok || p != "/srv/site/.upload-"+strings.Repeat("a", 32)+".zip" {
		t.Errorf("a genuine id resolved to %q, %v", p, ok)
	}
}

// An upload belongs to the site it was started on. Its id deployed through
// another site's URL finds nothing there.
func TestAnUploadCannotBeDeployedToAnotherSite(t *testing.T) {
	bundleTestEnv(t, 1<<30)
	a, b := testDomain("aaaaaaaaaaaaaaaaaaaaaaaa", "a.example"), testDomain("bbbbbbbbbbbbbbbbbbbbbbbb", "b.example")
	for _, d := range []string{scopedBundleDir(a), scopedBundleDir(b)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	scoped := func(next http.Handler, id string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			d := a
			if id == "b" {
				d = b
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxScopedDomainKey, d)))
		})
	}
	ha, hb := scoped(bundleRouter(scopedBundleSite, true), "a"), scoped(bundleRouter(scopedBundleSite, true), "b")
	data := siteZip(t, map[string][]byte{"index.html": []byte("A")}, zip.Store)
	id := startUpload(t, ha)
	if rec, _ := call(t, ha, http.MethodPost, "/b/uploads/"+id+"?offset=0", data); rec.Code != http.StatusOK {
		t.Fatalf("piece: %d", rec.Code)
	}
	rec, j := call(t, hb, http.MethodPost, "/b/uploads/"+id+"/deploy", nil)
	if rec.Code != http.StatusNotFound || errCode(j) != "unknown-upload" {
		t.Errorf("site A's upload deployed through site B got %d %s, want 404", rec.Code, rec.Body)
	}
	if customsite.Deployed(scopedBundleDir(b)) {
		t.Error("site A's bundle went live on site B")
	}
}

func TestBundleEndpointsAreAdminOnly(t *testing.T) {
	bundleTestEnv(t, 1<<30)
	h := bundleRouter(dirSite(t.TempDir(), "a.example"), false)
	id := strings.Repeat("a", 32)
	for _, c := range [][2]string{
		{http.MethodPost, "/b/uploads?size=0"}, {http.MethodPost, "/b/uploads/" + id + "?offset=0"},
		{http.MethodPost, "/b/uploads/" + id + "/deploy"}, {http.MethodGet, "/b/download"},
		{http.MethodPost, "/b/generations/01790000000000000000/restore"},
	} {
		if rec, _ := call(t, h, c[0], c[1], nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s without an admin: %d, want 403", c[0], c[1], rec.Code)
		}
	}
}

// Download returns the live site as an attachment named for the site, and it
// deploys again unchanged.
func TestTheLiveBundleDownloadsAsAZip(t *testing.T) {
	bundleTestEnv(t, 1<<30)
	dir := t.TempDir()
	h := bundleRouter(dirSite(dir, "shop.example"), true)
	if rec, j := call(t, h, http.MethodGet, "/b/download", nil); rec.Code != http.StatusNotFound || errCode(j) != "no-bundle" {
		t.Errorf("download with nothing deployed: %d %s, want 404 no-bundle", rec.Code, rec.Body)
	}
	files := map[string][]byte{"index.html": []byte("<h1>shop</h1>"), "css/s.css": []byte("body{}")}
	if rec, _ := sendAll(t, h, siteZip(t, files, zip.Deflate), bundleChunkBytes); rec.Code != http.StatusOK {
		t.Fatalf("deploy: %d", rec.Code)
	}
	rec, _ := call(t, h, http.MethodGet, "/b/download", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("download: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="shop.example-website-`) {
		t.Errorf("Content-Disposition %q does not name the site", cd)
	}
	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("the download is not a zip: %v", err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		got[f.Name] = string(b)
	}
	for name, want := range files {
		if got[name] != string(want) {
			t.Errorf("%s downloaded as %q, want %q", name, got[name], want)
		}
	}
	if len(got) != len(files) {
		t.Errorf("downloaded %d files, want %d: %v", len(got), len(files), got)
	}
}

// A tab closed mid-upload leaves a partial archive. The next upload to ANY
// site deletes it once it is a day old — the site it belongs to may never be
// uploaded to again — and leaves a recent one alone, which may be another tab
// still sending.
func TestAbandonedUploadsAreSwept(t *testing.T) {
	bundleTestEnv(t, 1<<30)
	root := customSiteRoot()
	other := filepath.Join(root, strings.Repeat("c", 24))
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	stalePrimary := filepath.Join(root, ".upload-"+strings.Repeat("a", 32)+".zip")
	staleHosted := filepath.Join(other, ".upload-"+strings.Repeat("d", 32)+".zip")
	fresh := filepath.Join(other, ".upload-"+strings.Repeat("b", 32)+".zip")
	for _, p := range []string{stalePrimary, staleHosted, fresh} {
		if err := os.WriteFile(p, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-bundleUploadMaxAge - time.Minute)
	for _, p := range []string{stalePrimary, staleHosted} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	startUpload(t, bundleRouter(dirSite(t.TempDir(), "unrelated.example"), true))
	for _, p := range []string{stalePrimary, staleHosted} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("a day-old partial upload survived: %s", p)
		}
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("an upload still in progress was deleted: %v", err)
	}
}

// Rendering the Website page asks how much room there is; it must not create
// the bundle directory to find out.
func TestMeasuringTheRoomCreatesNothing(t *testing.T) {
	bundleTestEnv(t, 1<<30)
	if err := os.MkdirAll(config.Cfg.MediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var measured string
	bundleDisk = func(p string) (uint64, uint64) { measured = p; return 100 << 30, 50 << 30 }
	_ = bundleRoomLine()
	if _, err := os.Stat(customSiteRoot()); !os.IsNotExist(err) {
		t.Errorf("measuring the room created %s", customSiteRoot())
	}
	if measured != filepath.Dir(customSiteRoot()) {
		t.Errorf("measured %q, want the data root %q", measured, filepath.Dir(customSiteRoot()))
	}
}

// Earlier uploads are listed on the page and any one can be restored from
// it; an id the history does not hold restores nothing.
func TestAnEarlierUploadCanBeRestoredFromTheConsole(t *testing.T) {
	bundleTestEnv(t, 1<<30)
	dir := t.TempDir()
	h := bundleRouter(dirSite(dir, "a.example"), true)
	for _, v := range []string{"FIRST", "SECOND", "THIRD"} {
		if rec, _ := sendAll(t, h, siteZip(t, map[string][]byte{"index.html": []byte(v)}, zip.Store), bundleChunkBytes); rec.Code != http.StatusOK {
			t.Fatalf("deploy %s: %d", v, rec.Code)
		}
	}
	list := bundleHistoryHTML(dir, "/b")
	if n := strings.Count(list, "data-bundle-restore="); n != 2 {
		t.Fatalf("the page lists %d earlier uploads, want 2:\n%s", n, list)
	}
	oldest := customsite.History(dir)[1].ID
	if !strings.Contains(list, `data-bundle-restore="/b/generations/`+oldest+`/restore"`) {
		t.Errorf("the list does not offer the oldest upload")
	}
	if rec, _ := call(t, h, http.MethodPost, "/b/generations/"+oldest+"/restore", nil); rec.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "current", "index.html"))
	if string(b) != "FIRST" {
		t.Errorf("after restoring the first upload the site holds %q", b)
	}
	if rec, j := call(t, h, http.MethodPost, "/b/generations/../../x/restore", nil); rec.Code != http.StatusNotFound && errCode(j) != "unknown-generation" {
		t.Errorf("an id outside the history: %d", rec.Code)
	}
	if rec, j := call(t, h, http.MethodPost, "/b/generations/01790000000000000000/restore", nil); rec.Code != http.StatusNotFound || errCode(j) != "unknown-generation" {
		t.Errorf("an id the history does not hold: %d %s", rec.Code, rec.Body)
	}
}
