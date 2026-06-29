// Package search provides VayuFind — VayuPress's built-in, dependency-free
// article search (ADR-0050, ADR-0101).
//
// Design goals (replacing the former external Meilisearch backend):
//
//   - Sovereign & lightweight: a compact in-memory index, no external process,
//     no network calls, a few MB of RAM for thousands of posts.
//   - Incremental: the index is mutated in place on every publish/update/delete
//     (Index/Delete) — it is never rebuilt from scratch on a content change. A
//     full Load() runs only once at boot (and as a reconciler safety net).
//   - Cache-friendly: the client widget downloads ONE compact JSON snapshot,
//     served with a content-hash version/ETag so a browser/CDN re-fetches it
//     only when the content actually changes. Per-keystroke filtering then runs
//     entirely client-side — zero server work per query.
//   - Accurate: a small field-weighted scorer (title ≫ tags ≫ excerpt) with
//     prefix/whole-word boosts and recency tie-breaking, shared in spirit by
//     the Go (server-rendered /search page) and JS (instant modal) sides.
package search

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/metrics"
)

// Hit is a single search result.
type Hit struct {
	Title     string    `json:"title"`
	Slug      string    `json:"slug"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
}

// Result wraps hits with metadata about the query.
type Result struct {
	Hits     []Hit  `json:"hits"`
	Query    string `json:"query"`
	Fallback bool   `json:"fallback,omitempty"`
}

// Service is the search contract. The sole implementation is the built-in
// in-memory engine below; the interface is retained so handlers, GraphQL, and
// the reconciler depend on behaviour rather than a concrete type.
type Service interface {
	// Search ranks indexed documents for q and returns up to limit hits.
	Search(ctx context.Context, q string, limit int) (Result, error)
	// Index upserts one document into the live in-memory index (incremental).
	Index(ctx context.Context, id, title, slug, content string, tags []string, createdAt int64) error
	// Delete removes one document from the index.
	Delete(ctx context.Context, id string) error
	// Ping reports backend health. The built-in engine is always ready.
	Ping(ctx context.Context) error
	// DocCount reports how many documents the index holds (drift detection).
	DocCount(ctx context.Context) (int, error)
	// Snapshot returns the compact client index payload (JSON) and its content
	// version. Callers serve it with the version as a strong ETag.
	Snapshot() (payload []byte, version string)
	// Load (re)builds the entire index from the article store. Run once at boot
	// and by the reconciler; never on the per-document hot path.
	Load(ctx context.Context) error
}

// =============================================================================
// Built-in engine
// =============================================================================

// enabled gates whether search runs at all. Set at boot and whenever the
// operator flips the Tools & Plugins "Search" switch. When off, Search returns
// no hits and Snapshot reports an empty index, so the public search box/modal
// (which is also hidden via render.SetSearchEnabled) does nothing. Default ON.
var enabled atomic.Bool

func init() { enabled.Store(true) }

// SetEnabled turns the built-in search engine on or off at runtime.
func SetEnabled(v bool) { enabled.Store(v) }

// doc is one indexed article. The lowercased fields are precomputed once at
// index time so Search never lower-cases inside its hot loop.
type doc struct {
	ID        string
	Title     string
	Slug      string
	Excerpt   string
	Tags      []string
	CreatedAt int64

	titleLower string
	tagsLower  string
	textLower  string // title + tags + excerpt, lowercased — the match haystack
}

// snapshot is the memoised client payload + its content version.
type snapshot struct {
	payload []byte
	version string
}

type builtinService struct {
	db *sql.DB

	mu   sync.RWMutex
	byID map[string]*doc

	// snap caches the serialized client index; it is cleared (set to nil) on any
	// mutation and lazily rebuilt on the next Snapshot() call.
	snapMu sync.Mutex
	snap   atomic.Pointer[snapshot]
}

// NewService returns the built-in search engine. Call Load once after the
// database is ready to populate the index.
func NewService(db *sql.DB) Service {
	return &builtinService{db: db, byID: make(map[string]*doc)}
}

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

const excerptLen = 200

// makeDoc normalises an article into an indexed document.
func makeDoc(id, title, slug, content string, tags []string, createdAt int64) *doc {
	ex := excerpt(content)
	tagsJoined := strings.Join(tags, " ")
	d := &doc{
		ID: id, Title: title, Slug: slug, Excerpt: ex, Tags: tags, CreatedAt: createdAt,
		titleLower: strings.ToLower(title),
		tagsLower:  strings.ToLower(tagsJoined),
	}
	d.textLower = d.titleLower + "\n" + d.tagsLower + "\n" + strings.ToLower(ex)
	return d
}

// excerpt produces a trimmed, single-line plain-text summary. content may be
// HTML (from Load) or already-stripped text (from the event hooks); either way
// tags are removed and whitespace collapsed.
func excerpt(content string) string {
	s := htmlTagRe.ReplaceAllString(content, " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > excerptLen {
		// Cut on a rune boundary near the limit.
		cut := excerptLen
		for cut > 0 && !validBoundary(s, cut) {
			cut--
		}
		if cut == 0 {
			cut = excerptLen
		}
		s = strings.TrimSpace(s[:cut]) + "…"
	}
	return s
}

func validBoundary(s string, i int) bool {
	if i >= len(s) {
		return true
	}
	return s[i]&0xC0 != 0x80 // not a UTF-8 continuation byte
}

func (s *builtinService) Index(_ context.Context, id, title, slug, content string, tags []string, createdAt int64) error {
	if id == "" {
		return nil
	}
	d := makeDoc(id, title, slug, content, tags, createdAt)
	s.mu.Lock()
	s.byID[id] = d
	s.mu.Unlock()
	s.snap.Store(nil) // invalidate the client snapshot
	return nil
}

func (s *builtinService) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	delete(s.byID, id)
	s.mu.Unlock()
	s.snap.Store(nil)
	return nil
}

func (s *builtinService) Ping(_ context.Context) error { return nil }

func (s *builtinService) DocCount(_ context.Context) (int, error) {
	s.mu.RLock()
	n := len(s.byID)
	s.mu.RUnlock()
	return n, nil
}

// Load rebuilds the whole index from the article store. Published, non-page
// articles only — exactly what the public search should surface.
func (s *builtinService) Load(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,title,slug,content,tags,created_at FROM articles
		 WHERE COALESCE(status,'published')='published' AND COALESCE(is_page,0)=0`)
	if err != nil {
		atomic.AddInt64(&metrics.MetricMeiliErrors, 1)
		return err
	}
	defer rows.Close()
	next := make(map[string]*doc, 256)
	for rows.Next() {
		var id, title, slug, content, tagsCSV string
		var created time.Time
		if rows.Scan(&id, &title, &slug, &content, &tagsCSV, &created) != nil {
			continue
		}
		next[id] = makeDoc(id, title, slug, content, splitCSV(tagsCSV), created.Unix())
	}
	if err := rows.Err(); err != nil {
		atomic.AddInt64(&metrics.MetricMeiliErrors, 1)
		return err
	}
	s.mu.Lock()
	s.byID = next
	s.mu.Unlock()
	s.snap.Store(nil)
	return nil
}

