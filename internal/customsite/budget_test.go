// SPDX-License-Identifier: Apache-2.0

package customsite

import (
	"archive/zip"
	"bytes"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// deployBytes deploys an in-memory archive with room to spare, for tests whose
// subject is not the budget.
func deployBytes(base string, b []byte) (Manifest, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return Manifest{}, err
	}
	return Deploy(base, zr, 1<<30)
}

func readerOf(t *testing.T, b []byte) *zip.Reader {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

// The fixed caps are gone: a site beyond every one of them — 50 MiB total,
// 25 MiB for one file, 3000 files — deploys when the machine has room for it.
func TestASiteBeyondTheOldFixedCapsDeploys(t *testing.T) {
	files := map[string]string{
		"index.html":      "<h1>big</h1>",
		"media/intro.mp4": strings.Repeat("\x00", 60<<20),
	}
	for i := 0; i < 3100; i++ {
		files["docs/p"+strconv.Itoa(i)+".html"] = "p"
	}
	base := t.TempDir()
	m, err := Deploy(base, readerOf(t, zipOf(t, files)), 1<<30)
	if err != nil {
		t.Fatalf("a site larger than the old caps was refused with room to spare: %v", err)
	}
	if m.Files != len(files) {
		t.Errorf("deployed %d files, want %d", m.Files, len(files))
	}
	if fi, err := os.Stat(filepath.Join(base, "current", "media", "intro.mp4")); err != nil || fi.Size() != 60<<20 {
		t.Errorf("the 60 MiB file did not land whole: %v", err)
	}
}

// A bundle that does not fit is refused on its declaration, before extraction,
// and the reason says it is about space — the operator frees disk, they do not
// rebuild the zip.
func TestABundleBeyondTheBudgetIsRefusedAsNoSpace(t *testing.T) {
	base := t.TempDir()
	z := zipOf(t, map[string]string{"index.html": strings.Repeat("a", 1000)})
	_, err := Deploy(base, readerOf(t, z), 999)
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("a bundle 1 byte over the budget was not refused as ErrNoSpace: %v", err)
	}
	if !strings.Contains(err.Error(), "the 999 bytes there is room for") {
		t.Errorf("the refusal does not say how much room there was: %v", err)
	}
	if Deployed(base) {
		t.Error("a refused bundle went live")
	}
	// Exactly at the budget fits: the check is "more than", not "at least".
	if _, err := Deploy(base, readerOf(t, z), 1000); err != nil {
		t.Errorf("a bundle exactly the size of the budget was refused: %v", err)
	}
}

// The budget is for the whole bundle, not each file: two files that fit alone
// and not together are refused.
func TestTheBudgetIsForTheWholeBundle(t *testing.T) {
	z := zipOf(t, map[string]string{"index.html": strings.Repeat("a", 600), "b.html": strings.Repeat("b", 600)})
	if _, err := Deploy(t.TempDir(), readerOf(t, z), 1000); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("two 600-byte files deployed into 1000 bytes of room: %v", err)
	}
}

// Deploy and Rollback share fixed directory names under base. Concurrent
// deploys to one site must each either finish or wait, never destroy the
// other's staging mid-extraction.
func TestConcurrentDeploysToOneSiteAllSucceed(t *testing.T) {
	files := map[string]string{"index.html": "x"}
	for i := 0; i < 300; i++ {
		files["p/"+strconv.Itoa(i)+".html"] = strings.Repeat("y", 2048)
	}
	z := zipOf(t, files)
	base := t.TempDir()
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			_, err := deployBytes(base, z)
			errs <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Errorf("a deploy racing another failed: %v", err)
		}
	}
	if !Deployed(base) {
		t.Error("nothing is live after eight deploys")
	}
}

// ZIP64 lets an archive declare entries near 2^64. Summed, two of them wrap to
// a small number and would pass a total-versus-budget comparison.
func TestDeclaredSizesCannotWrapPastTheBudget(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"index.html", "b.html"} {
		w, err := zw.CreateRaw(&zip.FileHeader{
			Name: name, Method: zip.Store,
			UncompressedSize64: 1<<63 + 8, CompressedSize64: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Deploy(t.TempDir(), readerOf(t, buf.Bytes()), 1<<20)
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("entries declaring 2^63+8 bytes each were not refused as ErrNoSpace: %v", err)
	}
}

// The declaration check is the whole space control, which is sound only
// because archive/zip will not let an entry decompress past its declared size.
// This pins that assumption: if the reader ever stopped enforcing it, a zip
// bomb declaring a few bytes could fill the disk, and this test is where that
// would show.
func TestALyingHeaderIsRefusedByTheReader(t *testing.T) {
	body := bytes.Repeat([]byte("z"), 4096)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name: "index.html", Method: zip.Store,
		CRC32:              crc32.ChecksumIEEE(body),
		UncompressedSize64: 10, CompressedSize64: uint64(len(body)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	if _, err := Deploy(base, readerOf(t, buf.Bytes()), 100); err == nil {
		t.Fatal("an entry declaring 10 bytes and holding 4096 was deployed")
	}
	if Deployed(base) {
		t.Error("the lying bundle went live")
	}
	if _, err := os.Stat(filepath.Join(base, ".staging")); !os.IsNotExist(err) {
		t.Errorf("the refused extraction left staging behind: %v", err)
	}
}

// The download is the site as served: every file, same bytes, same paths, and
// nothing from beside current/ (the previous generation, the manifest).
func TestExportIsTheLiveSiteAndNothingElse(t *testing.T) {
	base := t.TempDir()
	if _, err := deployBytes(base, zipOf(t, map[string]string{"index.html": "OLD"})); err != nil {
		t.Fatal(err)
	}
	live := map[string]string{"index.html": "NEW", "assets/site.css": "body{}", "img/a.png": "\x89PNG"}
	if _, err := deployBytes(base, zipOf(t, live)); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Export(&out, base); err != nil {
		t.Fatalf("Export: %v", err)
	}
	zr := readerOf(t, out.Bytes())
	got := map[string]string{}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		got[f.Name] = string(b)
	}
	if len(got) != len(live) {
		t.Errorf("exported %v, want exactly %v", got, live)
	}
	for name, want := range live {
		if got[name] != want {
			t.Errorf("%s exported as %q, want %q", name, got[name], want)
		}
	}
	// And it goes straight back in.
	again := t.TempDir()
	if _, err := deployBytes(again, out.Bytes()); err != nil {
		t.Errorf("an exported site could not be deployed again: %v", err)
	}
}

func TestExportWithNothingDeployedFails(t *testing.T) {
	if err := Export(io.Discard, t.TempDir()); err == nil {
		t.Error("Export of a site with no bundle returned no error")
	}
}
