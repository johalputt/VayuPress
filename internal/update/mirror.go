// SPDX-License-Identifier: Apache-2.0

package update

// The release mirror: the server half of the fallback chain in endpoints.go.
//
// An install whose host cannot reach GitHub falls back to OfficialMirror. That
// host has to exist and has to hold something worth installing, so any VayuPress
// install can serve it: it pulls the project's recent releases from GitHub on a
// timer, VERIFIES each one exactly as ApplyVerified would, and answers the three
// paths the fallback client requests — from what it holds, never by relaying.
//
// Two decisions that shape everything below:
//
//   - Verify before holding. The installs downloading from a mirror verify the
//     bytes themselves, so a mirror that stored whatever GitHub returned would
//     not endanger them. It would still advertise a release no install can
//     accept — an update that exists on the page and fails on every server —
//     and it would do so silently. A release that does not verify is not held,
//     not advertised, and the reason is on the operator's panel.
//   - Serve, never relay. The Cloudflare Worker this replaces forwarded any path
//     under /api/github/ to api.github.com, which made it an open GitHub proxy.
//     This answers only from the state file: an unknown owner, tag or file is a
//     404, and nothing a request carries ever reaches an outbound call.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// The paths the fallback client builds (mirrorLatest, mirrorReleasesList,
// mirrorAssetURL) and this server parses. Installs on 3.17.64 and later have
// them compiled in, so they are a published contract, not an internal detail.
const (
	mirrorAPIPrefix      = "/api/github/"
	mirrorDownloadPrefix = "/download/github/"
	// MirrorStatusPath is the public summary the mirror's own web page reads.
	MirrorStatusPath = "/mirror/status.json"
)

const (
	// mirrorKeep bounds what is held: the newest stable release, the one before
	// it (an install that has to roll back still finds files), and the newest
	// pre-release when it is ahead of both.
	mirrorKeep = 3
	// A release is read into memory to be verified, because the verifiers take
	// bytes. Today's release is ~60 MiB across fourteen files; the bound exists
	// so a release list that suddenly claims gigabytes is refused rather than
	// allowed to exhaust the host.
	mirrorMaxReleaseBytes = int64(256) << 20
	mirrorMaxAssets       = 64
	// The GitHub list request: 30 is the page the fallback client also reads.
	mirrorListQuery = "?per_page=30"
)

// ErrMirrorBusy is returned when a sync is already running.
var ErrMirrorBusy = errors.New("update: a mirror sync is already running")

// mirrorName is what a tag or file name must look like before it becomes a path
// component. GitHub permits far more; this mirror stores only names it can put
// on disk without interpretation. No separator and no leading dot means neither
// "." nor ".." nor a traversal can be spelled in it.
var mirrorName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)

func validMirrorName(s string) bool { return mirrorName.MatchString(s) }

// MirrorState is the mirror's persisted record, written atomically beside the
// release directories it describes.
type MirrorState struct {
	// CheckedAt is the last sync attempt, successful or not.
	CheckedAt time.Time `json:"checked_at"`
	// SyncedAt is the last attempt that left every target release held.
	SyncedAt time.Time `json:"synced_at"`
	// LastError is empty after a clean sync. It is operator-facing only and is
	// never served publicly: transport errors carry addresses and paths.
	LastError string `json:"last_error,omitempty"`
	// ETag is GitHub's validator for the release list. Stored only after a
	// clean sync, so a release that failed to verify is retried next time
	// instead of being hidden behind a 304.
	ETag     string            `json:"etag,omitempty"`
	Releases []MirroredRelease `json:"releases"`
}

// MirroredRelease is one release held on disk, newest first in MirrorState.
type MirroredRelease struct {
	Tag        string          `json:"tag"`
	Prerelease bool            `json:"prerelease"`
	Published  time.Time       `json:"published"`
	VerifiedAt time.Time       `json:"verified_at"`
	Assets     []MirroredAsset `json:"assets"`
	// Raw is the release object exactly as GitHub's API published it — what
	// the fallback client decodes, so it cannot tell the mirror from GitHub.
	Raw json.RawMessage `json:"raw"`
}

