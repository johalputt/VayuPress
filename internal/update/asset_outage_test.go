// SPDX-License-Identifier: Apache-2.0

package update

// asset_outage_test.go — the updater installed a zip file over the live binary.
//
// WHAT HAPPENED, exactly, on 2026-08-04.
//
// Release v3.16.86 attached a packaged copy of the marketing website as
// `selfhosted-site.zip`, with its `.sha256` beside it. An operator clicked
// Update & Backup. The updater:
//
//  1. listed the release's assets — the GitHub API returns them sorted
//     ALPHABETICALLY BY NAME, so `selfhosted-site.zip` came first;
//  2. discarded the sidecars it recognised (`.sha256`, `.sig`, `.bundle`,
//     `.json`) — `.zip` was not among them, so the zip stayed a candidate;
//  3. looked for a candidate naming this platform. VayuPress ships a bare
//     `vayupress`, so NOTHING ever matches on platform, and the function ended in
//     `return cands[0]` — the alphabetically first name;
//  4. downloaded `selfhosted-site.zip`, found `selfhosted-site.zip.sha256` on the
//     release, and verified it. The checksum PASSED, because the file really was
//     the file the release published;
//  5. wrote 500 KB of zip over /var/lib/vayupress/bin/vayupress, chmod 0755, and
//     logged a successful update.
//
// systemd could not exec it. Nothing bound :8080. Every request to the site got
// 502 from nginx until the operator copied the `.bak` back by hand — and because
// the fault was in selection rather than transport, every retry did it again.
//
// Two independent failures, so two independent fixes, and a test for each:
//
//   - SELECTION took the first name in an alphabetical list as evidence. It was
//     never evidence; the previous releases worked only because `vayupress`
//     happened to sort ahead of `vayuprovision-…` and `vayushield-…`.
//   - VERIFICATION proved the bytes were authentic and never asked whether they
//     were a program. A checksum cannot tell you that, and nothing else was
//     looking.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

// releaseV31686Assets is the asset list of the release that caused the outage,
// in the exact order and spelling the GitHub API returned it.
func releaseV31686Assets() []Asset {
	names := []string{
		"selfhosted-site.zip",
		"selfhosted-site.zip.sha256",
		"vayupress",
		"vayupress-v3.16.86.sbom.cosign.bundle",
		"vayupress-v3.16.86.sbom.json",
		"vayupress.cosign.bundle",
		"vayupress.sha256",
		"vayuprovision-helpers.tar.gz",
		"vayuprovision-helpers.tar.gz.cosign.bundle",
		"vayuprovision-helpers.tar.gz.sha256",
		"vayushield-agent.tar.gz",
		"vayushield-agent.tar.gz.cosign.bundle",
		"vayushield-agent.tar.gz.sha256",
	}
	as := make([]Asset, 0, len(names))
	for _, n := range names {
		as = append(as, Asset{Name: n, DownloadURL: "https://example.invalid/" + n})
	}
	return as
}

// The exact release, replayed. If this ever picks anything but the binary, a
// production install goes down again.
func TestTheReleaseThatBrokeProductionNowSelectsTheBinary(t *testing.T) {
	got := selectBinaryAsset(releaseV31686Assets(), "linux", "amd64", "vayupress")
	if got == nil {
		t.Fatal("no asset selected from a release that plainly contains `vayupress`")
	}
	if got.Name != "vayupress" {
		t.Fatalf("selected %q — this is the outage. A %s was installed over the service binary "+
			"and the site returned 502 until the backup was restored by hand", got.Name, got.Name)
	}
}

// The attack, generalised: attach ANY file whose name sorts before the binary's
// and the old selector installs it. Nobody has to be malicious for this — the
// zip was attached by the release workflow itself.
func TestAnAssetThatSortsFirstCannotHijackTheInstall(t *testing.T) {
	for _, intruder := range []string{
		"aardvark.zip",           // sorts first by luck
		"CHANGELOG.html",         // uppercase sorts before lowercase in ASCII
		"0-release-notes.pdf",    // digits sort before letters
		"assets.tar.gz",          // an archive that is not a sidecar
		"selfhosted-site.zip",    // the real one
		"docs-bundle.zip",        // the next one somebody attaches
		"Makefile",               // no extension at all
		"_manifest.yml",          // underscore sorts before lowercase letters
		"COPYING",                // a licence file
		"vayupress-site.tar.zst", // sorts after `vayupress` but is still not a binary
	} {
		assets := append(releaseV31686Assets(), Asset{Name: intruder})
		sort.Slice(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })

		got := selectBinaryAsset(assets, "linux", "amd64", "vayupress")
		if got == nil {
			t.Errorf("%q: selection refused entirely, though `vayupress` is present", intruder)
			continue
		}
		if got.Name != "vayupress" {
			t.Errorf("%q hijacked the install — selected %q instead of the binary", intruder, got.Name)
		}
	}
}

