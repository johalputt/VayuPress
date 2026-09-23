// SPDX-License-Identifier: Apache-2.0

package update

// mirror_test.go — the release mirror, attacked from both ends.
//
// From upstream: what would it take to make the mirror HOLD something an install
// should not take? Every rule in verifyMirroredRelease gets its own release that
// breaks exactly that rule and nothing else, because a seed that trips several
// rules kills a mutation of none of them.
//
// From downstream: what would a stranger do with a public mirror? Walk out of
// the release directory, use it as a relay to GitHub, ask for a repository it
// does not hold, or read the operator's error text off the public status.
//
// The signature verifier is stubbed for the synthetic releases here, because no
// fixture can carry a genuine Sigstore signature over made-up bytes. The stub
// checks PAIRING (this bundle names this artifact's digest), so a mirror that
// passed the wrong bundle would still fail. That the mirror reaches the REAL
// verifier is pinned separately, with the stub removed.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeFile is one release attachment in the fake GitHub.
type fakeFile struct {
	name   string
	body   []byte
	digest string // "" = GitHub publishes none; "auto" = the true digest
	url    string // "" = the canonical github.com download URL
	size   int    // 0 = the true size
}

type fakeRelease struct {
	tag        string
	prerelease bool
	draft      bool
	published  time.Time
	files      []fakeFile
}

// fakeGitHub serves a releases list and the files, counting list requests and
// honouring If-None-Match the way GitHub does.
type fakeGitHub struct {
	mu        sync.Mutex
	releases  []fakeRelease
	etag      string
	listCalls int
	downloads int
	srv       *httptest.Server
}

func newFakeGitHub(t *testing.T, rels ...fakeRelease) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{releases: rels, etag: `W/"v1"`}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	return g
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func (g *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r.Host == "api.github.com" {
		if r.URL.Path != "/repos/johalputt/VayuPress/releases" {
			http.NotFound(w, r)
			return
		}
		g.listCalls++
		if inm := r.Header.Get("If-None-Match"); inm != "" && inm == g.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		var list []map[string]any
		for _, rel := range g.releases {
			var assets []map[string]any
			for _, f := range rel.files {
				u := f.url
				if u == "" {
					u = "https://github.com/johalputt/VayuPress/releases/download/" + rel.tag + "/" + f.name
				}
				size := len(f.body)
				if f.size != 0 {
					size = f.size
				}
				a := map[string]any{"name": f.name, "browser_download_url": u, "size": size}
				switch f.digest {
				case "":
				case "auto":
					a["digest"] = "sha256:" + sha(f.body)
				default:
					a["digest"] = f.digest
				}
				assets = append(assets, a)
			}
			list = append(list, map[string]any{
				"tag_name": rel.tag, "draft": rel.draft, "prerelease": rel.prerelease,
				"published_at": rel.published.Format(time.RFC3339), "body": "notes for " + rel.tag,
				"html_url": "https://github.com/johalputt/VayuPress/releases/tag/" + rel.tag,
				"assets":   assets,
			})
		}
		w.Header().Set("ETag", g.etag)
		_ = json.NewEncoder(w).Encode(list)
		return
	}
	// github.com/johalputt/VayuPress/releases/download/<tag>/<name>
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) == 6 {
		for _, rel := range g.releases {
			if rel.tag != parts[4] {
				continue
			}
			for _, f := range rel.files {
				if f.name == parts[5] {
					g.downloads++
					_, _ = w.Write(f.body)
					return
				}
			}
		}
	}
	http.NotFound(w, r)
}

// client routes every host to the fake, keeping the Host so it can tell the
// API from the download site.
func (g *fakeGitHub) client() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: hostKeepingTransport{g.srv}}
}

type hostKeepingTransport struct{ srv *httptest.Server }

func (h hostKeepingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := req.URL.Parse(h.srv.URL)
	r2 := req.Clone(req.Context())
	r2.Host = req.URL.Host
	r2.URL.Scheme, r2.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(r2)
}

// elf is a body that passes the executable-image check.
func elf(s string) []byte { return append([]byte("\x7fELF"), s...) }

