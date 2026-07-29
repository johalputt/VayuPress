// SPDX-License-Identifier: Apache-2.0

package intel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/safefetch"
)

// fetch.go — getting a feed onto the machine, and keeping the last known-good
// copy when that fails.
//
// Every fetch goes through safefetch, which means two things this package gets
// for free and must not work around: the SSRF barrier (a feed URL is operator
// text, and an operator who pastes an internal address must not turn this into a
// probe of their own network), and the clearnet kill-switch, so a Tor Space
// makes no outbound request at all.

const (
	// maxFeedBytes caps one feed response. The largest thing here is a cloud
	// vendor's full published ranges, which is low single-digit megabytes; this
	// is above that and far below anything that makes a refresh expensive.
	maxFeedBytes = 8 << 20
	// fetchTimeout per feed. Refreshes happen on a background ticker and a slow
	// endpoint must never hold one up long enough to overlap the next.
	fetchTimeout = 30 * time.Second
)

// Feed is one source of network intelligence.
//
// There is no field for "what this grants", because a feed cannot grant
// anything — see Kind. The most a Feed definition can choose is whether its
// entries are evidence (datacenter) or grounds to refuse (hostile).
type Feed struct {
	// ID is stable and used for the cache filename, the panel and the operator's
	// opt-in setting. Renaming one silently orphans its cache and resets its
	// delta baseline, so they are written once.
	ID string
	// Name is what an operator reads.
	Name string
	// URL is where it is published. HTTPS only — see Fetcher.Refresh.
	URL string
	// Kind is what membership means.
	Kind Kind
	// Note is the one line the panel shows about provenance, because "trust this
	// list" is not a thing to ask without saying whose list it is.
	Note string
	// Parse extracts prefixes. Feeds publish wildly different shapes; a parser
	// per feed is honest, and a single tolerant parser that accepted all of them
	// would also accept a hijacked response that looked like none of them.
	Parse func([]byte) ([]netip.Prefix, error)
}

// Status is what an operator needs to decide whether a feed is working.
type Status struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Note      string    `json:"note"`
	Enabled   bool      `json:"enabled"`
	Entries   int       `json:"entries"`
	Ranges    int       `json:"ranges"`
	Checksum  string    `json:"checksum"`
	FetchedAt time.Time `json:"fetched_at,omitempty"`
	// LastError is why the most recent refresh did not take. Kept visible
	// because a feed that silently stopped updating looks exactly like a feed
	// that is working, and the difference is months of stale intelligence.
	LastError string `json:"last_error,omitempty"`
	// Refused records a refresh rejected by the delta bound rather than by a
	// transport error. That is a different and more interesting event: it means
	// the endpoint served something that did not look like what it served
	// yesterday.
	Refused string `json:"refused,omitempty"`
}

// Fetcher refreshes a set of feeds and holds their compiled sets.
type Fetcher struct {
	cacheDir string
	client   *safefetch.Client

	mu    sync.RWMutex
	feeds map[string]*feedState
	order []string
}

type feedState struct {
	def       Feed
	enabled   bool
	live      Live
	lastErr   string
	refused   string
	fetchedAt time.Time
}

// NewFetcher builds a fetcher writing its last-good cache under cacheDir.
func NewFetcher(cacheDir string) *Fetcher {
	return &Fetcher{
		cacheDir: cacheDir,
		client: safefetch.New(safefetch.Options{
			MaxBytes:       maxFeedBytes,
			Timeout:        fetchTimeout,
			AllowedSchemes: []string{"https"},
			UserAgent:      "VayuShield-Intel/1.0 (+https://vayupress.com)",
		}),
		feeds: map[string]*feedState{},
	}
}

// Add registers a feed. enabled reflects the operator's opt-in: a registered but
// disabled feed is never fetched and its set stays empty, so switching one off
// costs nothing rather than merely hiding it.
func (f *Fetcher) Add(def Feed, enabled bool) {
	if f == nil || def.ID == "" || !def.Kind.Valid() || def.Parse == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if st, ok := f.feeds[def.ID]; ok {
		st.def, st.enabled = def, enabled
		if !enabled {
			st.live.Store(Set{})
		}
		return
	}
	f.feeds[def.ID] = &feedState{def: def, enabled: enabled}
	f.order = append(f.order, def.ID)
}

// Match reports the strongest kind matching an address, and which feed said so.
//
// Hostile beats datacenter: they answer different questions, and a source that
// is both is one an operator would want refused rather than merely scored.
func (f *Fetcher) Match(ip string) (Kind, string) {
	if f == nil {
		return kindUnset, ""
	}
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return kindUnset, ""
	}
	a = a.Unmap().WithZone("")

	f.mu.RLock()
	defer f.mu.RUnlock()
	var dcSource string
	for _, id := range f.order {
		st := f.feeds[id]
		if !st.enabled {
			continue
		}
		s := st.live.Get()
		if s.Len() == 0 || !s.Contains(a) {
			continue
		}
		if s.Kind() == KindHostile {
			return KindHostile, st.def.Name
		}
		if dcSource == "" {
			dcSource = st.def.Name
		}
	}
	if dcSource != "" {
		return KindDatacenter, dcSource
	}
	return kindUnset, ""
}

