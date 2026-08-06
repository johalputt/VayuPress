// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// The attack these tests hold the line on.
//
// storeValidatedMedia content-addresses what it writes, so the same file twice
// costs one file — which reads like a bound and is not one. Distinct bytes were
// unlimited. An API key granted media:write and nothing else, the weakest
// credential the connector can mint, could therefore upload unique images until
// the filesystem was full; and a full disk here does not mean "no more images",
// it means SQLite cannot write and the site answers 502. Taking an install down
// is not a privilege media:write is supposed to carry.
//
// What has to be true, and each has its own test below:
//
//   - a file that fits is stored;
//   - a file that does not fit is refused BEFORE anything is written, with a
//     sentinel a caller can tell apart from "too large" or "wrong type";
//   - a duplicate is admitted even at quota, because it consumes nothing and
//     refusing it would be a failure the operator cannot act on;
//   - the connector inherits all of it from the one enforcement point, rather
//     than carrying a second copy of the rule that can drift.

// mediaQuotaDir points MediaDir at a fresh temporary directory with quota bytes
// of headroom and restores both afterwards.
//
// It resets the cached usage total on the way in and on the way out. That total
// is process-global: without the reset a test would assert against a number some
// earlier test computed for a directory that no longer exists.
func mediaQuotaDir(t *testing.T, quota int64) string {
	t.Helper()
	dir := t.TempDir()
	oldDir, oldQuota := config.Cfg.MediaDir, config.Cfg.MediaQuotaBytes
	config.Cfg.MediaDir, config.Cfg.MediaQuotaBytes = dir, quota
	invalidateMediaUsage()
	t.Cleanup(func() {
		config.Cfg.MediaDir, config.Cfg.MediaQuotaBytes = oldDir, oldQuota
		invalidateMediaUsage()
	})
	return dir
}