// goodRelease is a complete release shaped like the real ones: the binary with
// its checksum, Sigstore bundle and Ed25519 signature, and a helper archive with
// its checksum. Tests break exactly one thing in it.
func goodRelease(tag string, published time.Time) fakeRelease {
	bin := elf("binary " + tag)
	helper := []byte("helper archive " + tag)
	return fakeRelease{tag: tag, published: published, files: []fakeFile{
		{name: "vayupress", body: bin, digest: "auto"},
		{name: "vayupress.sha256", body: []byte(sha(bin) + "  vayupress\n"), digest: "auto"},
		{name: "vayupress.cosign.bundle", body: []byte("bundle-for:" + sha(bin)), digest: "auto"},
		{name: "vayupress.sig", body: []byte("sig"), digest: "auto"},
		{name: "vayuprovision-helpers.tar.gz", body: helper, digest: "auto"},
		{name: "vayuprovision-helpers.tar.gz.sha256", body: []byte(sha(helper) + "  vayuprovision-helpers.tar.gz\n"), digest: "auto"},
	}}
}

// stubPairingVerifier accepts a bundle only for the artifact it names.
func stubPairingVerifier(t *testing.T) {
	t.Helper()
	prev := verifyReleaseSignature
	verifyReleaseSignature = func(artifact, bundle []byte, _ string) error {
		if string(bundle) != "bundle-for:"+sha(artifact) {
			return errors.New("signature does not cover this artifact")
		}
		return nil
	}
	t.Cleanup(func() { verifyReleaseSignature = prev })
}

func newTestMirror(t *testing.T) *Mirror {
	t.Helper()
	return NewMirror(t.TempDir(), "johalputt", "VayuPress")
}

func heldTags(m *Mirror) []string {
	var out []string
	for _, r := range m.State().Releases {
		out = append(out, r.Tag)
	}
	return out
}

var t0 = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func TestMirrorHoldsNewestStablePreviousStableAndAheadPrerelease(t *testing.T) {
	stubPairingVerifier(t)
	pre := goodRelease("v3.18.0-rc1", t0.Add(4*time.Hour))
	pre.prerelease = true
	oldPre := goodRelease("v3.17.10-rc1", t0)
	oldPre.prerelease = true
	draft := goodRelease("v9.9.9", t0.Add(5*time.Hour))
	draft.draft = true
	g := newFakeGitHub(t,
		goodRelease("v3.17.64", t0.Add(time.Hour)),
		goodRelease("v3.17.65", t0.Add(2*time.Hour)),
		goodRelease("v3.17.63", t0), pre, oldPre, draft)
	m := newTestMirror(t)
	if err := m.Sync(context.Background(), g.client()); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(heldTags(m), ",")
	if got != "v3.18.0-rc1,v3.17.65,v3.17.64" {
		t.Fatalf("held %s — want the ahead pre-release and the two newest stable releases, "+
			"no draft (a draft is not published) and no older release", got)
	}
}

