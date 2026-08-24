// SPDX-License-Identifier: Apache-2.0

package botdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Store is the SQLite-backed adaptive signature database. It records recurring
// fingerprints, learns bot candidates, supports operator review, and can
// export/import community signature files. It holds no in-memory table (the DB
// is the source of truth).
//
// Read/write split: writes (upserts, verify/dismiss) go to the single writer
// connection (db); the per-request signature Lookup — which runs on the hot
// classification path for every unverified request — goes to a SEPARATE read
// pool (rdb) so it never serialises on the writer. Without this, enabling bot
// protection would put a SELECT on the one writer connection for every public
// request, contending with every write and with itself. rdb defaults to db
// (safe) until UseReader wires the dedicated read pool.
type Store struct {
	db  *sql.DB
	rdb *sql.DB
}

// New wraps an open *sql.DB. The vayushield_signatures table must already exist
// (migration 055). Reads default to the same handle until UseReader is called.
func New(db *sql.DB) *Store { return &Store{db: db, rdb: db} }

// UseReader routes the read-only, per-request Lookup (and the panel read
// queries) through a dedicated read pool instead of the writer connection, so
// classification never contends on the single SQLite writer. Passing nil keeps
// the current handle. Call once at boot.
func (s *Store) UseReader(rdb *sql.DB) {
	if s == nil || rdb == nil {
		return
	}
	s.rdb = rdb
}

// reader returns the read handle, falling back to the writer if unset.
func (s *Store) reader() *sql.DB {
	if s.rdb != nil {
		return s.rdb
	}
	return s.db
}

// Observation is a single sighting of a fingerprint, used to upsert a row.
type Observation struct {
	FingerprintHash   string
	JA3               string
	JA4               string
	HTTP2SettingsHash string
	HeaderOrderHash   string
	UserAgentPattern  string // coarse UA family, never the full UA
	IPRangeHint       string
	PostQuantum       bool
	Classification    Classification
	BotName           string
	Confidence        float64
	AutoLearned       bool
}

// StoredSignature is a row from vayushield_signatures.
type StoredSignature struct {
	ID                int64          `json:"id"`
	FingerprintHash   string         `json:"fingerprint_hash"`
	JA3               string         `json:"ja3_hash"`
	JA4               string         `json:"ja4_hash"`
	HTTP2SettingsHash string         `json:"http2_settings_hash"`
	HeaderOrderHash   string         `json:"header_order_hash"`
	UserAgentPattern  string         `json:"user_agent_pattern"`
	IPRangeHint       string         `json:"ip_range_hint,omitempty"`
	PostQuantum       bool           `json:"post_quantum_present"`
	Classification    Classification `json:"classification"`
	BotName           string         `json:"bot_name"`
	Confidence        float64        `json:"confidence"`
	RequestCount      int64          `json:"request_count"`
	FalsePositives    int64          `json:"false_positive_count"`
	AutoLearned       bool           `json:"auto_learned"`
	OperatorVerified  bool           `json:"operator_verified"`
	Notes             string         `json:"notes,omitempty"`
	FirstSeen         time.Time      `json:"first_seen"`
	LastSeen          time.Time      `json:"last_seen"`
}

// autoPromoteThreshold is the request count at which a recurring auto-learned
// candidate has its confidence promoted (see the spec's "appears 5+ times").
const autoPromoteThreshold = 5

// Observe upserts a fingerprint sighting. On first sight it inserts the row; on
// repeat it bumps request_count and last_seen. When an auto-learned candidate
// crosses autoPromoteThreshold sightings AND has no recorded false positive, its
// confidence is nudged upward by +0.10/sighting (capped at 0.95) — so a
// persistent bot-like fingerprint reaches the scorer's action threshold (0.80)
// in a few sightings from the 0.7 detection seed, closing the learning loop
// without operator action. A false_positive_count>0 (a solved-challenge human
// proof) freezes the climb, so a coarse fingerprint many real users share never
// escalates. Operator-verified rows are left untouched.
func (s *Store) Observe(ctx context.Context, o Observation) error {
	if s == nil || s.db == nil {
		return nil
	}
	return observe(ctx, s.db, o)
}

// ObserveTx performs the same fingerprint upsert against a caller-owned execer
// (a *sql.Tx), so per-request learning can be batched off the request path by
// the async writer instead of hitting the single SQLite writer synchronously.
func (s *Store) ObserveTx(ctx context.Context, e execer, o Observation) error {
	if s == nil {
		return nil
	}
	return observe(ctx, e, o)
}