// mediaDirNames lists what is actually on disk, sorted. Asserting on the set of
// filenames rather than on a count is deliberate: a count cannot tell "nothing
// was written" apart from "one file was written and another vanished".
func mediaDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read media dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// distinctPNG returns a small valid PNG whose pixels depend on seed, so two
// seeds produce two different content hashes and therefore two different stored
// files. It goes through the real encoder because storeValidatedMedia decodes
// and re-encodes rasters — a magic-number stub would never reach the store.
func distinctPNG(t *testing.T, seed uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			img.Set(x, y, color.RGBA{R: seed, G: uint8(x * 40), B: uint8(y * 40), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// Under the ceiling, nothing changes: the file is stored and the bytes on disk
// are the bytes the caller was told about.
func TestMediaUnderQuotaIsStored(t *testing.T) {
	dir := mediaQuotaDir(t, 1<<20)

	res, err := storeValidatedMedia(distinctPNG(t, 11), true)
	if err != nil {
		t.Fatalf("an upload well under the quota was refused: %v", err)
	}
	info, statErr := os.Stat(filepath.Join(dir, res.Name))
	if statErr != nil {
		t.Fatalf("the store reported success but %s is not on disk: %v", res.Name, statErr)
	}
	if int(info.Size()) != res.Size {
		t.Errorf("stored %d bytes but reported %d — the size the quota accounts for is the one "+
			"the caller sees, and they have to be the same number", info.Size(), res.Size)
	}
}

// The finding itself. Space already consumed has to count against the ceiling,
// the refusal has to be its own sentinel, and — the part that makes it a quota
// rather than a report — nothing may reach disk on the way to being refused.
func TestMediaOverQuotaIsRefusedAndNothingIsWritten(t *testing.T) {
	const quota = 4096
	dir := mediaQuotaDir(t, quota)

	// A pre-existing library sitting exactly on the ceiling, written directly so
	// the store has to discover it by counting the directory rather than by
	// remembering its own writes. Exactly on it, not near it: the first draft
	// left 96 bytes free and a 4x4 PNG fits in 96, so the store admitted it —
	// correctly — and the test was measuring its own fixture, not the ceiling.
	filler := filepath.Join(dir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png")
	if err := os.WriteFile(filler, make([]byte, quota), 0o644); err != nil {
		t.Fatalf("seed media dir: %v", err)
	}
	invalidateMediaUsage()

	before := mediaDirNames(t, dir)

	_, err := storeValidatedMedia(distinctPNG(t, 22), true)
	if !errors.Is(err, errMediaQuotaExceeded) {
		t.Fatalf("storing over the quota gave %v, want errMediaQuotaExceeded. Without a distinct "+
			"sentinel every caller reports this as a bad file, and an operator spends the outage "+
			"looking at the image instead of at the disk", err)
	}

	after := mediaDirNames(t, dir)
	if strings.Join(before, ",") != strings.Join(after, ",") {
		t.Errorf("the media directory changed during a refused upload: %v → %v.\n"+
			"The refusal has to happen before the write. A ceiling enforced after the bytes land "+
			"still lets a caller fill the disk one refused upload at a time.", before, after)
	}
}

// A duplicate consumes no additional space, so refusing it at quota would be a
// false failure — the editor re-pasting a screenshot it already uploaded would
// look like a broken library, and there would be nothing the operator could
// delete to fix it.
//
// The distinct upload in the second half is what stops this test from passing
// against a quota that has simply stopped working: at that moment usage is over
// the ceiling, so anything NEW must still be refused.
func TestDuplicateMediaIsAdmittedAtQuota(t *testing.T) {
	dir := mediaQuotaDir(t, 1<<20)

	raw := distinctPNG(t, 33)
	first, err := storeValidatedMedia(raw, true)
	if err != nil {
		t.Fatalf("first upload refused: %v", err)
	}

	// Drop the ceiling below what is already stored — the state an operator ends
	// up in after tightening the limit, and the state an attacker's uploads
	// eventually create anyway.
	config.Cfg.MediaQuotaBytes = 1

	again, err := storeValidatedMedia(raw, true)
	if err != nil {
		t.Fatalf("re-uploading identical bytes at quota was refused: %v.\nIt is already on disk "+
			"under the same content hash, so storing it costs nothing; refusing it fails an "+
			"operation that consumes no space.", err)
	}
	if again.Name != first.Name {
		t.Errorf("the same bytes stored as %q then %q — content addressing is what makes the "+
			"duplicate free, so two names means two files", first.Name, again.Name)
	}
	if names := mediaDirNames(t, dir); len(names) != 1 {
		t.Errorf("media directory holds %v after two uploads of one file", names)
	}

	if _, err := storeValidatedMedia(distinctPNG(t, 44), true); !errors.Is(err, errMediaQuotaExceeded) {
		t.Errorf("a NEW file was admitted over the quota (%v). The duplicate exemption is the "+
			"one thing that may pass here; if it widens to anything else the ceiling is gone.", err)
	}
}

// The connector is the credential this whole control exists for: an upload_media
// key can do nothing but write media, and it must not be able to fill the disk.
//
// The test drives the real JSON-RPC surface, and it uploads successfully first.
// That ordering matters — a refusal on its own would also be produced by a
// connector that is simply broken, and this has to show the tool working and
// then hitting the SAME ceiling the direct path hits, having never implemented
// one of its own.
func TestMCPUploadInheritsTheMediaQuota(t *testing.T) {
	dir := mediaQuotaDir(t, 1<<20)
	srv := (&App{}).buildMCPServer()
	key := scopedKey([2]string{"media", "write"})

	upload := func(raw []byte) (isError bool, text string) {
		t.Helper()
		args, err := json.Marshal(map[string]any{
			"content": base64.StdEncoding.EncodeToString(raw),
		})
		if err != nil {
			t.Fatalf("marshal arguments: %v", err)
		}
		body, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "upload_media", "arguments": json.RawMessage(args)},
		})
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, keyCtxRequest(string(body), key))

		var out struct {
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("bad tools/call response: %v (%s)", err, rec.Body.String())
		}
		if len(out.Result.Content) == 0 {
			t.Fatalf("tools/call returned no content: %s", rec.Body.String())
		}
		return out.Result.IsError, out.Result.Content[0].Text
	}

	if isErr, text := upload(distinctPNG(t, 55)); isErr {
		t.Fatalf("a connector upload under the quota failed: %s", text)
	}
	stored := mediaDirNames(t, dir)
	if len(stored) != 1 {
		t.Fatalf("connector upload left %v on disk, want exactly one file", stored)
	}

	// Same move as the direct-path test: take the ceiling below current usage and
	// send DIFFERENT bytes.
	config.Cfg.MediaQuotaBytes = 1

	isErr, text := upload(distinctPNG(t, 66))
	if !isErr {
		t.Fatalf("the connector stored a file over the quota. media:write is the narrowest key "+
			"the panel can issue and it must not be able to fill the disk: %s", text)
	}
	if !strings.Contains(strings.ToLower(text), "full") {
		t.Errorf("the connector was refused with %q, which does not say the library is full — a "+
			"client reading that will retry, or re-encode a file that was never the problem", text)
	}
	if now := mediaDirNames(t, dir); strings.Join(now, ",") != strings.Join(stored, ",") {
		t.Errorf("the media directory changed during a refused connector upload: %v → %v", stored, now)
	}
}