// MirroredAsset records one held file and every check it passed.
type MirroredAsset struct {
	Name   string   `json:"name"`
	Size   int64    `json:"size"`
	SHA256 string   `json:"sha256"`
	Checks []string `json:"checks"`
}

// Bytes is the disk the held releases occupy.
func (s MirrorState) Bytes() int64 {
	var n int64
	for _, r := range s.Releases {
		for _, a := range r.Assets {
			n += a.Size
		}
	}
	return n
}

// Mirror holds verified releases of one repository under Root.
type Mirror struct {
	Root  string
	Owner string
	Repo  string

	syncMu sync.Mutex // one sync at a time; TryLock turns a second into ErrMirrorBusy

	mu     sync.RWMutex
	st     MirrorState
	bodies mirrorBodies
}

// mirrorBodies are the mirror's JSON answers, encoded once whenever the state
// changes. These paths bypass the shield — the updater cannot solve a
// challenge — and with it the shield's rate limiting, so a request must cost a
// copy of these bytes, not a fresh encode of every release's notes.
type mirrorBodies struct {
	status []byte
	list   []byte
	latest []byte // nil when no stable release is held
}

// NewMirror opens the mirror rooted at root, loading its state if one exists.
// A missing or unreadable state file is an empty mirror, not an error: the next
// sync rebuilds it, and serving must never fail on the file that describes it.
func NewMirror(root, owner, repo string) *Mirror {
	m := &Mirror{Root: root, Owner: owner, Repo: repo}
	if raw, err := os.ReadFile(m.statePath()); err == nil {
		var st MirrorState
		if json.Unmarshal(raw, &st) == nil {
			m.st = st
		}
	}
	m.bodies = m.encodeBodies(m.st)
	return m
}

func (m *Mirror) encodeBodies(st MirrorState) mirrorBodies {
	var b mirrorBodies
	b.status = encodeMirrorJSON(m.publicStatus(st))
	list := make([]json.RawMessage, 0, len(st.Releases))
	for _, rel := range st.Releases {
		list = append(list, rel.Raw)
		if b.latest == nil && !rel.Prerelease {
			b.latest = encodeMirrorJSON(rel.Raw)
		}
	}
	b.list = encodeMirrorJSON(list)
	return b
}

func encodeMirrorJSON(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return []byte("null\n")
	}
	return buf.Bytes()
}

func (m *Mirror) statePath() string   { return filepath.Join(m.Root, "state.json") }
func (m *Mirror) releasesDir() string { return filepath.Join(m.Root, "releases") }
func (m *Mirror) stagingDir() string  { return filepath.Join(m.Root, ".staging") }

// State returns a copy of the current state.
func (m *Mirror) State() MirrorState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := m.st
	st.Releases = append([]MirroredRelease(nil), m.st.Releases...)
	return st
}

func (m *Mirror) commit(st MirrorState) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return err
	}
	tmp := m.statePath() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.statePath()); err != nil {
		return err
	}
	bodies := m.encodeBodies(st)
	m.mu.Lock()
	m.st, m.bodies = st, bodies
	m.mu.Unlock()
	return nil
}

