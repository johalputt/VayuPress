package vayutor

// dist.go — VayuPress-managed current Tor. On a host whose *system* tor is too
// old to validate today's network consensus (Debian 10 "buster" ships 0.4.2.7,
// which the live directory authorities reject with "Consensus not signed by
// sufficient number of requested authorities"), VayuTor downloads the Tor
// Project's official static "expert bundle" into its OWN writable state dir and
// runs THAT binary — as the unprivileged service user, no root, no apt, nothing
// done on the server. If the download fails, or the modern build won't run on an
// ancient host libc, we fall back to the system tor, so this can only ever
// improve on the prior behaviour, never regress it.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// torMinVersion is the oldest tor that can still validate the live consensus.
// 0.4.7.x is the current long-term-support series; 0.4.2 (buster) and older are
// rejected by the directory authorities. The gate mainly weeds out an ancient
// system tor — a freshly downloaded expert bundle is always well past this.
var torMinVersion = torVersion{0, 4, 7, 0}

const (
	// distIndexURL lists every published Tor Browser / expert-bundle version.
	distIndexURL = "https://dist.torproject.org/torbrowser/"
	// distMirrorBase is the long-term archive mirror, tried when the primary
	// distribution host doesn't have a given file.
	distMirrorBase = "https://archive.torproject.org/tor-package-archive/torbrowser/"
	// distIndexTimeout bounds fetching the (tiny) version index.
	distIndexTimeout = 25 * time.Second
	// distFetchTimeout bounds a single expert-bundle download+extract so a
	// slow/blocked mirror can't wedge a reconcile; the caller falls back to the
	// system tor.
	distFetchTimeout = 120 * time.Second
	// distCooldown is how long to wait after a failed download before trying
	// again automatically, so a persistent failure (e.g. host libc too old)
	// doesn't re-download every reconcile. An operator toggle/kick resets it.
	distCooldown = 15 * time.Minute
	// distMaxCandidates caps how many recent versions we try, newest first.
	distMaxCandidates = 6
	// distMaxTarBytes bounds decompressed extraction (defence against a hostile
	// or corrupt archive). A tor binary + its bundled libs is well under this.
	distMaxTarBytes = 200 << 20
	// distMaxDownloadBytes bounds the COMPRESSED tarball buffered in memory for
	// hashing before extraction (audit L3). The real expert bundle is ~20 MB.
	distMaxDownloadBytes = 100 << 20
)

// torVersion is a comparable tor version tuple (a.b.c.d).
type torVersion struct{ a, b, c, d int }

// atLeast reports whether v >= o.
func (v torVersion) atLeast(o torVersion) bool {
	va := [...]int{v.a, v.b, v.c, v.d}
	oa := [...]int{o.a, o.b, o.c, o.d}
	for i := range va {
		if va[i] != oa[i] {
			return va[i] > oa[i]
		}
	}
	return true
}

func (v torVersion) String() string { return fmt.Sprintf("%d.%d.%d.%d", v.a, v.b, v.c, v.d) }

var torVerRe = regexp.MustCompile(`\d+\.\d+(?:\.\d+){0,2}`)

// parseTorVersion pulls the first x.y[.z[.w]] out of a string (e.g. tor's
// "Tor version 0.4.8.13." banner, or a "14.0.1/" or "14.5/" directory entry).
func parseTorVersion(s string) (torVersion, bool) {
	m := torVerRe.FindString(s)
	if m == "" {
		return torVersion{}, false
	}
	parts := strings.Split(m, ".")
	var v torVersion
	fields := [...]*int{&v.a, &v.b, &v.c, &v.d}
	for i := 0; i < len(parts) && i < len(fields); i++ {
		n, _ := strconv.Atoi(parts[i])
		*fields[i] = n
	}
	return v, true
}

// torBinVersion runs `<bin> --version` (with libDir on LD_LIBRARY_PATH for a
// bundled build) and parses tor's reported version. ok is false if the binary
// won't execute — which is exactly how an ancient host libc rejecting a modern
// build surfaces, letting the caller fall back cleanly.
func torBinVersion(bin, libDir string) (torVersion, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version") //nolint:gosec // resolved tor path we control
	if libDir != "" {
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+libDir)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return torVersion{}, false
	}
	return parseTorVersion(string(out))
}