// The burst. Counting the directory on every upload would be O(files), so the
// total is counted at most once per TTL and carried forward by adding each
// file's size as it is written. Drop that increment and the ceiling holds for
// one upload and then stops: every upload inside the same TTL window reads the
// same stale total, and an attacker who can upload faster than the window can
// walk straight past the quota — which is precisely the pattern of the attack it
// exists to stop.
//
// Mutation-found. The first version of this file never uploaded twice inside a
// window, so removing the increment killed nothing.
func TestConsecutiveUploadsAccumulateWithinOneWindow(t *testing.T) {
	first, second, third := distinctPNG(t, 71), distinctPNG(t, 72), distinctPNG(t, 73)

	// Measure what these three actually cost once stored — the store re-encodes
	// rasters, so the uploaded length is not the stored length and a quota
	// derived from the wrong one would prove nothing.
	mediaQuotaDir(t, 1<<20)
	sizes := make([]int, 0, 3)
	for _, raw := range [][]byte{first, second, third} {
		res, err := storeValidatedMedia(raw, true)
		if err != nil {
			t.Fatalf("measuring fixture: %v", err)
		}
		sizes = append(sizes, res.Size)
	}

	// A ceiling with room for exactly the first two.
	dir := mediaQuotaDir(t, int64(sizes[0]+sizes[1]))

	if _, err := storeValidatedMedia(first, true); err != nil {
		t.Fatalf("first upload refused: %v", err)
	}
	if _, err := storeValidatedMedia(second, true); err != nil {
		t.Fatalf("second upload refused with room for it: %v", err)
	}
	if _, err := storeValidatedMedia(third, true); !errors.Is(err, errMediaQuotaExceeded) {
		t.Fatalf("the third upload in one window gave %v, want errMediaQuotaExceeded.\n"+
			"Files on disk: %v. Uploads inside a single cache window are not counting against "+
			"each other, so the quota is only a limit on the FIRST upload after a recount.",
			err, mediaDirNames(t, dir))
	}
}

// Deleting from the library has to free space NOW. The usage total is cached,
// so without an explicit invalidation the operator who deletes to make room is
// refused the very next upload for up to a TTL — which reads as "the delete did
// not work", and the next thing they delete is something they wanted.
func TestDeletingMediaFreesTheQuotaImmediately(t *testing.T) {
	pngA, pngB := distinctPNG(t, 91), distinctPNG(t, 92)

	mediaQuotaDir(t, 1<<20)
	sizeA, sizeB := 0, 0
	for i, raw := range [][]byte{pngA, pngB} {
		res, err := storeValidatedMedia(raw, true)
		if err != nil {
			t.Fatalf("measuring fixture: %v", err)
		}
		if i == 0 {
			sizeA = res.Size
		} else {
			sizeB = res.Size
		}
	}

	// Room for either file but not both.
	dir := mediaQuotaDir(t, int64(sizeA+sizeB-1))
	stored, err := storeValidatedMedia(pngA, true)
	if err != nil {
		t.Fatalf("first upload refused: %v", err)
	}
	if _, err := storeValidatedMedia(pngB, true); !errors.Is(err, errMediaQuotaExceeded) {
		t.Fatalf("setup expected a full library, got %v", err)
	}

	body := `{"names":["` + stored.Name + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/os/media/delete", strings.NewReader(body))
	rec := httptest.NewRecorder()
	(&App{}).handleOSMediaDelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete returned %d: %s", rec.Code, rec.Body.String())
	}
	if names := mediaDirNames(t, dir); len(names) != 0 {
		t.Fatalf("delete left %v on disk", names)
	}

	if _, err := storeValidatedMedia(pngB, true); err != nil {
		t.Errorf("an upload straight after deleting to make room was refused: %v.\n"+
			"The space is free on disk; only the cached total still thinks otherwise, and an "+
			"operator cannot tell those apart — they see a delete that did nothing.", err)
	}
}