// Statuses reports every registered feed for the panel and the posture report.
func (f *Fetcher) Statuses() []Status {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]Status, 0, len(f.order))
	for _, id := range f.order {
		st := f.feeds[id]
		s := st.live.Get()
		out = append(out, Status{
			ID: st.def.ID, Name: st.def.Name, Kind: st.def.Kind.String(),
			Note: st.def.Note, Enabled: st.enabled,
			Entries: s.Entries(), Ranges: s.Len(), Checksum: s.Checksum(),
			FetchedAt: st.fetchedAt, LastError: st.lastErr, Refused: st.refused,
		})
	}
	return out
}

// LoadCache seeds every enabled feed from its last-good copy on disk.
//
// Called at boot so a restart does not leave the shield with no intelligence
// until the first refresh lands — which on a slow or blocked network could be
// never. A cache entry that fails to parse is discarded silently: it is a
// convenience, and a corrupt one must not stop the process starting.
func (f *Fetcher) LoadCache() {
	if f == nil || f.cacheDir == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.order {
		st := f.feeds[id]
		if !st.enabled {
			continue
		}
		b, err := os.ReadFile(f.cachePath(id)) //nolint:gosec // path is derived from a hashed feed ID
		if err != nil {
			continue
		}
		// Re-parsed with the feed's OWN parser, from the bytes it served. Caching
		// the compiled set instead would mean converting merged ranges back into
		// prefixes, which is lossy in both directions — the entry count would not
		// survive, and that count is the delta baseline the next refresh is
		// measured against.
		prefixes, err := st.def.Parse(b)
		if err != nil {
			continue
		}
		if set, err := Build(st.def.Kind, st.def.Name, prefixes); err == nil && set.Len() > 0 {
			st.live.Store(set)
		}
	}
}

// Refresh fetches every enabled feed and swaps in the ones that pass.
//
// Fails soft, per feed. A feed that errors, times out or serves something the
// delta bound refuses keeps its last-good set — the alternative is that one
// unreachable endpoint disarms a layer, which turns a third party's outage into
// this site's vulnerability.
func (f *Fetcher) Refresh(ctx context.Context) {
	if f == nil {
		return
	}
	// No outbound request at all in a Tor Space. Checked here as well as inside
	// safefetch: a layer that silently makes no requests is indistinguishable
	// from one that is broken, and this way the reason is recorded per feed.
	if safefetch.ClearnetBlocked() {
		f.mu.Lock()
		for _, id := range f.order {
			f.feeds[id].lastErr = "clearnet egress is blocked in this Space; feeds do not refresh here"
		}
		f.mu.Unlock()
		return
	}

	f.mu.RLock()
	todo := make([]Feed, 0, len(f.order))
	for _, id := range f.order {
		if f.feeds[id].enabled {
			todo = append(todo, f.feeds[id].def)
		}
	}
	f.mu.RUnlock()

	for _, def := range todo {
		set, body, err := f.fetchOne(ctx, def)
		f.mu.Lock()
		st := f.feeds[def.ID]
		switch {
		case err != nil:
			st.lastErr = err.Error()
		default:
			st.lastErr = ""
			if ok, why := AcceptRefresh(st.live.Get(), set); !ok {
				// NOT applied. This is the integrity control doing its job, and it
				// is recorded distinctly from a transport error because it means
				// something quite different: the endpoint answered, and answered
				// with something unlike what it served last time.
				st.refused = why
			} else {
				st.refused = ""
				st.live.Store(set)
				st.fetchedAt = time.Now()
				f.writeCache(def.ID, body)
			}
		}
		f.mu.Unlock()
	}
}

// fetchOne returns the compiled set and the raw bytes it came from. The bytes
// are what gets cached — see LoadCache.
func (f *Fetcher) fetchOne(ctx context.Context, def Feed) (Set, []byte, error) {
	res, err := f.client.Get(ctx, def.URL)
	if err != nil {
		return Set{}, nil, err
	}
	prefixes, err := def.Parse(res.Body)
	if err != nil {
		return Set{}, nil, err
	}
	set, err := Build(def.Kind, def.Name, prefixes)
	if err != nil {
		return Set{}, nil, err
	}
	return set, res.Body, nil
}

// cachePath hashes the feed ID so an ID can never escape the cache directory,
// whatever it contains.
func (f *Fetcher) cachePath(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(f.cacheDir, "intel-"+hex.EncodeToString(sum[:8])+".json")
}

// writeCache stores the last-good set. Best-effort: a read-only or full disk is
// a reason to lose the boot-time seed, never a reason to fail a refresh that
// already succeeded in memory.
func (f *Fetcher) writeCache(id string, body []byte) {
	if f.cacheDir == "" || len(body) == 0 {
		return
	}
	if err := os.MkdirAll(f.cacheDir, 0o750); err != nil {
		return
	}
	_ = os.WriteFile(f.cachePath(id), body, 0o600)
}