// scored pairs a document with its relevance score for sorting.
type scored struct {
	d     *doc
	score int
}

func (s *builtinService) Search(_ context.Context, q string, limit int) (Result, error) {
	if !enabled.Load() {
		return Result{Hits: []Hit{}, Query: q}, nil
	}
	terms := tokenize(q)
	if len(terms) == 0 {
		return Result{Hits: []Hit{}, Query: q}, nil
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	s.mu.RLock()
	matches := make([]scored, 0, 32)
	for _, d := range s.byID {
		if sc := scoreDoc(d, terms); sc > 0 {
			matches = append(matches, scored{d: d, score: sc})
		}
	}
	s.mu.RUnlock()

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].d.CreatedAt > matches[j].d.CreatedAt // newer first
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}
	hits := make([]Hit, 0, len(matches))
	for _, m := range matches {
		hits = append(hits, Hit{
			Title:     m.d.Title,
			Slug:      m.d.Slug,
			Tags:      m.d.Tags,
			CreatedAt: time.Unix(m.d.CreatedAt, 0).UTC(),
		})
	}
	return Result{Hits: hits, Query: q}, nil
}

// tokenize lowercases q and splits it into search terms on any non-alphanumeric
// rune, dropping empties. Mirrors the JS tokenizer in the modal widget.
func tokenize(q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	parts := strings.FieldsFunc(q, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// scoreDoc returns a relevance score for d against the AND-set of terms. Every
// term must hit somewhere (title, tags, or excerpt) or the score is 0, so
// results stay precise. Field weight: title ≫ tags ≫ excerpt, with a boost for
// whole-word and title-prefix matches.
func scoreDoc(d *doc, terms []string) int {
	total := 0
	for _, t := range terms {
		termScore := 0
		switch {
		case strings.HasPrefix(d.titleLower, t):
			termScore = 60
		case wordHit(d.titleLower, t):
			termScore = 45
		case strings.Contains(d.titleLower, t):
			termScore = 30
		}
		if wordHit(d.tagsLower, t) {
			termScore += 25
		} else if strings.Contains(d.tagsLower, t) {
			termScore += 12
		}
		if termScore == 0 { // not in title or tags — try the excerpt
			if wordHit(d.textLower, t) {
				termScore = 8
			} else if strings.Contains(d.textLower, t) {
				termScore = 4
			}
		}
		if termScore == 0 {
			return 0 // this term matched nothing → drop the doc (AND semantics)
		}
		total += termScore
	}
	return total
}

// wordHit reports whether term appears in haystack on word boundaries.
func wordHit(haystack, term string) bool {
	idx := 0
	for {
		i := strings.Index(haystack[idx:], term)
		if i < 0 {
			return false
		}
		i += idx
		leftOK := i == 0 || !isWordByte(haystack[i-1])
		rEnd := i + len(term)
		rightOK := rEnd >= len(haystack) || !isWordByte(haystack[rEnd])
		if leftOK && rightOK {
			return true
		}
		idx = i + 1
		if idx >= len(haystack) {
			return false
		}
	}
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// clientPost is one entry in the compact JSON index downloaded by the modal.
// Short keys keep the payload small: t=title, u=url-slug, e=excerpt, g=tags,
// d=created-unix.
type clientPost struct {
	T string   `json:"t"`
	U string   `json:"u"`
	E string   `json:"e"`
	G []string `json:"g,omitempty"`
	D int64    `json:"d"`
}

type clientIndex struct {
	V     string       `json:"v"`
	Posts []clientPost `json:"posts"`
}

// Snapshot returns the memoised client index payload, rebuilding it only when a
// mutation has invalidated it. When search is disabled it reports an empty
// index so the modal has nothing to show.
func (s *builtinService) Snapshot() ([]byte, string) {
	if !enabled.Load() {
		return []byte(`{"v":"off","posts":[]}`), "off"
	}
	if cur := s.snap.Load(); cur != nil {
		return cur.payload, cur.version
	}

	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	if cur := s.snap.Load(); cur != nil { // another goroutine rebuilt it
		return cur.payload, cur.version
	}

	s.mu.RLock()
	docs := make([]*doc, 0, len(s.byID))
	for _, d := range s.byID {
		docs = append(docs, d)
	}
	s.mu.RUnlock()

	sort.Slice(docs, func(i, j int) bool { return docs[i].CreatedAt > docs[j].CreatedAt })

	posts := make([]clientPost, 0, len(docs))
	for _, d := range docs {
		posts = append(posts, clientPost{T: d.Title, U: d.Slug, E: d.Excerpt, G: d.Tags, D: d.CreatedAt})
	}
	// Hash the post set (pre-version) for a stable content version.
	raw, _ := json.Marshal(posts)
	sum := sha256.Sum256(raw)
	version := hex.EncodeToString(sum[:8])
	payload, _ := json.Marshal(clientIndex{V: version, Posts: posts})

	snap := &snapshot{payload: payload, version: version}
	s.snap.Store(snap)
	return snap.payload, snap.version
}

// splitCSV splits a comma-separated tag string into trimmed, non-empty tags.
func splitCSV(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
