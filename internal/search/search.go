// SPDX-License-Identifier: Apache-2.0

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
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
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
	// SaveIndex persists the current index to path so the next start can restore
	// it instead of rescanning the whole store. Cheap (reads id+updated_at only).
	SaveIndex(ctx context.Context, path string) error
	// LoadIndex restores the index from path and reconciles ONLY the articles that
	// changed since it was saved — no full rescan. Returns (false, nil) when there
	// is no usable snapshot, so the caller falls back to a full Load. Any snapshot
	// problem returns false: the snapshot is a pure optimisation and can never make
	// search worse than a rebuild.
	LoadIndex(ctx context.Context, path string) (bool, error)
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

	titleLower   string
	tagsLower    string
	excerptLower string // excerpt lowercased — the excerpt-only fallback haystack
	// (title and tags are matched via titleLower/tagsLower above, so the excerpt
	// fallback never needs them folded in; storing only the excerpt here instead
	// of a title+tags+excerpt concatenation drops the largest redundant copy from
	// every indexed doc without changing any ranking. RAM audit 2026-07 / L2a)
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
	d.excerptLower = strings.ToLower(ex)
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

// ── Incremental persistence (ADR-0110) ───────────────────────────────────────
//
// The in-memory index is rebuilt from scratch on every boot by Load, which scans
// every published article — minutes of work on a large store. SaveIndex/LoadIndex
// persist the index so a restart or version update RESTORES it and reconciles
// only what changed, instead of rebuilding. It is defensive by construction: any
// problem with the snapshot makes LoadIndex report "no snapshot" so the caller
// does the same full Load as before — persistence can never make search worse.

const persistVersion = 2

// persistDoc is the on-disk form of an indexed document. Lowercased match fields
// are recomputed on restore (not stored), and UpdatedAt lets a later load detect
// which articles changed.
type persistDoc struct {
	ID, Title, Slug, Excerpt string
	Tags                     []string
	CreatedAt, UpdatedAt     int64
}

type persistedIndex struct {
	Version int
	Docs    []persistDoc
}

// publishedFilter is the WHERE clause selecting exactly the documents the index
// holds (published, non-page articles), shared by Load, SaveIndex and LoadIndex.
const publishedFilter = `COALESCE(status,'published')='published' AND COALESCE(is_page,0)=0`

// SaveIndex writes the current index to path atomically, stamping each document
// with its article's updated_at (read via a cheap id+timestamp scan — no content)
// so LoadIndex can reconcile only what changed.
func (s *builtinService) SaveIndex(ctx context.Context, path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	upd := make(map[string]int64, 1024)
	if rows, err := s.db.QueryContext(ctx, `SELECT id,updated_at FROM articles WHERE `+publishedFilter); err == nil {
		for rows.Next() {
			var id string
			var t time.Time
			if rows.Scan(&id, &t) == nil {
				upd[id] = t.Unix()
			}
		}
		_ = rows.Err()
		rows.Close()
	}
	s.mu.RLock()
	docs := make([]persistDoc, 0, len(s.byID))
	for _, d := range s.byID {
		docs = append(docs, persistDoc{
			ID: d.ID, Title: d.Title, Slug: d.Slug, Excerpt: d.Excerpt,
			Tags: d.Tags, CreatedAt: d.CreatedAt, UpdatedAt: upd[d.ID],
		})
	}
	s.mu.RUnlock()

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(persistedIndex{Version: persistVersion, Docs: docs}); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path) // atomic replace
}

// LoadIndex restores the index from path, then reconciles only changed/new/removed
// articles. See the doc comment on the interface method for the fallback contract.
func (s *builtinService) LoadIndex(ctx context.Context, path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil // no snapshot yet → caller does a full Load
	}
	var pi persistedIndex
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&pi); err != nil || pi.Version != persistVersion {
		return false, nil // unreadable/old-format snapshot → full Load
	}

	restored := make(map[string]*doc, len(pi.Docs))
	savedUpd := make(map[string]int64, len(pi.Docs))
	for i := range pi.Docs {
		p := pi.Docs[i]
		restored[p.ID] = docFromPersist(p)
		savedUpd[p.ID] = p.UpdatedAt
	}

	// Cheap delta scan: current published id+updated_at (no content read).
	rows, err := s.db.QueryContext(ctx, `SELECT id,updated_at FROM articles WHERE `+publishedFilter)
	if err != nil {
		return false, nil // cannot verify deltas → fall back to full Load
	}
	current := make(map[string]struct{}, len(restored))
	var changed []string
	for rows.Next() {
		var id string
		var t time.Time
		if rows.Scan(&id, &t) != nil {
			continue
		}
		current[id] = struct{}{}
		if old, ok := savedUpd[id]; !ok || t.Unix() != old {
			changed = append(changed, id) // new or modified since the snapshot
		}
	}
	if rerr := rows.Err(); rerr != nil {
		rows.Close()
		return false, nil // incomplete delta scan → fall back to a full Load
	}
	rows.Close()

	// Drop documents that are no longer published (deleted/unpublished).
	for id := range restored {
		if _, ok := current[id]; !ok {
			delete(restored, id)
		}
	}

	// Publish the restored set immediately — search is live now, from the snapshot.
	s.mu.Lock()
	s.byID = restored
	s.mu.Unlock()
	s.snap.Store(nil)

	// Fetch content and (re)index ONLY the changed/new articles.
	s.reindexByIDs(ctx, changed)
	return true, nil
}