// A pre-release BEHIND the newest stable is not what a development-channel
// install wants and only costs disk.
func TestMirrorSkipsAPrereleaseBehindStable(t *testing.T) {
	stubPairingVerifier(t)
	pre := goodRelease("v3.17.10-rc1", t0.Add(3*time.Hour))
	pre.prerelease = true
	g := newFakeGitHub(t, goodRelease("v3.17.65", t0), pre)
	m := newTestMirror(t)
	if err := m.Sync(context.Background(), g.client()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(heldTags(m), ","); got != "v3.17.65" {
		t.Fatalf("held %s, want only v3.17.65", got)
	}
}

// Each of these is a release that must NOT be held, broken in exactly one way.
// The reason is asserted, not just the refusal: two guards for one outcome are
// individually unkillable by an outcome-only assertion.
func TestMirrorRefusesAReleaseAnInstallWouldRefuse(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(*fakeRelease)
		reason string
	}{
		{"binary bytes differ from GitHub's recorded digest", func(r *fakeRelease) {
			r.files[0].digest = "sha256:" + strings.Repeat("0", 64)
		}, "does not match the digest GitHub recorded"},
		// Both checksum files go: with one left, the installer's selector takes
		// the release's only .sha256 for the binary, and the refusal becomes a
		// checksum mismatch — a different rule.
		{"binary has no checksum file", func(r *fakeRelease) {
			r.files = []fakeFile{r.files[0], r.files[2], r.files[3], r.files[4]}
		}, "has no .sha256 beside it"},
		{"binary's checksum file names other bytes", func(r *fakeRelease) {
			r.files[1].body = []byte(strings.Repeat("a", 64) + "  vayupress\n")
		}, "checksum mismatch"},
		{"binary has no Sigstore bundle", func(r *fakeRelease) {
			r.files = append(r.files[:2], r.files[3:]...)
		}, "carries no Sigstore signature"},
		{"binary's bundle signs something else", func(r *fakeRelease) {
			r.files[2].body = []byte("bundle-for:" + sha([]byte("the attacker's binary")))
		}, "signature does not cover this artifact"},
		{"binary has no Ed25519 signature", func(r *fakeRelease) {
			r.files = append(r.files[:3], r.files[4:]...)
		}, "carries no Ed25519 signature"},
		{"binary is not an executable", func(r *fakeRelease) {
			zip := []byte("PK\x03\x04 not a program")
			r.files[0].body = zip
			r.files[1].body = []byte(sha(zip) + "  vayupress\n")
			r.files[2].body = []byte("bundle-for:" + sha(zip))
		}, "not an executable"},
		{"release carries no installable binary", func(r *fakeRelease) {
			r.files = r.files[4:]
		}, "carries no installable binary"},
		{"a helper has nothing to verify it against", func(r *fakeRelease) {
			r.files[4].digest = ""
			r.files = r.files[:5]
		}, "has no digest, checksum or signature"},
		{"a helper's checksum file names other bytes", func(r *fakeRelease) {
			r.files[4].digest = ""
			r.files[5].body = []byte(strings.Repeat("b", 64) + "  vayuprovision-helpers.tar.gz\n")
		}, "vayuprovision-helpers.tar.gz: update: checksum mismatch"},
		{"a file downloads from somewhere other than this release", func(r *fakeRelease) {
			r.files[4].url = "https://evil.example/vayuprovision-helpers.tar.gz"
		}, "does not download from this release on github.com"},
		{"a file name would escape the release directory", func(r *fakeRelease) {
			r.files[4].name = "../../state.json"
		}, "cannot be stored safely"},
		{"a file arrives at a different size than GitHub lists", func(r *fakeRelease) {
			r.files[4].size = len(r.files[4].body) + 1
		}, "GitHub lists"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubPairingVerifier(t)
			rel := goodRelease("v3.17.65", t0)
			tc.break_(&rel)
			g := newFakeGitHub(t, rel)
			m := newTestMirror(t)
			err := m.Sync(context.Background(), g.client())
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("sync error = %v, want the reason %q", err, tc.reason)
			}
			if len(m.State().Releases) != 0 {
				t.Fatal("the release was held anyway — the mirror now advertises an update " +
					"that no install will accept, or worse")
			}
			if _, statErr := os.Stat(filepath.Join(m.Root, "releases", "v3.17.65")); !os.IsNotExist(statErr) {
				t.Fatal("a refused release left files in the served directory")
			}
		})
	}
}

// Seeded to be the positive twin of the table above: the unbroken release is
// held, and every file records the checks it passed. Without it, a verifier that
// refused everything would pass every case in the table.
func TestMirrorHoldsAnIntactReleaseAndRecordsItsChecks(t *testing.T) {
	stubPairingVerifier(t)
	g := newFakeGitHub(t, goodRelease("v3.17.65", t0))
	m := newTestMirror(t)
	if err := m.Sync(context.Background(), g.client()); err != nil {
		t.Fatal(err)
	}
	rel := m.State().Releases[0]
	checks := map[string]string{}
	for _, a := range rel.Assets {
		checks[a.Name] = strings.Join(a.Checks, ",")
	}
	if want := "github-digest,sha256-file,sigstore,ed25519,executable"; checks["vayupress"] != want {
		t.Errorf("binary checks = %q, want %q", checks["vayupress"], want)
	}
	if want := "github-digest,sha256-file"; checks["vayuprovision-helpers.tar.gz"] != want {
		t.Errorf("helper checks = %q, want %q", checks["vayuprovision-helpers.tar.gz"], want)
	}
}