// execer is satisfied by both *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func observe(ctx context.Context, e execer, o Observation) error {
	if e == nil || o.FingerprintHash == "" {
		return nil
	}
	now := time.Now().UTC()
	if o.Classification == "" {
		o.Classification = ClassUnknown
	}
	pq := 0
	if o.PostQuantum {
		pq = 1
	}
	auto := 0
	if o.AutoLearned {
		auto = 1
	}
	_, err := e.ExecContext(ctx, `INSERT INTO vayushield_signatures
(fingerprint_hash,ja3_hash,ja4_hash,http2_settings_hash,header_order_hash,user_agent_pattern,ip_range_hint,post_quantum_present,classification,bot_name,confidence,first_seen,last_seen,request_count,auto_learned)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,1,?)
ON CONFLICT(fingerprint_hash) DO UPDATE SET
last_seen=excluded.last_seen,
request_count=request_count+1,
ja4_hash=CASE WHEN vayushield_signatures.ja4_hash='' THEN excluded.ja4_hash ELSE vayushield_signatures.ja4_hash END,
confidence=CASE WHEN operator_verified=1 THEN confidence WHEN request_count+1>=? AND auto_learned=1 AND false_positive_count=0 THEN MIN(0.95, confidence+0.10) ELSE confidence END`,
		o.FingerprintHash, o.JA3, o.JA4, o.HTTP2SettingsHash, o.HeaderOrderHash, o.UserAgentPattern, o.IPRangeHint, pq,
		string(o.Classification), o.BotName, o.Confidence, now, now, auto, autoPromoteThreshold)
	return err
}

// Lookup returns the stored classification for a fingerprint, if known.
func (s *Store) Lookup(ctx context.Context, fingerprintHash string) (*StoredSignature, bool) {
	if s == nil || s.db == nil || fingerprintHash == "" {
		return nil, false
	}
	row := s.reader().QueryRowContext(ctx, selectCols+` WHERE fingerprint_hash=?`, fingerprintHash)
	sig, err := scanSignature(row)
	if err != nil {
		return nil, false
	}
	return sig, true
}

// ReportFalsePositive increments the false-positive counter for a fingerprint
// and, once several are reported, decays its confidence so a mistakenly-flagged
// real client stops being challenged.
func (s *Store) ReportFalsePositive(ctx context.Context, fingerprintHash string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE vayushield_signatures SET false_positive_count=false_positive_count+1, confidence=MAX(0.0, confidence-0.2) WHERE fingerprint_hash=?`,
		fingerprintHash)
	return err
}

// ReviewQueue returns auto-learned candidates awaiting operator verification,
// highest-confidence and most-frequent first.
func (s *Store) ReviewQueue(ctx context.Context, limit int) ([]StoredSignature, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.reader().QueryContext(ctx,
		selectCols+` WHERE operator_verified=0 AND auto_learned=1 ORDER BY confidence DESC, request_count DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSignatures(rows)
}

// Verify marks a candidate as operator-verified with a final classification,
// pinning its confidence high so it becomes an authoritative signature.
func (s *Store) Verify(ctx context.Context, id int64, class Classification, notes string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE vayushield_signatures SET operator_verified=1, classification=?, confidence=0.99, notes=? WHERE id=?`,
		string(class), notes, id)
	return err
}

// Dismiss deletes a candidate the operator judged not a bot.
func (s *Store) Dismiss(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM vayushield_signatures WHERE id=? AND operator_verified=0`, id)
	return err
}

// Stats summarizes the signature database for the dashboard.
type Stats struct {
	Total          int64            `json:"total"`
	ByClass        map[string]int64 `json:"by_class"`
	LearnedLast24h int64            `json:"learned_last_24h"`
	PendingReview  int64            `json:"pending_review"`
	Verified       int64            `json:"verified"`
}