// `Makefile` and `COPYING` above have no recognisable extension, so the
// suffix lists cannot help. Only the exact-name rule saves those cases — pin it
// so nobody "simplifies" it away later.
func TestTheExactNameRuleIsWhatMakesUnextensionedIntrudersSafe(t *testing.T) {
	assets := []Asset{
		{Name: "COPYING"},
		{Name: "Makefile"},
		{Name: "vayupress"},
		{Name: "vayupress.sha256"},
	}
	if got := selectBinaryAsset(assets, "linux", "amd64", "vayupress"); got == nil || got.Name != "vayupress" {
		t.Fatalf("want the binary by name, got %+v", got)
	}
	// And with no name to match on, several unhinted candidates must be refused
	// rather than guessed between — guessing is the whole bug.
	if got := selectBinaryAsset(assets, "linux", "amd64", ""); got != nil {
		t.Fatalf("with nothing to match on, selection must refuse, not pick %q", got.Name)
	}
}

// With no name to match on and no platform hint, discarding archives is the ONLY
// thing that leaves one unambiguous candidate. Without it the release below is
// two candidates and the update cannot proceed at all.
func TestDiscardingArchivesIsWhatMakesAnUnhintedReleaseUnambiguous(t *testing.T) {
	assets := []Asset{
		{Name: "site.zip"},
		{Name: "site.zip.sha256"},
		{Name: "vayupress"},
		{Name: "vayupress.sha256"},
	}
	got := selectBinaryAsset(assets, "linux", "amd64", "")
	if got == nil {
		t.Fatal("the zip was still treated as a possible binary, so the release reads as ambiguous " +
			"and no update can be installed")
	}
	if got.Name != "vayupress" {
		t.Fatalf("selected %q rather than the binary", got.Name)
	}
}

// The updater writes the downloaded bytes straight over the binary. It has no
// extraction step, so an archive can NEVER be the right answer — installing one
// is guaranteed to produce a file the loader rejects.
func TestArchivesAreNeverCandidatesBecauseNothingUnpacksThem(t *testing.T) {
	for _, n := range []string{
		"x.zip", "x.tar", "x.tar.gz", "x.tgz", "x.tar.xz", "x.tar.bz2", "x.tar.zst",
		"x.gz", "x.xz", "x.bz2", "x.zst", "x.7z", "x.deb", "x.rpm", "x.dmg", "x.msi",
		"x.html", "x.yml", "x.pdf",
	} {
		if !isArchiveAsset(n) {
			t.Errorf("%q is a container/document and must not be installable as a binary", n)
		}
	}
	for _, n := range []string{"vayupress", "vayupress_linux_amd64", "tool.exe", "vayupress-v3"} {
		if isArchiveAsset(n) {
			t.Errorf("%q is a plausible binary name and must stay a candidate", n)
		}
	}
}

// The backstop. Even if selection is wrong again — a new asset type, a renamed
// binary, a release built by something else — bytes that the kernel cannot exec
// must never reach the binary path. This is the check that was missing; had it
// existed, the outage would have been a failed update with a clear message.
func TestNonExecutableBytesAreRefusedWhateverTheirChecksumSays(t *testing.T) {
	cases := []struct {
		what string
		data []byte
		says string
	}{
		{"a zip archive", []byte("PK\x03\x04\x14\x00\x00\x00\x08\x00"), "ZIP"},
		{"a gzip archive", []byte{0x1f, 0x8b, 0x08, 0x00, 0x00}, "gzip"},
		{"an HTML error page", []byte("<!doctype html><html>502</html>"), "HTML"},
		{"a JSON error body", []byte(`{"message":"Not Found"}`), "JSON"},
		{"a shell script", []byte("#!/bin/sh\necho hi\n"), "shell script"},
		{"a PDF", []byte("%PDF-1.7\n%âãÏÓ"), "PDF"},
		{"empty", []byte(nil), "empty"},
	}
	for _, c := range cases {
		err := verifyExecutableImage(c.data, "linux", "selfhosted-site.zip")
		if err == nil {
			t.Errorf("%s was accepted as a Linux binary — this is exactly how a live site "+
				"was left returning 502", c.what)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: the operator is told %q, which does not name what actually arrived (%s)",
				c.what, err.Error(), c.says)
		}
		if !strings.Contains(err.Error(), "selfhosted-site.zip") {
			t.Errorf("%s: the error does not name the offending asset, so the operator cannot act on it: %v",
				c.what, err)
		}
	}
}