// The mirror must reach the REAL signature verifier. With the stub removed, a
// release whose bundle is not a genuine Sigstore bundle is refused — the
// harness's inability to sign is itself the assertion.
func TestMirrorUsesTheRealSignatureVerifier(t *testing.T) {
	g := newFakeGitHub(t, goodRelease("v3.17.65", t0))
	m := newTestMirror(t)
	err := m.Sync(context.Background(), g.client())
	if err == nil || !strings.Contains(err.Error(), "signature bundle could not be read") {
		t.Fatalf("sync error = %v — a made-up bundle was not refused by the production verifier", err)
	}
	if len(m.State().Releases) != 0 {
		t.Fatal("an unsigned release was held")
	}
}

// Legacy-format bundles are left to `cosign verify-blob` on the host that
// installs the helper. Recognition must be by shape: a bundle that is neither
// format is attempted and refused, never waved through for failing to parse.
func TestMirrorLegacyBundleRecognitionIsByShapeNotByParseFailure(t *testing.T) {
	legacy := []byte(`{"base64Signature":"c2ln","cert":"Y2VydA==","rekorBundle":{"SignedEntryTimestamp":"x","Payload":{}}}`)
	if !isLegacyCosignBundle(legacy) {
		t.Fatal("the shape cosign sign-blob writes without --new-bundle-format was not recognised")
	}
	for _, junk := range []string{`{}`, `not json`, `{"base64Signature":"c2ln"}`, `{"mediaType":"application/vnd.dev.sigstore.bundle+json;version=0.3"}`} {
		if isLegacyCosignBundle([]byte(junk)) {
			t.Errorf("%s was accepted as a legacy bundle and would skip verification", junk)
		}
	}

	stubPairingVerifier(t)
	rel := goodRelease("v3.17.65", t0)
	rel.files = append(rel.files, fakeFile{name: "vayuprovision-helpers.tar.gz.cosign.bundle", body: []byte("garbage"), digest: "auto"})
	g := newFakeGitHub(t, rel)
	m := newTestMirror(t)
	if err := m.Sync(context.Background(), g.client()); err == nil ||
		!strings.Contains(err.Error(), "vayuprovision-helpers.tar.gz: update: the release signature bundle could not be read") {
		t.Fatalf("a helper bundle in no known format was not attempted and refused: %v", err)
	}
}