// listedRelease is one entry of GitHub's releases list, decoded for the fields
// the mirror acts on, with the original bytes kept for serving.
type listedRelease struct {
	TagName     string    `json:"tag_name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
		Digest             string `json:"digest"`
	} `json:"assets"`
	raw json.RawMessage
}

// mirrorTargets picks what the mirror should hold from GitHub's list: the newest
// stable release, the stable release before it, and the newest pre-release if it
// is ahead of the newest stable one. Drafts never count.
func mirrorTargets(list []listedRelease) []listedRelease {
	var stable, pre []listedRelease
	for _, r := range list {
		if r.Draft || r.TagName == "" {
			continue
		}
		if r.Prerelease {
			pre = append(pre, r)
		} else {
			stable = append(stable, r)
		}
	}
	newestFirst := func(s []listedRelease) {
		sort.SliceStable(s, func(i, j int) bool {
			if c := CompareVersions(s[i].TagName, s[j].TagName); c != 0 {
				return c > 0
			}
			return s[i].PublishedAt.After(s[j].PublishedAt)
		})
	}
	newestFirst(stable)
	newestFirst(pre)
	var out []listedRelease
	if len(pre) > 0 && (len(stable) == 0 || CompareVersions(pre[0].TagName, stable[0].TagName) > 0) {
		out = append(out, pre[0])
	}
	for i := 0; i < len(stable) && i < 2; i++ {
		out = append(out, stable[i])
	}
	return out
}

// Sync pulls the release list from GitHub and brings the held set up to date.
// It returns ErrMirrorBusy when another sync holds the lock, and otherwise the
// problem that kept any target from being held (nil after a clean sync).
func (m *Mirror) Sync(ctx context.Context, client *http.Client) error {
	if !m.syncMu.TryLock() {
		return ErrMirrorBusy
	}
	defer m.syncMu.Unlock()

	st := m.State()
	st.CheckedAt = time.Now().UTC()
	for _, r := range st.Releases {
		if !m.releaseIntact(r) {
			// A held file vanished from disk. The list may be unchanged, so the
			// validator would answer 304 and the gap would never close.
			st.ETag = ""
		}
	}

	listURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases%s", m.Owner, m.Repo, mirrorListQuery)
	list, etag, notModified, err := fetchReleaseList(ctx, client, listURL, st.ETag)
	if err != nil {
		st.LastError = "could not read the release list from GitHub: " + err.Error()
		_ = m.commit(st)
		return errors.New(st.LastError)
	}
	if notModified {
		st.LastError = ""
		st.SyncedAt = st.CheckedAt
		return m.commit(st)
	}

	targets := mirrorTargets(list)
	if len(targets) == 0 {
		st.LastError = "GitHub lists no published release to mirror"
		_ = m.commit(st)
		return errors.New(st.LastError)
	}

	held := map[string]MirroredRelease{}
	for _, r := range st.Releases {
		held[r.Tag] = r
	}
	var keep []MirroredRelease
	var problems []string
	for _, t := range targets {
		if h, ok := held[t.TagName]; ok && m.releaseIntact(h) {
			// Already verified and on disk. The raw object is refreshed so an
			// edit to the release notes reaches the page; the files are
			// immutable at a tag and are not fetched again.
			h.Raw = t.raw
			keep = append(keep, h)
			continue
		}
		r, ferr := m.fetchAndVerify(ctx, client, t)
		if ferr != nil {
			problems = append(problems, t.TagName+": "+ferr.Error())
			continue
		}
		keep = append(keep, r)
	}
	// A target that failed must not leave the mirror holding less than it did:
	// previously held releases fill the gap, newest first. Only then — after a
	// clean sync the held set is exactly the targets.
	if len(problems) > 0 {
		for _, r := range st.Releases {
			if len(keep) >= mirrorKeep {
				break
			}
			if !containsTag(keep, r.Tag) && m.releaseIntact(r) {
				keep = append(keep, r)
			}
		}
	}
	sort.SliceStable(keep, func(i, j int) bool {
		if c := CompareVersions(keep[i].Tag, keep[j].Tag); c != 0 {
			return c > 0
		}
		return keep[i].Published.After(keep[j].Published)
	})

	st.Releases = keep
	if len(problems) == 0 {
		st.LastError = ""
		st.SyncedAt = st.CheckedAt
		st.ETag = etag
	} else {
		st.LastError = strings.Join(problems, "; ")
		st.ETag = ""
	}
	if err := m.commit(st); err != nil {
		return err
	}
	m.prune(keep)
	if len(problems) > 0 {
		return errors.New(st.LastError)
	}
	return nil
}

func containsTag(rs []MirroredRelease, tag string) bool {
	for _, r := range rs {
		if r.Tag == tag {
			return true
		}
	}
	return false
}

// releaseIntact reports whether every file of a held release is still on disk
// at its recorded size. Cheap: a stat per file.
func (m *Mirror) releaseIntact(r MirroredRelease) bool {
	for _, a := range r.Assets {
		fi, err := os.Stat(filepath.Join(m.releasesDir(), r.Tag, a.Name))
		if err != nil || fi.Size() != a.Size {
			return false
		}
	}
	return true
}

// prune removes release directories the state no longer lists, and staging.
func (m *Mirror) prune(keep []MirroredRelease) {
	_ = os.RemoveAll(m.stagingDir())
	entries, err := os.ReadDir(m.releasesDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if !containsTag(keep, e.Name()) {
			_ = os.RemoveAll(filepath.Join(m.releasesDir(), e.Name()))
		}
	}
}

// fetchReleaseList GETs GitHub's release list, conditionally when a validator
// is known. It keeps each release's original bytes for serving.
func fetchReleaseList(ctx context.Context, client *http.Client, listURL, etag string) ([]listedRelease, string, bool, error) {
	resp, err := githubGetConditional(ctx, client, listURL, etag)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified && etag != "" {
		return nil, etag, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("github returned status %d", resp.StatusCode)
	}
	var raws []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raws); err != nil {
		return nil, "", false, fmt.Errorf("decode: %w", err)
	}
	out := make([]listedRelease, 0, len(raws))
	for _, raw := range raws {
		var r listedRelease
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, "", false, fmt.Errorf("decode release: %w", err)
		}
		r.raw = raw
		out = append(out, r)
	}
	return out, resp.Header.Get("ETag"), false, nil
}

// fetchAndVerify downloads every file of one release into staging, verifies the
// release as a whole, and only then moves it into place.
func (m *Mirror) fetchAndVerify(ctx context.Context, client *http.Client, t listedRelease) (MirroredRelease, error) {
	if !validMirrorName(t.TagName) {
		return MirroredRelease{}, fmt.Errorf("tag %q cannot be stored safely", t.TagName)
	}
	if len(t.Assets) == 0 || len(t.Assets) > mirrorMaxAssets {
		return MirroredRelease{}, fmt.Errorf("release has %d files (the mirror holds 1–%d)", len(t.Assets), mirrorMaxAssets)
	}
	var total int64
	for _, a := range t.Assets {
		if !validMirrorName(a.Name) {
			return MirroredRelease{}, fmt.Errorf("file name %q cannot be stored safely", a.Name)
		}
		// Pin the download to this release on github.com. The list came from
		// GitHub over TLS, but an asset URL pointing anywhere else would turn
		// the mirror into a fetcher of arbitrary hosts.
		want := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", m.Owner, m.Repo, t.TagName, a.Name)
		if !strings.EqualFold(a.BrowserDownloadURL, want) {
			return MirroredRelease{}, fmt.Errorf("file %s does not download from this release on github.com", a.Name)
		}
		total += a.Size
	}
	if total > mirrorMaxReleaseBytes {
		return MirroredRelease{}, fmt.Errorf("release is %d MiB, over the mirror's %d MiB bound", total>>20, mirrorMaxReleaseBytes>>20)
	}

	data := make(map[string][]byte, len(t.Assets))
	assets := make([]Asset, 0, len(t.Assets))
	for _, a := range t.Assets {
		b, err := download(ctx, client, a.BrowserDownloadURL)
		if err != nil {
			return MirroredRelease{}, fmt.Errorf("download %s: %w", a.Name, err)
		}
		if int64(len(b)) != a.Size {
			return MirroredRelease{}, fmt.Errorf("%s arrived as %d bytes, GitHub lists %d", a.Name, len(b), a.Size)
		}
		data[a.Name] = b
		assets = append(assets, Asset{Name: a.Name, DownloadURL: a.BrowserDownloadURL, Size: a.Size})
	}

	checks, err := verifyMirroredRelease(t, assets, data)
	if err != nil {
		return MirroredRelease{}, err
	}

	stage := filepath.Join(m.stagingDir(), t.TagName)
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return MirroredRelease{}, err
	}
	out := MirroredRelease{
		Tag: t.TagName, Prerelease: t.Prerelease, Published: t.PublishedAt,
		VerifiedAt: time.Now().UTC(), Raw: t.raw,
	}
	for _, a := range t.Assets {
		b := data[a.Name]
		if err := os.WriteFile(filepath.Join(stage, a.Name), b, 0o644); err != nil {
			return MirroredRelease{}, err
		}
		sum := sha256.Sum256(b)
		out.Assets = append(out.Assets, MirroredAsset{
			Name: a.Name, Size: a.Size, SHA256: hex.EncodeToString(sum[:]), Checks: checks[a.Name],
		})
	}
	final := filepath.Join(m.releasesDir(), t.TagName)
	if err := os.MkdirAll(m.releasesDir(), 0o755); err != nil {
		return MirroredRelease{}, err
	}
	_ = os.RemoveAll(final)
	if err := os.Rename(stage, final); err != nil {
		return MirroredRelease{}, err
	}
	return out, nil
}

// Check names, as recorded per file and shown on the panel and the page.
const (
	checkDigest   = "github-digest" // matches the SHA-256 GitHub records for the upload
	checkChecksum = "sha256-file"   // matches the release's own <name>.sha256
	checkSigstore = "sigstore"      // signed by the release workflow (pinned identity)
	checkEd25519  = "ed25519"       // signed by the project's release key
	checkELF      = "executable"    // is a Linux executable image
)

// verifyMirroredRelease applies, to every file, the strongest check the release
// offers for it — and refuses the release if any file is covered by none.
//
// Every installable binary passes exactly what ApplyVerified demands, through
// the same selectors and the same verifier, so the mirror cannot hold a release
// an install would refuse. The other files (helper archives, the SBOM, the
// packaged site, the sidecars themselves) must match GitHub's recorded digest,
// plus their own checksum and signature when the release ships one.
func verifyMirroredRelease(t listedRelease, assets []Asset, data map[string][]byte) (map[string][]string, error) {
	checks := make(map[string][]string, len(assets))
	for _, a := range t.Assets {
		if a.Digest == "" {
			continue
		}
		want, ok := strings.CutPrefix(a.Digest, "sha256:")
		if !ok {
			continue // an algorithm this mirror does not know proves nothing either way
		}
		sum := sha256.Sum256(data[a.Name])
		if !strings.EqualFold(hex.EncodeToString(sum[:]), want) {
			return nil, fmt.Errorf("%s does not match the digest GitHub recorded for it", a.Name)
		}
		checks[a.Name] = append(checks[a.Name], checkDigest)
	}

	installable := 0
	for _, a := range assets {
		if isMetadataAsset(a.Name) || isArchiveAsset(a.Name) {
			continue
		}
		installable++
		b := data[a.Name]
		sum := selectChecksumAsset(assets, a.Name)
		if sum == nil {
			return nil, fmt.Errorf("%s has no .sha256 beside it; no install would accept it", a.Name)
		}
		if err := VerifyChecksum(b, checksumForFile(data[sum.Name], a.Name)); err != nil {
			return nil, fmt.Errorf("%s: %w", a.Name, err)
		}
		bundle := selectBundleAsset(assets, a.Name)
		if bundle == nil {
			return nil, fmt.Errorf("%s carries no Sigstore signature; no install would accept it", a.Name)
		}
		sigHex := ""
		names := []string{checkChecksum, checkSigstore}
		if ReleaseRequiresEd25519() {
			sig := selectSidecar(assets, a.Name, ".sig")
			if sig == nil {
				return nil, fmt.Errorf("%s carries no Ed25519 signature; no install would accept it", a.Name)
			}
			sigHex = string(data[sig.Name])
			names = append(names, checkEd25519)
		}
		if err := verifyReleaseSignature(b, data[bundle.Name], sigHex); err != nil {
			return nil, fmt.Errorf("%s: %w", a.Name, err)
		}
		if err := verifyExecutableImage(b, "linux", a.Name); err != nil {
			return nil, err
		}
		checks[a.Name] = append(checks[a.Name], append(names, checkELF)...)
	}
	if installable == 0 {
		return nil, errors.New("release carries no installable binary")
	}

	for _, a := range assets {
		if !isMetadataAsset(a.Name) && !isArchiveAsset(a.Name) {
			continue // an installable, done above
		}
		if s, ok := data[a.Name+".sha256"]; ok {
			if err := VerifyChecksum(data[a.Name], checksumForFile(s, a.Name)); err != nil {
				return nil, fmt.Errorf("%s: %w", a.Name, err)
			}
			checks[a.Name] = append(checks[a.Name], checkChecksum)
		}
		// The helper archives, the shield agent and the SBOM are signed without
		// --new-bundle-format, and their real verifier is `cosign verify-blob`
		// on the host that installs them, which reads that legacy shape. This
		// verifier reads only the new one. Recognised legacy bundles are left to
		// that consumer and do not count as a check here; anything that is
		// neither shape is still attempted, and refused when it fails.
		if b, ok := data[a.Name+".cosign.bundle"]; ok && !isLegacyCosignBundle(b) {
			if err := VerifyReleaseBundle(data[a.Name], b); err != nil {
				return nil, fmt.Errorf("%s: %w", a.Name, err)
			}
			checks[a.Name] = append(checks[a.Name], checkSigstore)
		}
		if len(checks[a.Name]) == 0 {
			return nil, fmt.Errorf("%s has no digest, checksum or signature to verify it against", a.Name)
		}
	}
	return checks, nil
}

// isLegacyCosignBundle reports whether b is the bundle `cosign sign-blob`
// writes without --new-bundle-format: a signature, a certificate and a Rekor
// entry at the top level.
func isLegacyCosignBundle(b []byte) bool {
	var legacy struct {
		Sig  string          `json:"base64Signature"`
		Cert string          `json:"cert"`
		Log  json.RawMessage `json:"rekorBundle"`
	}
	return json.Unmarshal(b, &legacy) == nil && legacy.Sig != "" && legacy.Cert != "" && len(legacy.Log) > 0
}

// IsMirrorPath reports whether a request path belongs to the mirror protocol.
// The app layer uses it to route and to exempt these paths from the browser
// challenge, which the fallback client can never solve.
func IsMirrorPath(p string) bool {
	return p == MirrorStatusPath ||
		strings.HasPrefix(p, mirrorAPIPrefix) ||
		strings.HasPrefix(p, mirrorDownloadPrefix)
}

// IsMirrorDownloadPath reports whether p requests a release file — the one
// mirror path that costs real bandwidth.
func IsMirrorDownloadPath(p string) bool { return strings.HasPrefix(p, mirrorDownloadPrefix) }

// ServeHTTP answers the mirror protocol from the held state. Callers route only
// IsMirrorPath requests here; anything under those prefixes that names nothing
// held is a 404.
func (m *Mirror) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m.mu.RLock()
	st, bodies := m.st, m.bodies
	m.mu.RUnlock()
	p := r.URL.Path
	switch {
	case p == MirrorStatusPath:
		writeMirrorJSON(w, bodies.status)
	case strings.HasPrefix(p, mirrorAPIPrefix):
		m.serveAPI(w, r, bodies, strings.TrimPrefix(p, mirrorAPIPrefix))
	case strings.HasPrefix(p, mirrorDownloadPrefix):
		m.serveFile(w, r, st, strings.TrimPrefix(p, mirrorDownloadPrefix))
	default:
		http.NotFound(w, r)
	}
}

// ownRepo reports whether owner/repo name the repository this mirror holds.
// GitHub treats both case-insensitively, and the releases JSON uses the
// canonical "VayuPress" while older builds ask for "vayupress".
func (m *Mirror) ownRepo(owner, repo string) bool {
	return strings.EqualFold(owner, m.Owner) && strings.EqualFold(repo, m.Repo)
}

// serveAPI answers repos/{owner}/{repo}/releases and …/releases/latest with the
// held releases' original JSON. The query string is ignored: the list is at
// most mirrorKeep long, which is inside any page size a client asks for.
func (m *Mirror) serveAPI(w http.ResponseWriter, r *http.Request, bodies mirrorBodies, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) < 4 || parts[0] != "repos" || !m.ownRepo(parts[1], parts[2]) || parts[3] != "releases" {
		http.NotFound(w, r)
		return
	}
	switch {
	case len(parts) == 4:
		writeMirrorJSON(w, bodies.list)
	case len(parts) == 5 && parts[4] == "latest" && bodies.latest != nil:
		writeMirrorJSON(w, bodies.latest)
	default:
		http.NotFound(w, r)
	}
}

// serveFile streams one held file: {owner}/{repo}/releases/download/{tag}/{name}.
func (m *Mirror) serveFile(w http.ResponseWriter, r *http.Request, st MirrorState, rest string) {
	parts := strings.Split(rest, "/")
	if len(parts) != 6 || !m.ownRepo(parts[0], parts[1]) || parts[2] != "releases" || parts[3] != "download" {
		http.NotFound(w, r)
		return
	}
	tag, name := parts[4], parts[5]
	var asset *MirroredAsset
	var rel *MirroredRelease
	for i := range st.Releases {
		if st.Releases[i].Tag != tag {
			continue
		}
		rel = &st.Releases[i]
		for j := range rel.Assets {
			if rel.Assets[j].Name == name {
				asset = &rel.Assets[j]
			}
		}
	}
	// The path is built only from names the state recorded, which were
	// validated before they were stored. Nothing from the request reaches it.
	if asset == nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(m.releasesDir(), rel.Tag, asset.Name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	// The server's WriteTimeout is 30 seconds — right for pages, and an abort
	// for a 56 MB binary on a slow link, which is exactly the kind of host
	// that needs this mirror. The deadline is lifted to a bound rather than
	// removed, so a client that stops reading cannot hold a connection forever.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))

	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", `attachment; filename="`+asset.Name+`"`)
	// A tag's files never change; GitHub marks these releases immutable.
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("ETag", `"sha256:`+asset.SHA256+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, asset.Name, rel.VerifiedAt, f)
}

func writeMirrorJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Short: a new release should reach a checking install within minutes of
	// the sync that fetched it.
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(body)
}

// PublicMirrorStatus is what anyone may read about the mirror. It carries no
// error text: a transport error names addresses and paths, and a public page
// should say what the mirror holds, not describe the machine serving it.
type PublicMirrorStatus struct {
	Repository string                `json:"repository"`
	CheckedAt  *time.Time            `json:"checked_at"`
	SyncedAt   *time.Time            `json:"synced_at"`
	Healthy    bool                  `json:"healthy"`
	Releases   []PublicMirrorRelease `json:"releases"`
}

// PublicMirrorRelease is one held release as the mirror's page shows it.
type PublicMirrorRelease struct {
	Version    string              `json:"version"`
	Prerelease bool                `json:"prerelease"`
	Published  time.Time           `json:"published"`
	VerifiedAt time.Time           `json:"verified_at"`
	Notes      string              `json:"notes"`
	SourceURL  string              `json:"source_url"`
	Files      []PublicMirrorAsset `json:"files"`
}

// PublicMirrorAsset is one held file with its mirror download path.
type PublicMirrorAsset struct {
	Name   string   `json:"name"`
	Size   int64    `json:"size"`
	SHA256 string   `json:"sha256"`
	Checks []string `json:"checks"`
	URL    string   `json:"url"`
}