func (m *managedTor) distDir() string     { return filepath.Join(m.dir, "dist") }
func (m *managedTor) distBinPath() string { return filepath.Join(m.distDir(), "tor") }

// cachedDistBinary returns a validated VayuPress-managed tor if one is already
// available — either in memory (this run) or on disk from a previous run — else
// "". Re-validating the on-disk copy via `--version` guards against a truncated
// download or a host that has since been downgraded.
func (m *managedTor) cachedDistBinary() string {
	m.distMu.Lock()
	if m.distBin != "" {
		b := m.distBin
		m.distMu.Unlock()
		return b
	}
	m.distMu.Unlock()

	bin := m.distBinPath()
	if fi, err := os.Stat(bin); err != nil || fi.IsDir() {
		return ""
	}
	if v, ok := torBinVersion(bin, m.distDir()); ok && v.atLeast(torMinVersion) {
		m.distMu.Lock()
		m.distBin, m.distLibDir, m.distErr = bin, m.distDir(), ""
		m.distMu.Unlock()
		return bin
	}
	return ""
}

// ensureDist downloads, extracts, and validates a current tor into the managed
// state dir, honouring a cooldown so a persistent failure isn't retried on every
// reconcile. Returns the binary path on success.
func (m *managedTor) ensureDist() (string, error) {
	if b := m.cachedDistBinary(); b != "" {
		return b, nil
	}
	m.distMu.Lock()
	if !m.distNextTry.IsZero() && time.Now().Before(m.distNextTry) {
		msg := m.distErr
		m.distMu.Unlock()
		if msg == "" {
			msg = "managed tor download is on cooldown"
		}
		return "", errors.New(msg)
	}
	m.distMu.Unlock()

	bin, libDir, err := m.downloadDist()

	m.distMu.Lock()
	defer m.distMu.Unlock()
	if err != nil {
		m.distErr = err.Error()
		m.distNextTry = time.Now().Add(distCooldown)
		return "", err
	}
	m.distBin, m.distLibDir, m.distErr = bin, libDir, ""
	m.distNextTry = time.Time{}
	return bin, nil
}

// downloadDist fetches a recent expert bundle, extracts the tor binary + its
// bundled libraries into distDir, and verifies the result runs and is new
// enough. A modern binary that won't start on an old host libc is reported with
// actionable guidance so the admin page can point at the apt fallback.
func (m *managedTor) downloadDist() (bin, libDir string, err error) {
	versions, err := m.distVersions()
	if err != nil {
		return "", "", fmt.Errorf("could not reach the Tor download server (%v) — check outbound HTTPS is allowed", err)
	}
	dir := m.distDir()
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}

	var lastErr error
	extracted := false
	for _, ver := range versions {
		for _, u := range expertBundleURLs(ver) {
			if e := fetchAndExtractTor(u, dir); e != nil {
				lastErr = e
				continue
			}
			extracted = true
			break
		}
		if extracted {
			break
		}
	}
	if !extracted {
		if lastErr == nil {
			lastErr = errors.New("no expert bundle download succeeded")
		}
		return "", "", lastErr
	}

	bin = m.distBinPath()
	if fi, e := os.Stat(bin); e != nil || fi.IsDir() {
		return "", "", errors.New("the downloaded Tor expert bundle contained no tor binary")
	}
	_ = os.Chmod(bin, 0o700)
	v, ok := torBinVersion(bin, dir)
	if !ok {
		return "", "", errors.New("downloaded Tor did not run on this host — its system libraries are likely too old for a current Tor build; use the one-time apt fallback")
	}
	if !v.atLeast(torMinVersion) {
		return "", "", fmt.Errorf("downloaded Tor %s is older than the required %s", v, torMinVersion)
	}
	return bin, dir, nil
}