// A release that fails must not take the mirror below what it held, and must be
// retried next hour — which means its validator must not be stored.
func TestAFailedReleaseKeepsWhatWasHeldAndIsRetried(t *testing.T) {
	stubPairingVerifier(t)
	g := newFakeGitHub(t, goodRelease("v3.17.64", t0), goodRelease("v3.17.63", t0.Add(-time.Hour)))
	m := newTestMirror(t)
	if err := m.Sync(context.Background(), g.client()); err != nil {
		t.Fatal(err)
	}

	broken := goodRelease("v3.17.65", t0.Add(time.Hour))
	broken.files[2].body = []byte("bundle-for:nothing")
	g.mu.Lock()
	g.releases = append(g.releases, broken)
	g.etag = `W/"v2"`
	g.mu.Unlock()
	if err := m.Sync(context.Background(), g.client()); err == nil {
		t.Fatal("the broken release was not reported")
	}
	if got := strings.Join(heldTags(m), ","); got != "v3.17.64,v3.17.63" {
		t.Fatalf("held %s after a failed release — the mirror lost a release it could still serve", got)
	}
	if st := m.State(); st.ETag != "" || st.LastError == "" {
		t.Fatalf("etag=%q lastError=%q — storing the validator hides the broken release behind a "+
			"304 forever, so a corrected upload would never be picked up", st.ETag, st.LastError)
	}

	// The release is fixed upstream under the SAME list validator a stale
	// mirror would have sent; the mirror must still see it.
	g.mu.Lock()
	g.releases[2] = goodRelease("v3.17.65", t0.Add(time.Hour))
	g.mu.Unlock()
	if err := m.Sync(context.Background(), g.client()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(heldTags(m), ","); got != "v3.17.65,v3.17.64" {
		t.Fatalf("held %s after the fix", got)
	}
	if _, err := os.Stat(filepath.Join(m.Root, "releases", "v3.17.63")); !os.IsNotExist(err) {
		t.Fatal("a release that left the held set was not removed from disk")
	}
}

// An unchanged list costs one conditional request and no downloads — unless a
// held file has gone missing, in which case the validator must not paper over it
// and the held release must not be reused as though it were whole.
func TestAnUnchangedListIsConditionalUnlessAFileWentMissing(t *testing.T) {
	stubPairingVerifier(t)
	g := newFakeGitHub(t, goodRelease("v3.17.65", t0))
	m := newTestMirror(t)
	if err := m.Sync(context.Background(), g.client()); err != nil {
		t.Fatal(err)
	}
	fetched := g.downloads
	if err := m.Sync(context.Background(), g.client()); err != nil {
		t.Fatal(err)
	}
	if g.downloads != fetched {
		t.Fatalf("an unchanged list re-downloaded %d files", g.downloads-fetched)
	}
	if m.State().ETag == "" {
		t.Fatal("a clean sync stored no validator")
	}

	bin := filepath.Join(m.Root, "releases", "v3.17.65", "vayupress")
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if err := m.Sync(context.Background(), g.client()); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(bin); err != nil || string(b) != string(elf("binary v3.17.65")) {
		t.Fatalf("the missing binary was not restored (%v) — the mirror keeps advertising a "+
			"file it 404s on, and every install falling back to it fails mid-update", err)
	}
}

// The public protocol: exactly the URLs the fallback client in 3.17.64+ builds.
// Written as literals, not through the shared constants, because installs
// already running have these strings compiled in; a constant renamed on both
// sides would keep this package consistent and strand every one of them.
func TestTheMirrorAnswersTheURLsShippedInstallsRequest(t *testing.T) {
	stubPairingVerifier(t)
	g := newFakeGitHub(t, goodRelease("v3.17.65", t0))
	m := newTestMirror(t)
	if err := m.Sync(context.Background(), g.client()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(m)
	defer srv.Close()

	get := func(path string) (int, string, http.Header) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}

	// Stable channel, both casings of the repository a client may use.
	for _, p := range []string{
		"/api/github/repos/johalputt/VayuPress/releases/latest",
		"/api/github/repos/johalputt/vayupress/releases/latest",
	} {
		code, body, _ := get(p)
		if code != 200 || !strings.Contains(body, `"tag_name":"v3.17.65"`) {
			t.Errorf("%s → %d %.80s", p, code, body)
		}
	}
	// Development channel.
	if code, body, _ := get("/api/github/repos/johalputt/VayuPress/releases?per_page=30"); code != 200 || !strings.HasPrefix(body, "[") {
		t.Errorf("list → %d %.80s", code, body)
	}
	// A release file, byte for byte.
	code, body, h := get("/download/github/johalputt/VayuPress/releases/download/v3.17.65/vayupress")
	if code != 200 || body != string(elf("binary v3.17.65")) {
		t.Fatalf("download → %d, %d bytes", code, len(body))
	}
	if h.Get("Content-Type") != "application/octet-stream" || !strings.Contains(h.Get("Cache-Control"), "immutable") {
		t.Errorf("download headers: %v", h)
	}
}

// What a stranger would try against a public mirror.
func TestTheMirrorIsNotARelayAndServesOnlyWhatItHolds(t *testing.T) {
	stubPairingVerifier(t)
	g := newFakeGitHub(t, goodRelease("v3.17.65", t0))
	m := newTestMirror(t)
	if err := m.Sync(context.Background(), g.client()); err != nil {
		t.Fatal(err)
	}
	// A file on disk inside the mirror root but not in any release.
	if err := os.WriteFile(filepath.Join(m.Root, "secret.txt"), []byte("not for you"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(m)
	defer srv.Close()
	listed := g.listCalls

	for _, p := range []string{
		"/api/github/repos/torvalds/linux/releases/latest", // someone else's repo: a relay would fetch it
		"/api/github/user", // any other GitHub API path
		"/api/github/repos/johalputt/VayuPress/releases/tags/v3.17.65",             // a real API path the mirror does not implement
		"/download/github/johalputt/VayuPress/releases/download/v3.17.65/nope",     // a file the release lacks
		"/download/github/johalputt/VayuPress/releases/download/v3.17.1/vayupress", // a release not held
		"/download/github/johalputt/VayuPress/releases/download/v3.17.65/..%2f..%2fstate.json",
		"/download/github/johalputt/VayuPress/releases/download/../../secret.txt",
		"/download/github/other/VayuPress/releases/download/v3.17.65/vayupress", // right file, wrong owner
	} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s → %d %.60q, want 404", p, resp.StatusCode, b)
		}
		if strings.Contains(string(b), "not for you") || strings.Contains(string(b), "etag") {
			t.Errorf("%s leaked a file outside the held releases", p)
		}
	}
	if g.listCalls != listed {
		t.Error("serving a request made an outbound call to GitHub — the mirror is acting as a relay")
	}

	resp, err := http.Post(srv.URL+"/api/github/repos/johalputt/VayuPress/releases", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST → %d, want 405", resp.StatusCode)
	}
}

// The public status says what is held and whether the mirror is healthy. It
// must not carry the operator's error text, which names addresses and paths.
func TestThePublicStatusCarriesNoInfrastructureDetail(t *testing.T) {
	m := newTestMirror(t)
	secret := `dial tcp 10.9.8.7:443: i/o timeout reading /var/lib/vayupress/release-mirror`
	if err := m.commit(MirrorState{CheckedAt: t0, LastError: secret}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, MirrorStatusPath, nil))
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status → %d", rec.Code)
	}
	for _, leak := range []string{"10.9.8.7", "/var/lib", "i/o timeout"} {
		if strings.Contains(body, leak) {
			t.Errorf("the public status leaked %q: %s", leak, body)
		}
	}
	if !strings.Contains(body, `"healthy":false`) {
		t.Errorf("a failing mirror reported itself healthy: %s", body)
	}
}