// reindexByIDs fetches full content for the given ids and (re)indexes them, in
// bounded chunks so the IN(...) placeholder list stays small.
func (s *builtinService) reindexByIDs(ctx context.Context, ids []string) {
	const chunk = 400
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]interface{}, len(batch))
		for i, v := range batch {
			args[i] = v
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT id,title,slug,content,tags,created_at FROM articles WHERE id IN (`+placeholders+`)`, args...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id, title, slug, content, tagsCSV string
			var created time.Time
			if rows.Scan(&id, &title, &slug, &content, &tagsCSV, &created) != nil {
				continue
			}
			d := makeDoc(id, title, slug, content, splitCSV(tagsCSV), created.Unix())
			s.mu.Lock()
			s.byID[id] = d
			s.mu.Unlock()
		}
		_ = rows.Err()
		rows.Close()
	}
	if len(ids) > 0 {
		s.snap.Store(nil)
	}
}

// docFromPersist rebuilds an in-memory doc (with its precomputed lowercase match
// fields) from the persisted form.
func docFromPersist(p persistDoc) *doc {
	d := &doc{
		ID: p.ID, Title: p.Title, Slug: p.Slug, Excerpt: p.Excerpt,
		Tags: p.Tags, CreatedAt: p.CreatedAt,
		titleLower: strings.ToLower(p.Title),
		tagsLower:  strings.ToLower(strings.Join(p.Tags, " ")),
	}
	d.excerptLower = strings.ToLower(p.Excerpt)
	return d
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
			if wordHit(d.excerptLower, t) {
				termScore = 8
			} else if strings.Contains(d.excerptLower, t) {
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
	N     int          `json:"n"` // total published posts (may exceed len(Posts) when capped)
	Posts []clientPost `json:"posts"`
}

// clientSnapshotMax bounds how many of the most-recent posts are shipped in the
// downloadable client search index (see Snapshot). The in-browser modal gives
// instant, zero-latency search over this recent window; the full archive stays
// searchable server-side via /search (which scans the complete in-memory index),
// surfaced by the modal's "search the full archive" action. Capping keeps both
// the client download and the resident server snapshot small and bounded no
// matter how large the corpus grows. (client-perf / RAM audit 2026-07 / L2b)
const clientSnapshotMax = 5000

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

	total := len(docs)
	sort.Slice(docs, func(i, j int) bool { return docs[i].CreatedAt > docs[j].CreatedAt })

	// Ship only the most-recent clientSnapshotMax posts to the browser. The full
	// archive remains searchable server-side via /search; the modal offers a
	// one-click jump there when total exceeds what it holds locally. (L2b)
	if len(docs) > clientSnapshotMax {
		docs = docs[:clientSnapshotMax]
	}

	// Build the post slice and a content version in a single pass. The version
	// must stay fully content-sensitive (a title/excerpt/tag/date edit has to
	// bump it so browsers/CDNs re-fetch), but we no longer marshal the entire
	// index to JSON *twice* — once to hash, once to serve. Instead we fold the
	// same fields into a streaming SHA-256 as we build the slice, then marshal
	// the payload exactly once. On a large store this removes an ~O(index-size)
	// transient JSON blob (and the hash over it) from every rebuild. (L3)
	posts := make([]clientPost, 0, len(docs))
	h := sha256.New()
	// hash.Hash.Write never returns an error; these helpers discard the
	// (n, err) results so the intent stays readable and errcheck stays happy.
	hw := func(b []byte) { _, _ = h.Write(b) }
	hs := func(s string) { _, _ = io.WriteString(h, s) }
	fieldSep := []byte{0x1f} // unit separator between fields
	tagSep := []byte{0x1e}   // record separator between tags
	recSep := []byte{0x00}   // NUL between documents
	var nbuf [8]byte
	for _, d := range docs {
		posts = append(posts, clientPost{T: d.Title, U: d.Slug, E: d.Excerpt, G: d.Tags, D: d.CreatedAt})
		hs(d.Title)
		hw(fieldSep)
		hs(d.Slug)
		hw(fieldSep)
		hs(d.Excerpt)
		hw(fieldSep)
		for _, g := range d.Tags {
			hs(g)
			hw(tagSep)
		}
		binary.LittleEndian.PutUint64(nbuf[:], uint64(d.CreatedAt))
		hw(nbuf[:])
		hw(recSep)
	}
	// Fold the total post count into the version so the client re-fetches when
	// the corpus crosses the cap boundary (its full-archive hint depends on the
	// total), even if the recent window's contents are otherwise unchanged.
	binary.LittleEndian.PutUint64(nbuf[:], uint64(total))
	hw(nbuf[:])
	sum := h.Sum(nil)
	version := hex.EncodeToString(sum[:8])
	payload, _ := json.Marshal(clientIndex{V: version, N: total, Posts: posts})

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