// distVersions returns recent stable Tor Browser / expert-bundle versions,
// newest first, parsed from the distribution directory index.
func (m *managedTor) distVersions() ([]string, error) {
	body, err := httpGetLimited(distIndexURL, distIndexTimeout, 4<<20)
	if err != nil {
		return nil, err
	}
	out := parseDistIndex(string(body), distMaxCandidates)
	if len(out) == 0 {
		return nil, errors.New("no versions found in the Tor download index")
	}
	return out, nil
}

var distIndexRe = regexp.MustCompile(`href="(\d+\.\d+(?:\.\d+)?)/"`)

// parseDistIndex extracts stable version directory names from a Tor distribution
// index page, sorted newest-first and capped at max. Alpha/beta dirs (e.g.
// "14.5a1/") don't match the stable-only pattern and are skipped. Pure (no I/O)
// so it is unit-testable.
func parseDistIndex(body string, max int) []string {
	seen := map[string]bool{}
	var vers []torVersion
	for _, m := range distIndexRe.FindAllStringSubmatch(body, -1) {
		s := m[1]
		if seen[s] {
			continue
		}
		seen[s] = true
		if v, ok := parseTorVersion(s); ok {
			vers = append(vers, v)
		}
	}
	sort.Slice(vers, func(i, j int) bool { return vers[i] != vers[j] && vers[i].atLeast(vers[j]) })
	out := make([]string, 0, max)
	for _, v := range vers {
		// Directory entries are Tor Browser versions (x.y or x.y.z); render them
		// back without a trailing .0 so the URL matches the published filename.
		s := fmt.Sprintf("%d.%d.%d", v.a, v.b, v.c)
		if v.c == 0 {
			s = fmt.Sprintf("%d.%d", v.a, v.b)
		}
		out = append(out, s)
		if len(out) >= max {
			break
		}
	}
	return out
}

// expertBundleURLs lists the candidate download URLs for a version, covering
// both the current and older filename conventions and both mirrors.
func expertBundleURLs(ver string) []string {
	names := []string{
		"tor-expert-bundle-linux-x86_64-" + ver + ".tar.gz", // current (13.0+)
		"tor-expert-bundle-" + ver + "-linux-x86_64.tar.gz", // older (12.x)
	}
	var urls []string
	for _, base := range []string{distIndexURL + ver + "/", distMirrorBase + ver + "/"} {
		for _, n := range names {
			urls = append(urls, base+n)
		}
	}
	return urls
}

// fetchAndExtractTor downloads one expert-bundle tarball, verifies its integrity
// (audit L3), then extracts just the tor binary and its bundled shared libraries
// into dir (flat, by base name — so the binary finds its libs via
// LD_LIBRARY_PATH=dir). Returns nil only if a tor binary was extracted.
//
// The tarball is buffered and its SHA-256 checked BEFORE anything is written or
// made executable: an operator can pin the expected digest via
// VAYUTOR_BUNDLE_SHA256 (obtained from the Tor Project's signed sha256sums over
// a trusted channel), and a mismatch aborts. Without a pin the digest is logged
// so it can be pinned, and VAYUTOR_REQUIRE_VERIFIED_BUNDLE=1 refuses any
// unpinned bundle outright. A bundle with no tor binary (lone .so files) is
// rejected without extracting anything.
func fetchAndExtractTor(url, dir string) error {
	raw, err := downloadBundle(url)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	if err := verifyBundleIntegrity(url, hex.EncodeToString(sum[:])); err != nil {
		return err
	}
	// Refuse to extract a bundle of libraries with no tor binary (a lone-.so
	// payload) — the whole point is to run the verified tor, not arbitrary
	// shared objects (audit L3).
	if !tarContainsTor(raw) {
		return errors.New("archive contained no tor binary — refusing to extract shared libraries alone")
	}
	return extractTorFromTar(raw, dir)
}

// downloadBundle fetches url into a bounded in-memory buffer.
func downloadBundle(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), distFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "VayuTor")
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // fixed torproject.org host, HTTPS
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, distMaxDownloadBytes))
}