// A cached total belongs to the directory it was counted in. Repointing
// MediaDir and keeping the old number would apply one library's usage to
// another — refusing uploads into an empty directory because a different one was
// full. Tests in this package repoint MediaDir constantly, and an operator
// moving the library is the same shape.
func TestUsageTotalDoesNotSurviveADirectoryChange(t *testing.T) {
	mediaQuotaDir(t, 1<<20)
	full, err := storeValidatedMedia(distinctPNG(t, 81), true)
	if err != nil {
		t.Fatalf("seeding the first directory: %v", err)
	}

	// Point at an empty directory WITHOUT clearing the cache, and leave room for
	// exactly one file. If the previous directory's total is still in play, this
	// upload is refused.
	empty := t.TempDir()
	config.Cfg.MediaDir = empty
	config.Cfg.MediaQuotaBytes = int64(full.Size) + 16

	if _, err := storeValidatedMedia(distinctPNG(t, 82), true); err != nil {
		t.Fatalf("an upload into an EMPTY media directory was refused: %v.\n"+
			"The usage total was counted for a different directory and never re-counted for this "+
			"one, so the quota is being decided on somebody else's files.", err)
	}
}

// distinctSVG returns a small SVG the sanitiser passes intact, with geometry
// that depends on seed so every one of them stores under its own content hash.
// This is the shape a media:write key would send: valid, harmless, and different
// every time, so content addressing never collapses two of them.
func distinctSVG(seed int) []byte {
	return []byte(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="16">`+
			`<rect width="%d" height="16" fill="#0a0a0a"/></svg>`, 16+seed, 8+seed))
}

// The way out of a full library has to exist, and it has to be the panel.
//
// The upload path accepts SVG from an operator, sanitised on the way in — and a
// media:write connector key reaches that same path. That is the narrowest
// credential this panel can issue and the one the ceiling exists to contain.
// Those files land under MediaDir and are charged to MEDIA_QUOTA_GB like
// anything else. The Media page, though, lists only the formats /media will
// serve, and the delete endpoint refuses every name it will not serve.
//
// So the ceiling can be reached entirely with files the panel will not show and
// cannot remove. Every upload after that is refused with a 507 telling the
// operator to "delete files you no longer need from Media" while the page in
// front of them reads zero items, and the only remedy left is a shell on the
// box. Before the quota those uploads merely wasted disk; with it they lock the
// door and keep the key — a strictly worse outcome than the one being fixed,
// because a wasted gigabyte still leaves the library working.
//
// The invariant, and it is the whole finding: the bytes the ceiling charges are
// bytes the Media page can delete. The test walks the operator's real recovery —
// open Media, delete what is listed, upload again — and that has to work.
func TestAFullLibraryIsAlwaysRecoverableFromThePanel(t *testing.T) {
	dir := mediaQuotaDir(t, 1<<20)

	var consumed int64
	for i := 0; i < 8; i++ {
		res, err := storeValidatedMedia(distinctSVG(i), true)
		if err != nil {
			t.Fatalf("SVG %d was refused: %v", i, err)
		}
		consumed += int64(res.Size)
	}

	// The page header counts the same set the grid renders and the ceiling
	// charges. A header reading zero over a full library is how an operator
	// concludes the refusal is a bug in the uploader rather than a full disk.
	if got, want := countMediaItems(), len(mediaDirNames(t, dir)); got != want {
		t.Errorf("the Media page header counts %d items over a library of %d files — the number "+
			"the operator is shown has to be the number the quota is counting", got, want)
	}

	// Drop the ceiling to exactly what those uploads consumed. A script holding a
	// media:write key arrives at this state on its own; this only gets there in
	// eight files instead of several thousand.
	config.Cfg.MediaQuotaBytes = consumed
	invalidateMediaUsage()

	png := distinctPNG(t, 101)
	if _, err := storeValidatedMedia(png, true); !errors.Is(err, errMediaQuotaExceeded) {
		t.Fatalf("setup expected a full library, got %v", err)
	}

	// Now do precisely what the refusal instructs: open Media, delete what it
	// shows.
	listed := listMediaItems()
	names := make([]string, 0, len(listed))
	for _, it := range listed {
		names = append(names, it.Name)
	}
	body, err := json.Marshal(map[string][]string{"names": names})
	if err != nil {
		t.Fatalf("marshal delete body: %v", err)
	}
	rec := httptest.NewRecorder()
	(&App{}).handleOSMediaDelete(rec, httptest.NewRequest(http.MethodPost, "/os/media/delete", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete returned %d: %s", rec.Code, rec.Body.String())
	}

	if left := mediaDirNames(t, dir); len(left) != 0 {
		t.Errorf("deleting everything the Media page lists left %d file(s) behind: %v.\n"+
			"Those bytes are still charged against the ceiling and nothing in the product removes "+
			"them, so the library is full and the panel offers no way down.", len(left), left)
	}

	if _, err := storeValidatedMedia(png, true); err != nil {
		t.Fatalf("an upload was still refused after clearing the library from the panel: %v.\n"+
			"A media:write key can therefore leave this install's media library in a state only a "+
			"shell on the box can undo.", err)
	}
}

// The same invariant from the other side, because a ceiling that charges for
// what it cannot free is the defect whichever direction it is approached from.
//
// MEDIA_QUOTA_GB names the media library, so it has to measure the media
// library. Summing the whole directory tree instead means a stray file under
// MediaDir — an operator's notes, an archive dropped there by hand, anything a
// later feature writes into a subdirectory — silently takes the library's
// headroom, with nothing on the page to explain it and no control that reclaims
// it. The operator sees uploads refused, opens Media, and every file there is
// one they want to keep.
func TestBytesThePanelCannotDeleteDoNotConsumeTheCeiling(t *testing.T) {
	png := distinctPNG(t, 111)

	mediaQuotaDir(t, 1<<20)
	measured, err := storeValidatedMedia(png, true)
	if err != nil {
		t.Fatalf("measuring fixture: %v", err)
	}

	// Room for exactly that one file, and then junk the library has no control
	// over — one beside the media, one below it.
	dir := mediaQuotaDir(t, int64(measured.Size))
	if err := os.WriteFile(filepath.Join(dir, "operator-notes.txt"), make([]byte, 8192), 0o644); err != nil {
		t.Fatalf("seed stray file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "thumbs"), 0o755); err != nil {
		t.Fatalf("seed subdirectory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "thumbs", "cache.bin"), make([]byte, 8192), 0o644); err != nil {
		t.Fatalf("seed nested file: %v", err)
	}
	invalidateMediaUsage()

	if _, err := storeValidatedMedia(png, true); err != nil {
		t.Fatalf("an upload with room for it was refused over bytes the Media page neither lists "+
			"nor deletes: %v.\nThe ceiling is measuring the directory rather than the library it is "+
			"named after, and the 507 then asks the operator to delete files that are not there.", err)
	}
}

// A quota that switches itself off when nobody configured it is not a quota.
// Every subcommand that skips config.Load(), and every test in this package,
// runs with a zeroed Cfg — so zero has to mean "use the default", never
// "unlimited". This is the one line between the control existing and the control
// being absent on exactly the paths nobody looks at.
func TestUnsetMediaQuotaFallsBackToTheDefault(t *testing.T) {
	old := config.Cfg.MediaQuotaBytes
	t.Cleanup(func() { config.Cfg.MediaQuotaBytes = old })

	wantDefault := int64(config.DefaultMediaQuotaGB) * 1024 * 1024 * 1024
	for _, unset := range []int64{0, -1} {
		config.Cfg.MediaQuotaBytes = unset
		if got := mediaQuotaBytes(); got != wantDefault {
			t.Errorf("MediaQuotaBytes=%d gave a ceiling of %d, want the %d default", unset, got, wantDefault)
		}
	}

	config.Cfg.MediaQuotaBytes = 7 << 30
	if got := mediaQuotaBytes(); got != 7<<30 {
		t.Errorf("a configured quota of %d was reported as %d", int64(7)<<30, got)
	}
}