func (m *Mirror) publicStatus(st MirrorState) PublicMirrorStatus {
	out := PublicMirrorStatus{
		Repository: m.Owner + "/" + m.Repo,
		Healthy:    st.LastError == "" && len(st.Releases) > 0,
		Releases:   []PublicMirrorRelease{},
	}
	if !st.CheckedAt.IsZero() {
		t := st.CheckedAt
		out.CheckedAt = &t
	}
	if !st.SyncedAt.IsZero() {
		t := st.SyncedAt
		out.SyncedAt = &t
	}
	for _, rel := range st.Releases {
		var meta struct {
			Body    string `json:"body"`
			HTMLURL string `json:"html_url"`
		}
		_ = json.Unmarshal(rel.Raw, &meta)
		pr := PublicMirrorRelease{
			Version: rel.Tag, Prerelease: rel.Prerelease, Published: rel.Published,
			VerifiedAt: rel.VerifiedAt, Notes: meta.Body, SourceURL: meta.HTMLURL,
		}
		for _, a := range rel.Assets {
			pr.Files = append(pr.Files, PublicMirrorAsset{
				Name: a.Name, Size: a.Size, SHA256: a.SHA256, Checks: a.Checks,
				URL: mirrorDownloadPrefix + url.PathEscape(m.Owner) + "/" + url.PathEscape(m.Repo) +
					"/releases/download/" + url.PathEscape(rel.Tag) + "/" + url.PathEscape(a.Name),
			})
		}
		out.Releases = append(out.Releases, pr)
	}
	return out
}