// verifyBundleIntegrity enforces an operator-pinned SHA-256 when one is set, and
// otherwise logs the observed digest (and, in strict mode, refuses) — audit L3.
func verifyBundleIntegrity(url, sumHex string) error {
	expected := strings.ToLower(strings.TrimSpace(os.Getenv("VAYUTOR_BUNDLE_SHA256")))
	if expected != "" {
		if subtle.ConstantTimeCompare([]byte(expected), []byte(sumHex)) != 1 {
			return fmt.Errorf("tor bundle sha256 mismatch (got %s, pinned %s): refusing to run an unverified binary", sumHex, expected)
		}
		logging.LogInfo("vayutor", "tor expert bundle sha256 verified against VAYUTOR_BUNDLE_SHA256")
		return nil
	}
	logging.LogWarn("vayutor", fmt.Sprintf(
		"tor expert bundle fetched WITHOUT integrity pinning (sha256=%s from %s): set VAYUTOR_BUNDLE_SHA256 to this value — cross-checked against the Tor Project's signed sha256sums — to enforce it (audit L3)",
		sumHex, url))
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("VAYUTOR_REQUIRE_VERIFIED_BUNDLE"))); v == "1" || v == "true" || v == "yes" {
		return errors.New("tor bundle integrity is not pinned and VAYUTOR_REQUIRE_VERIFIED_BUNDLE is set: refusing to download and execute an unverified binary")
	}
	return nil
}

// tarContainsTor reports whether the gzip'd tar in raw carries a regular file
// named "tor".
func tarContainsTor(raw []byte) bool {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			return false
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "tor" {
			return true
		}
	}
}

// extractTorFromTar extracts the tor binary and its shared libraries from the
// verified, buffered tarball into dir.
func extractTorFromTar(raw []byte, dir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	gotTor := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(h.Name)
		isTor := base == "tor"
		isLib := strings.Contains(base, ".so")
		if !isTor && !isLib {
			continue
		}
		// filepath.Base already strips any directory, but guard explicitly against
		// archive path traversal (Zip Slip): the destination must stay within dir.
		dst := filepath.Join(dir, base)
		if dst != filepath.Join(dir, filepath.Base(dst)) ||
			!strings.HasPrefix(dst, filepath.Clean(dir)+string(os.PathSeparator)) {
			continue
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, io.LimitReader(tr, distMaxTarBytes)); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if isTor {
			gotTor = true
		}
	}
	if !gotTor {
		return errors.New("archive contained no tor binary")
	}
	return nil
}

// httpGetLimited GETs a URL and returns up to max bytes of its body.
func httpGetLimited(url string, timeout time.Duration, max int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "VayuTor")
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // fixed torproject.org host, HTTPS
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

// libDirFor returns the LD_LIBRARY_PATH dir to spawn bin with, when bin is our
// downloaded build (it bundles its own libssl/libevent/…); "" for a system tor.
func (m *managedTor) libDirFor(bin string) string {
	m.distMu.Lock()
	defer m.distMu.Unlock()
	if bin != "" && bin == m.distBin && m.distLibDir != "" {
		return m.distLibDir
	}
	return ""
}

// usingDistBinary reports whether VayuTor is running its own downloaded tor
// rather than a system one (for the admin status line).
func (m *managedTor) usingDistBinary() bool {
	m.distMu.Lock()
	defer m.distMu.Unlock()
	return m.distBin != ""
}

// distDownloadErr returns the last managed-download failure reason, if any.
func (m *managedTor) distDownloadErr() string {
	m.distMu.Lock()
	defer m.distMu.Unlock()
	return m.distErr
}

// setDistEnabled turns managed tor downloading on/off. Off by default so unit
// tests that construct a managedTor directly keep the legacy PATH-only behaviour.
func (m *managedTor) setDistEnabled(v bool) {
	m.distMu.Lock()
	m.distEnabled = v
	m.distMu.Unlock()
}

func (m *managedTor) distIsEnabled() bool {
	m.distMu.Lock()
	defer m.distMu.Unlock()
	return m.distEnabled
}

// resetDistCooldown clears the post-failure cooldown so the next resolve retries
// the download immediately (used when the operator toggles VayuTor or kicks it).
func (m *managedTor) resetDistCooldown() {
	m.distMu.Lock()
	m.distNextTry = time.Time{}
	m.distMu.Unlock()
}