// Stats computes counts by classification plus learned-in-last-24h.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	out := Stats{ByClass: map[string]int64{}}
	if err := s.reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM vayushield_signatures`).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT classification, COUNT(1) FROM vayushield_signatures GROUP BY classification`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		var n int64
		if err := rows.Scan(&c, &n); err != nil {
			return out, err
		}
		out.ByClass[c] = n
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	_ = s.reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM vayushield_signatures WHERE auto_learned=1 AND created_at>=?`, cutoff).Scan(&out.LearnedLast24h)
	_ = s.reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM vayushield_signatures WHERE operator_verified=0 AND auto_learned=1`).Scan(&out.PendingReview)
	_ = s.reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM vayushield_signatures WHERE operator_verified=1`).Scan(&out.Verified)
	return out, nil
}

// ── Community export / import ────────────────────────────────────────────────

// CommunityFile is the shareable signature export format. IP hints are stripped
// on export for privacy (they can encode an operator's network topology).
type CommunityFile struct {
	Format     string              `json:"format"`
	Version    int                 `json:"version"`
	ExportedAt time.Time           `json:"exported_at"`
	Signatures []CommunitySigEntry `json:"signatures"`
}

// CommunitySigEntry is one shareable signature (no IP data, no counts).
type CommunitySigEntry struct {
	FingerprintHash   string         `json:"fingerprint_hash"`
	JA3               string         `json:"ja3_hash,omitempty"`
	JA4               string         `json:"ja4_hash,omitempty"`
	HTTP2SettingsHash string         `json:"http2_settings_hash,omitempty"`
	HeaderOrderHash   string         `json:"header_order_hash,omitempty"`
	UserAgentPattern  string         `json:"user_agent_pattern,omitempty"`
	PostQuantum       bool           `json:"post_quantum_present"`
	Classification    Classification `json:"classification"`
	BotName           string         `json:"bot_name,omitempty"`
	Confidence        float64        `json:"confidence"`
}

// Export serializes operator-verified and high-confidence auto-learned
// signatures as a community file, with all IP data stripped.
func (s *Store) Export(ctx context.Context) ([]byte, error) {
	rows, err := s.reader().QueryContext(ctx,
		selectCols+` WHERE operator_verified=1 OR (auto_learned=1 AND confidence>=0.8) ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sigs, err := scanSignatures(rows)
	if err != nil {
		return nil, err
	}
	cf := CommunityFile{Format: "vayushield-signatures", Version: 1, ExportedAt: time.Now().UTC()}
	for _, sg := range sigs {
		cf.Signatures = append(cf.Signatures, CommunitySigEntry{
			FingerprintHash: sg.FingerprintHash, JA3: sg.JA3, JA4: sg.JA4,
			HTTP2SettingsHash: sg.HTTP2SettingsHash, HeaderOrderHash: sg.HeaderOrderHash,
			UserAgentPattern: sg.UserAgentPattern, PostQuantum: sg.PostQuantum,
			Classification: sg.Classification, BotName: sg.BotName, Confidence: sg.Confidence,
		})
	}
	return json.MarshalIndent(cf, "", "  ")
}

// Import merges a community signature file. Imported rows are marked auto_learned
// (not operator_verified) so the local operator still reviews them; existing
// operator-verified rows are never overwritten. Returns the number imported.
func (s *Store) Import(ctx context.Context, data []byte) (int, error) {
	var cf CommunityFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return 0, fmt.Errorf("parse community file: %w", err)
	}
	if cf.Format != "vayushield-signatures" {
		return 0, fmt.Errorf("unrecognized community file format %q", cf.Format)
	}
	now := time.Now().UTC()
	n := 0
	for _, e := range cf.Signatures {
		if e.FingerprintHash == "" {
			continue
		}
		pq := 0
		if e.PostQuantum {
			pq = 1
		}
		conf := e.Confidence
		if conf > 0.9 {
			conf = 0.9 // community signatures cap below local verification
		}
		res, err := s.db.ExecContext(ctx, `INSERT INTO vayushield_signatures
(fingerprint_hash,ja3_hash,ja4_hash,http2_settings_hash,header_order_hash,user_agent_pattern,post_quantum_present,classification,bot_name,confidence,first_seen,last_seen,request_count,auto_learned,notes)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,0,1,'imported from community')
ON CONFLICT(fingerprint_hash) DO UPDATE SET
ja4_hash=CASE WHEN vayushield_signatures.ja4_hash='' THEN excluded.ja4_hash ELSE vayushield_signatures.ja4_hash END,
confidence=CASE WHEN operator_verified=1 THEN confidence ELSE MAX(confidence, excluded.confidence) END`,
			e.FingerprintHash, e.JA3, e.JA4, e.HTTP2SettingsHash, e.HeaderOrderHash, e.UserAgentPattern, pq,
			string(e.Classification), e.BotName, conf, now, now)
		if err != nil {
			return n, err
		}
		if ra, _ := res.RowsAffected(); ra > 0 {
			n++
		}
	}
	return n, nil
}

// purgeChunk bounds how many rows a single maintenance DELETE removes, so it
// holds the single SQLite writer only briefly even when a swarm has left a huge
// backlog of one-off fingerprints. The rowid-subquery form works without the
// SQLITE_ENABLE_UPDATE_DELETE_LIMIT build tag (which mattn/go-sqlite3 omits).
const purgeChunk = 500