// The state survives a restart: a mirror that forgot what it held would serve
// nothing until the next hourly sync.
func TestTheMirrorServesWhatItHeldAcrossARestart(t *testing.T) {
	stubPairingVerifier(t)
	g := newFakeGitHub(t, goodRelease("v3.17.65", t0))
	m := newTestMirror(t)
	if err := m.Sync(context.Background(), g.client()); err != nil {
		t.Fatal(err)
	}
	again := NewMirror(m.Root, "johalputt", "VayuPress")
	rec := httptest.NewRecorder()
	again.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/download/github/johalputt/VayuPress/releases/download/v3.17.65/vayupress", nil))
	if rec.Code != 200 || rec.Body.String() != string(elf("binary v3.17.65")) {
		t.Fatalf("after restart: %d", rec.Code)
	}
}

func TestOnlyOneSyncRunsAtATime(t *testing.T) {
	m := newTestMirror(t)
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	if err := m.Sync(context.Background(), http.DefaultClient); !errors.Is(err, ErrMirrorBusy) {
		t.Fatalf("second sync = %v, want ErrMirrorBusy", err)
	}
}

func TestMirrorBoundsAReleaseBeforeDownloadingIt(t *testing.T) {
	stubPairingVerifier(t)
	rel := goodRelease("v3.17.65", t0)
	for i := 0; i < mirrorMaxAssets; i++ {
		rel.files = append(rel.files, fakeFile{name: fmt.Sprintf("extra-%d.txt", i), body: []byte("x"), digest: "auto"})
	}
	g := newFakeGitHub(t, rel)
	m := newTestMirror(t)
	if err := m.Sync(context.Background(), g.client()); err == nil || !strings.Contains(err.Error(), "files (the mirror holds") {
		t.Fatalf("an oversized release was not refused up front: %v", err)
	}
}

// The mirror's JSON paths bypass the shield (the updater cannot solve a
// challenge), which also takes them out of the shield's rate limiting. So the
// per-request cost must be a copy of bytes computed when the state changed —
// not a fresh encode of every release's notes for every anonymous request,
// which is an unmetered compute sink on a public path.
func TestTheMirrorsJSONIsEncodedOncePerStateNotPerRequest(t *testing.T) {
	stubPairingVerifier(t)
	g := newFakeGitHub(t, goodRelease("v3.17.65", t0), goodRelease("v3.17.64", t0.Add(-time.Hour)))
	m := newTestMirror(t)
	if err := m.Sync(context.Background(), g.client()); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{MirrorStatusPath, "/api/github/repos/johalputt/VayuPress/releases"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		allocs := testing.AllocsPerRun(50, func() {
			m.ServeHTTP(httptest.NewRecorder(), req)
		})
		// A recorder and its header map cost a handful; re-encoding the state
		// costs hundreds.
		if allocs > 25 {
			t.Errorf("%s costs %.0f allocations per request — it is re-encoded on every hit", p, allocs)
		}
	}
}