// …and it must not become a gate that blocks legitimate updates. A real ELF has
// to pass, or the fix for the outage is an outage of its own.
func TestARealExecutableStillInstalls(t *testing.T) {
	elf := append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, make([]byte, 64)...)
	if err := verifyExecutableImage(elf, "linux", "vayupress"); err != nil {
		t.Fatalf("a Linux ELF binary was refused — every update would now fail: %v", err)
	}
	macho := append([]byte{0xcf, 0xfa, 0xed, 0xfe}, make([]byte, 64)...)
	if err := verifyExecutableImage(macho, "darwin", "vayupress"); err != nil {
		t.Fatalf("a 64-bit Mach-O binary was refused on darwin: %v", err)
	}
	fat := append([]byte{0xca, 0xfe, 0xba, 0xbe}, make([]byte, 64)...)
	if err := verifyExecutableImage(fat, "darwin", "vayupress"); err != nil {
		t.Fatalf("a universal Mach-O binary was refused on darwin: %v", err)
	}
	pe := append([]byte{'M', 'Z'}, make([]byte, 64)...)
	if err := verifyExecutableImage(pe, "windows", "vayupress.exe"); err != nil {
		t.Fatalf("a PE binary was refused on windows: %v", err)
	}
	// An OS whose format is not listed must not be blocked on a guess.
	if err := verifyExecutableImage([]byte("anything at all"), "plan9", "vayupress"); err != nil {
		t.Fatalf("an unlisted GOOS must not be refused on a format we do not model: %v", err)
	}
}

// The write path itself must be closed, not just the path that happens to call
// the check first. Anything that reaches atomicReplace has to pass too.
func TestTheWritePathRefusesANonExecutableEvenIfCalledDirectly(t *testing.T) {
	target := t.TempDir() + "/vayupress"
	if err := atomicReplace(target, []byte("PK\x03\x04 not a program")); err == nil {
		t.Fatal("atomicReplace wrote a zip over the binary path — the backstop is bypassable")
	}
}

// End to end, through the real apply path: a release that publishes an asset
// with the RIGHT NAME and the WRONG CONTENT. Selection has nothing to catch here
// — the name is exactly what it should be — and the checksum verifies, because
// the release really did publish those bytes. Only the format check stands
// between this and a service that will not start.
func TestApplyRefusesAReleaseWhoseBinaryAssetIsNotABinary(t *testing.T) {
	payload := append([]byte("PK\x03\x04"), make([]byte, 4096)...) // a zip, correctly named
	sum := sha256.Sum256(payload)
	sumHex := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v9.9.9",
			"assets": []map[string]any{
				{"name": "vayupress", "browser_download_url": base + "/bin"},
				{"name": "vayupress.sha256", "browser_download_url": base + "/sum"},
			},
		})
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) })
	mux.HandleFunc("/sum", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sumHex + "  vayupress\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second, Transport: rewriteTransport{target: srv.URL}}
	opt := ApplyOptions{Current: "v1.0.0", DryRun: true, AllowUnsigned: true, BinaryPath: "/opt/vayupress/vayupress"}

	_, err := ApplyVerified(context.Background(), client, "o", "r", opt, nil)
	if err == nil {
		t.Fatal("a verified, correctly-named ZIP archive was accepted for install — " +
			"this is the outage with the selection bug fixed and the backstop missing")
	}
	if !strings.Contains(err.Error(), "not an executable") || !strings.Contains(err.Error(), "ZIP") {
		t.Fatalf("the refusal does not tell the operator what arrived: %v", err)
	}
}