// PurgeStale deletes low-value auto-learned rows that have not recurred within
// retainDays and were never verified — bounding table growth. Chunked so each
// statement holds the writer briefly; index idx_signatures_prune keeps it from
// scanning the whole table.
func (s *Store) PurgeStale(ctx context.Context, retainDays int) (int64, error) {
	if s == nil || s.db == nil || retainDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retainDays)
	var total int64
	for {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM vayushield_signatures WHERE rowid IN (SELECT rowid FROM vayushield_signatures WHERE operator_verified=0 AND last_seen<? AND request_count<? LIMIT ?)`,
			cutoff, autoPromoteThreshold, purgeChunk)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < purgeChunk {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
	}
}

// ForgetAutoLearned deletes every AUTO-LEARNED signature, keeping the ones the
// operator explicitly verified. It is the recovery path for a poisoned database.
//
// Auto-learning records the fingerprint of anything that got blocked, and raises
// that record's confidence on every repeat until the scorer treats it as an
// identified bot. If the blocks were false positives — a threshold set too tight,
// or a CDN making a whole audience look like one address — the database ends up
// certain that a real browser is a bot, and every visitor using it is refused with
// no way to prove otherwise. The learner cannot unlearn that on its own, because
// each refusal looks like confirmation. This gives the operator a clean slate
// without discarding their own verified work. Returns the number removed.
func (s *Store) ForgetAutoLearned(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	var total int64
	for {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM vayushield_signatures WHERE rowid IN (SELECT rowid FROM vayushield_signatures WHERE operator_verified=0 LIMIT ?)`,
			purgeChunk)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < purgeChunk {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
	}
}

// ForgetAllLearned deletes EVERY learned signature, including ones marked
// operator-verified. It is the last-resort recovery when the database itself is
// the thing refusing real visitors.
//
// ForgetAutoLearned deliberately preserves verified rows, on the assumption that
// a human vetted them. But "verified" only means someone clicked Confirm on a
// review-queue candidate — and during a false-positive run those candidates ARE
// the operator's own readers, so confirming one makes a real browser permanently
// unappealable: it becomes an identified bad actor that no leniency applies to.
// This clears the slate completely. The compiled-in static signatures are
// untouched, so known-bad clients are still recognised. Returns the count.
func (s *Store) ForgetAllLearned(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	var total int64
	for {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM vayushield_signatures WHERE rowid IN (SELECT rowid FROM vayushield_signatures LIMIT ?)`,
			purgeChunk)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < purgeChunk {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
	}
}

// PurgeBlocked ages out old rows from vayushield_blocked. That table is a hashed,
// non-PII record of hard blocks kept only for aggregate counts (there is no live
// per-IP list any more, ADR-0137), so it must not grow forever. Chunked and
// index-driven (idx_blocked_created). Returns the number of rows removed.
func (s *Store) PurgeBlocked(ctx context.Context, retainDays int) (int64, error) {
	if s == nil || s.db == nil || retainDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retainDays)
	var total int64
	for {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM vayushield_blocked WHERE rowid IN (SELECT rowid FROM vayushield_blocked WHERE created_at<? LIMIT ?)`,
			cutoff, purgeChunk)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < purgeChunk {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
	}
}

// PurgeChallenges ages out old rows from vayushield_challenges — the one trail
// table the maintenance cycle forgot (audit): every issued challenge writes a
// row and nothing ever removed them, so a site under sustained probe traffic
// grew that table forever. The data is aggregate-only (hashed IP, coarse
// outcome), but unbounded growth is still a liability; the daily learning cycle
// now trims it on the same schedule as the block log. Chunked and index-driven
// (idx_challenges_created).
func (s *Store) PurgeChallenges(ctx context.Context, retainDays int) (int64, error) {
	if s == nil || s.db == nil || retainDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retainDays)
	var total int64
	for {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM vayushield_challenges WHERE rowid IN (SELECT rowid FROM vayushield_challenges WHERE created_at<? LIMIT ?)`,
			cutoff, purgeChunk)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < purgeChunk {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
	}
}

// ── scanning helpers ─────────────────────────────────────────────────────────

const selectCols = `SELECT id,fingerprint_hash,ja3_hash,ja4_hash,http2_settings_hash,header_order_hash,user_agent_pattern,ip_range_hint,post_quantum_present,classification,bot_name,confidence,request_count,false_positive_count,auto_learned,operator_verified,notes,first_seen,last_seen FROM vayushield_signatures`

type scanner interface {
	Scan(dest ...any) error
}

func scanSignature(sc scanner) (*StoredSignature, error) {
	var s StoredSignature
	var pq, auto, verified int
	if err := sc.Scan(&s.ID, &s.FingerprintHash, &s.JA3, &s.JA4, &s.HTTP2SettingsHash, &s.HeaderOrderHash,
		&s.UserAgentPattern, &s.IPRangeHint, &pq, &s.Classification, &s.BotName, &s.Confidence,
		&s.RequestCount, &s.FalsePositives, &auto, &verified, &s.Notes, &s.FirstSeen, &s.LastSeen); err != nil {
		return nil, err
	}
	s.PostQuantum = pq == 1
	s.AutoLearned = auto == 1
	s.OperatorVerified = verified == 1
	return &s, nil
}

func scanSignatures(rows *sql.Rows) ([]StoredSignature, error) {
	var out []StoredSignature
	for rows.Next() {
		sig, err := scanSignature(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sig)
	}
	return out, rows.Err()
}
