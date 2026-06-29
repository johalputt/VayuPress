// Package secwatch implements the VayuOS security-update watcher.
//
// It tracks the upstream releases of the security-critical Go dependencies that
// power VayuPress's sovereignty layers — notably ProtonMail go-crypto (PGP) and
// Cloudflare CIRCL (the elliptic-curve backend) — and surfaces when a newer
// upstream release exists so the operator can apply security patches promptly.
//
// Privacy by default: the watcher is DISABLED unless the operator explicitly
// enables it. It is not telemetry — it sends nothing about the operator or
// their site; it only fetches public release metadata from api.github.com, and
// only when enabled. The actual upgrade is performed by the operator via the
// documented `go get -u <module> && go build` maintenance path; the watcher
// never mutates the binary or dependencies on its own.
package secwatch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// trackedRepos maps a Go module path to its GitHub owner/repo. These are the
// dependencies whose security advisories matter most to VayuPress sovereignty.
var trackedRepos = map[string]string{
	"github.com/ProtonMail/go-crypto":    "ProtonMail/go-crypto",
	"github.com/cloudflare/circl":        "cloudflare/circl",
	"github.com/mattn/go-sqlite3":        "mattn/go-sqlite3",
	"github.com/yuin/goldmark":           "yuin/goldmark",
	"github.com/microcosm-cc/bluemonday": "microcosm-cc/bluemonday",
	"github.com/go-chi/chi/v5":           "go-chi/chi",
}

// Component is one watched dependency and its update state.
type Component struct {
	Name            string `json:"name"`
	Module          string `json:"module"`
	Repo            string `json:"repo"`
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	Note            string `json:"note"`
}

// Report is the result of a watcher run.
type Report struct {
	Enabled          bool        `json:"enabled"`
	CheckedAt        time.Time   `json:"checked_at"`
	Components       []Component `json:"components"`
	UpdatesAvailable int         `json:"updates_available"`
	UpgradeHint      string      `json:"upgrade_hint"`
}

// Watcher checks upstream releases for the tracked dependencies.
type Watcher struct {
	enabled bool
	client  *http.Client
	apiBase string

	mu   sync.RWMutex
	last *Report
}

// New returns a watcher. When enabled is false it performs no network I/O.
func New(enabled bool) *Watcher {
	return &Watcher{
		enabled: enabled,
		client:  &http.Client{Timeout: 10 * time.Second},
		apiBase: "https://api.github.com",
	}
}

// Name identifies the subsystem for the boot orchestrator.
func (w *Watcher) Name() string { return "VayuSecWatch" }

// Start satisfies the VayuOS boot Subsystem interface. The watcher performs no
// blocking I/O at boot; checks happen on demand or on a timer when enabled.
func (w *Watcher) Start(_ context.Context) error { return nil }

// Enabled reports whether the watcher may reach the network.
func (w *Watcher) Enabled() bool { return w.enabled }

// Components returns the tracked dependencies with their currently-built
// versions, read from the embedded build info (no network).
func (w *Watcher) Components() []Component {
	out := []Component{}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return out
	}
	for _, dep := range bi.Deps {
		repo, tracked := trackedRepos[dep.Path]
		if !tracked {
			continue
		}
		out = append(out, Component{
			Name:    shortName(dep.Path),
			Module:  dep.Path,
			Repo:    repo,
			Current: dep.Version,
		})
	}
	return out
}

// Check fetches the latest upstream release for each tracked dependency and
// compares it to the built version. When disabled it returns a report with
// Enabled=false and performs no network I/O.
func (w *Watcher) Check(ctx context.Context) (*Report, error) {
	rep := &Report{Enabled: w.enabled, CheckedAt: time.Now().UTC(), Components: w.Components()}
	if !w.enabled {
		w.store(rep)
		return rep, nil
	}
	for i := range rep.Components {
		c := &rep.Components[i]
		latest, err := w.latestRelease(ctx, c.Repo)
		if err != nil {
			c.Note = "check failed: " + err.Error()
			continue
		}
		c.Latest = latest
		if latest != "" && isNewer(latest, c.Current) {
			c.UpdateAvailable = true
			rep.UpdatesAvailable++
			c.Note = "newer upstream release available"
		} else {
			c.Note = "up to date"
		}
	}
	if rep.UpdatesAvailable > 0 {
		rep.UpgradeHint = "Run `go get -u <module>@latest && go build ./...` then redeploy to apply security patches."
	}
	w.store(rep)
	return rep, nil
}

// Last returns the most recent report, if any.
func (w *Watcher) Last() *Report {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.last
}

func (w *Watcher) store(r *Report) {
	w.mu.Lock()
	w.last = r
	w.mu.Unlock()
}

type ghRelease struct {
	TagName string `json:"tag_name"`
}

func (w *Watcher) latestRelease(ctx context.Context, repo string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.apiBase+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "VayuPress-SecWatch")
	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", &httpError{code: resp.StatusCode}
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", err
	}
	return rel.TagName, nil
}

type httpError struct{ code int }

func (e *httpError) Error() string { return "github api status " + itoa(e.code) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// shortName derives a human-friendly component name from a module path. It skips
// a trailing semantic-import major-version element (e.g. ".../chi/v5" → "chi")
// so the UI shows "chi" rather than the meaningless "v5".
func shortName(module string) string {
	parts := strings.Split(module, "/")
	last := parts[len(parts)-1]
	if len(parts) >= 2 && isMajorSuffix(last) {
		last = parts[len(parts)-2]
	}
	return last
}

// isMajorSuffix reports whether a path element is a Go module major-version
// suffix like "v2", "v5", "v10".
func isMajorSuffix(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isNewer reports whether the latest version is strictly greater than current,
// using a tolerant dotted-numeric (semver-ish) comparison. This avoids the false
// "update available" a plain inequality produced when the built version was a
// pseudo-version or otherwise differed without being older.
func isNewer(latest, current string) bool {
	lv, cv := splitVer(latest), splitVer(current)
	for i := 0; i < len(lv) || i < len(cv); i++ {
		var a, b int
		if i < len(lv) {
			a = lv[i]
		}
		if i < len(cv) {
			b = cv[i]
		}
		if a != b {
			return a > b
		}
	}
	return false
}

// splitVer parses a version into its numeric components (major, minor, patch…),
// stripping the leading "v" and any pre-release/build metadata.
func splitVer(v string) []int {
	v = normalizeVer(v)
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n := 0
		for _, r := range p {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		out = append(out, n)
	}
	return out
}

// normalizeVer trims a leading v and any build/pre-release metadata for a
// tolerant equality comparison.
func normalizeVer(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	return v
}