// ── Compatibility with the updaters already installed in the world ────────────
//
// The selector above is fixed, but that fix only protects an install AFTER it
// has been installed — and the update that delivers it is chosen by the OLD
// code, running on the operator's machine right now. So the release layout has
// to remain installable by the buggy version, for as long as any pre-fix install
// exists, which is indefinitely.
//
// selectBinaryAssetPreFix is that buggy version, written out verbatim. It is not
// dead weight: it is the specification of every updater currently in the field,
// and the test below is the only thing that checks a release against it.
func selectBinaryAssetPreFix(assets []Asset, goos, goarch string) *Asset {
	cands := make([]*Asset, 0, len(assets))
	for i := range assets {
		if isMetadataAsset(assets[i].Name) { // NOTE: no archive exclusion
			continue
		}
		cands = append(cands, &assets[i])
	}
	if len(cands) == 0 {
		return nil
	}
	if len(cands) == 1 {
		return cands[0]
	}
	wantArch := archAliases[goarch]
	if len(wantArch) == 0 {
		wantArch = []string{goarch}
	}
	var osOnlyMatch *Asset
	for _, a := range cands {
		n := strings.ToLower(a.Name)
		if goos != "" && !strings.Contains(n, goos) {
			continue
		}
		if osOnlyMatch == nil {
			osOnlyMatch = a
		}
		for _, al := range wantArch {
			if strings.Contains(n, al) {
				return a
			}
		}
	}
	if osOnlyMatch != nil {
		return osOnlyMatch
	}
	return cands[0] // the guess that caused the outage
}

// currentReleaseLayout is what tag-release.yml attaches, sorted the way the
// GitHub API returns it. Keep it in step with the `files:` block.
func currentReleaseLayout() []Asset {
	names := []string{
		"vayupress",
		"vayupress-selfhosted-site.zip",
		"vayupress-selfhosted-site.zip.sha256",
		"vayupress-v3.16.87.sbom.cosign.bundle",
		"vayupress-v3.16.87.sbom.json",
		"vayupress.cosign.bundle",
		"vayupress.sha256",
		"vayuprovision-helpers.tar.gz",
		"vayuprovision-helpers.tar.gz.cosign.bundle",
		"vayuprovision-helpers.tar.gz.sha256",
		"vayushield-agent.tar.gz",
		"vayushield-agent.tar.gz.cosign.bundle",
		"vayushield-agent.tar.gz.sha256",
	}
	sort.Strings(names) // the GitHub API sorts by name; do not assume our order
	as := make([]Asset, 0, len(names))
	for _, n := range names {
		as = append(as, Asset{Name: n})
	}
	return as
}

// The one that matters to an operator whose site is currently down: their
// existing binary must be able to fetch the fixed one.
func TestAnUpdaterFromBeforeTheFixCanStillInstallOurCurrentRelease(t *testing.T) {
	got := selectBinaryAssetPreFix(currentReleaseLayout(), "linux", "amd64")
	if got == nil || got.Name != "vayupress" {
		t.Fatalf("an install running the pre-fix updater would download %+v instead of the binary. "+
			"Every existing install is then stuck: it cannot reach the release that would fix it, "+
			"and clicking Update takes the site down again.", got)
	}
}

// And the fixed updater must agree, or the two disagree about what a release means.
func TestBothUpdatersAgreeOnOurCurrentRelease(t *testing.T) {
	layout := currentReleaseLayout()
	old := selectBinaryAssetPreFix(layout, "linux", "amd64")
	nw := selectBinaryAsset(layout, "linux", "amd64", "vayupress")
	if old == nil || nw == nil || old.Name != nw.Name {
		t.Fatalf("the old updater picks %+v and the new one picks %+v", old, nw)
	}
}

// The rule stated as a rule, so a future attachment is checked against it here
// and not only in CI: nothing an old updater considers installable may sort
// ahead of the binary.
func TestNoAttachmentOutranksTheBinaryForAnOldUpdater(t *testing.T) {
	for _, a := range currentReleaseLayout() {
		if isMetadataAsset(a.Name) {
			continue
		}
		if a.Name < "vayupress" {
			t.Errorf("%q sorts before \"vayupress\" and is not a recognised sidecar, so every "+
				"pre-fix install would download it instead of the binary. Rename it with a "+
				"\"vayupress-\" prefix.", a.Name)
		}
	}
}
