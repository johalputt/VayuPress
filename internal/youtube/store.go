// SPDX-License-Identifier: Apache-2.0

package youtube

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"time"
)

// Store holds the channels an install follows and every video they have made
// a post of or found (migration 129).
type Store struct {
	w, r *sql.DB
	now  func() time.Time
}

// NewStore is the store over the install's writer and reader, with a clock.
func NewStore(w, r *sql.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{w: w, r: r, now: now}
}

// Followed is one channel an install follows, and how.
type Followed struct {
	ID        string
	DomainID  string // "" for the primary site
	ChannelID string
	Title     string
	AutoPost  bool // new videos become posts by themselves
	AsDraft   bool // a post is kept as a draft rather than published
	Shorts    bool // Shorts become posts too
	Paused    bool // not checked at all
	Created   time.Time
	LastCheck time.Time
	NextCheck time.Time
	LastFound int    // new videos the last check found
	LastError string // why the last check failed, or ""
	Failures  int    // checks failed in a row
	// The back catalogue, read through the API with a key.
	BackfillToken string
	BackfillDone  bool
	BackfillSeen  int
	BackfillTotal int
}

// Video states.
const (
	StateClaimed  = "claimed"  // a post is being made of it now
	StatePosted   = "posted"   // its post exists, or did
	StateHeld     = "held"     // found while auto-post was off: waits for Post
	StateUpcoming = "upcoming" // posted ahead of its premiere or stream
	StateSkipped  = "skipped"  // a Short, while Shorts are off
)

// Checking runs at most every CheckEvery per channel, the feed's own
// freshness; Backoff is the wait after one, two, three and more failures.
const CheckEvery = 15 * time.Minute

var Backoff = []time.Duration{15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}

// ErrFollowed is returned for a channel the site already follows.
var ErrFollowed = errors.New("this site already follows that channel")

const channelCols = `id,domain_id,channel_id,title,auto_post,as_draft,shorts,paused,created_at,last_check,next_check,last_found,last_error,failures,backfill_token,backfill_done,backfill_seen,backfill_total`

func scanFollowed(sc interface{ Scan(...any) error }) (Followed, error) {
	var f Followed
	var last sql.NullTime
	err := sc.Scan(&f.ID, &f.DomainID, &f.ChannelID, &f.Title, &f.AutoPost, &f.AsDraft, &f.Shorts, &f.Paused,
		&f.Created, &last, &f.NextCheck, &f.LastFound, &f.LastError, &f.Failures,
		&f.BackfillToken, &f.BackfillDone, &f.BackfillSeen, &f.BackfillTotal)
	f.LastCheck = last.Time
	return f, err
}

// Follow starts following a channel on a site, due for its first check now.
func (s *Store) Follow(ctx context.Context, domainID string, ch Channel, autoPost, asDraft, shorts bool) (Followed, error) {
	if !ValidChannelID(ch.ID) {
		return Followed{}, ErrUnrecognised
	}
	now := s.now().UTC()
	f := Followed{ID: newID(), DomainID: domainID, ChannelID: ch.ID, Title: ch.Title, AutoPost: autoPost, AsDraft: asDraft, Shorts: shorts, Created: now, NextCheck: now}
	_, err := s.w.ExecContext(ctx, `INSERT INTO youtube_channels(id,domain_id,channel_id,title,auto_post,as_draft,shorts,created_at,next_check) VALUES(?,?,?,?,?,?,?,?,?)`,
		f.ID, f.DomainID, f.ChannelID, f.Title, f.AutoPost, f.AsDraft, f.Shorts, f.Created, f.NextCheck)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return Followed{}, ErrFollowed
	}
	return f, err
}

// Channels is every followed channel, newest first.
func (s *Store) Channels(ctx context.Context) ([]Followed, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+channelCols+` FROM youtube_channels ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Followed
	for rows.Next() {
		f, err := scanFollowed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Channel is one followed channel by its row id.
func (s *Store) Channel(ctx context.Context, id string) (Followed, error) {
	return scanFollowed(s.r.QueryRowContext(ctx, `SELECT `+channelCols+` FROM youtube_channels WHERE id=?`, id))
}

// Due is the channels whose next check has come, most overdue first.
func (s *Store) Due(ctx context.Context, limit int) ([]Followed, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+channelCols+` FROM youtube_channels WHERE paused=0 AND next_check<=? ORDER BY next_check LIMIT ?`, s.now().UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Followed
	for rows.Next() {
		f, err := scanFollowed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Configure sets how a channel is followed.
func (s *Store) Configure(ctx context.Context, id string, autoPost, asDraft, shorts, paused bool) error {
	_, err := s.w.ExecContext(ctx, `UPDATE youtube_channels SET auto_post=?,as_draft=?,shorts=?,paused=? WHERE id=?`, autoPost, asDraft, shorts, paused, id)
	return err
}

// CheckNow makes a channel due at once.
func (s *Store) CheckNow(ctx context.Context, id string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE youtube_channels SET next_check=? WHERE id=?`, s.now().UTC(), id)
	return err
}

// Unfollow stops following a channel. Its videos' rows stay, so following it
// again never makes a second post of a video.
func (s *Store) Unfollow(ctx context.Context, id string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM youtube_channels WHERE id=?`, id)
	return err
}

// Checked records a check: what it found, and when the next one is. A failed
// check waits longer each time it fails again in a row.
func (s *Store) Checked(ctx context.Context, f Followed, found int, failure error) error {
	now := s.now().UTC()
	if failure == nil {
		next := now.Add(CheckEvery + jitter())
		_, err := s.w.ExecContext(ctx, `UPDATE youtube_channels SET last_check=?,next_check=?,last_found=?,last_error='',failures=0 WHERE id=?`, now, next, found, f.ID)
		return err
	}
	n := f.Failures + 1
	wait := Backoff[min(n, len(Backoff))-1]
	msg := failure.Error()
	if len(msg) > 300 {
		msg = msg[:300]
	}
	_, err := s.w.ExecContext(ctx, `UPDATE youtube_channels SET last_check=?,next_check=?,last_error=?,failures=? WHERE id=?`, now, now.Add(wait), msg, n, f.ID)
	return err
}

// SetTitle keeps a channel's name as its feed says it now.
func (s *Store) SetTitle(ctx context.Context, id, title string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE youtube_channels SET title=? WHERE id=? AND title<>?`, title, id, title)
	return err
}

// Backfilled records how far the back catalogue has been read.
func (s *Store) Backfilled(ctx context.Context, id, token string, seen, total int, done bool) error {
	_, err := s.w.ExecContext(ctx, `UPDATE youtube_channels SET backfill_token=?,backfill_seen=?,backfill_total=?,backfill_done=? WHERE id=?`, token, seen, total, done, id)
	return err
}

// Known is which of ids a site already has a row for, in any state.
func (s *Store) Known(ctx context.Context, domainID string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{domainID}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.r.QueryContext(ctx, `SELECT video_id FROM youtube_videos WHERE domain_id=? AND video_id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Claim records a video for a site in a state, and reports whether this call
// made the row. Only the caller that made it goes on to make a post, so a
// video is never posted twice, by two checks at once or by a later one.
func (s *Store) Claim(ctx context.Context, domainID, channelRow string, v Video, state string) (bool, error) {
	res, err := s.w.ExecContext(ctx, `INSERT OR IGNORE INTO youtube_videos(domain_id,video_id,channel,kind,title,published_at,start_at,state,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		domainID, v.ID, channelRow, v.Kind, v.Title, nullTime(v.Published), nullTime(v.Start), state, s.now().UTC())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Settle records what became of a claimed or held video.
func (s *Store) Settle(ctx context.Context, domainID, videoID, slug, state string) error {
	_, err := s.w.ExecContext(ctx, `UPDATE youtube_videos SET slug=?,state=? WHERE domain_id=? AND video_id=?`, slug, state, domainID, videoID)
	return err
}

// Release forgets a claim whose post could not be made, so the next check
// tries again.
func (s *Store) Release(ctx context.Context, domainID, videoID string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM youtube_videos WHERE domain_id=? AND video_id=? AND state=?`, domainID, videoID, StateClaimed)
	return err
}

// Row is a video as the store has it.
type Row struct {
	VideoID   string
	Kind      string
	Title     string
	Published time.Time
	Start     time.Time
	Slug      string
	State     string
}

// Videos is a channel's videos in a state, newest first, at most limit.
func (s *Store) Videos(ctx context.Context, channelRow, state string, limit int) ([]Row, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT video_id,kind,title,published_at,start_at,slug,state FROM youtube_videos WHERE channel=? AND state=? ORDER BY published_at DESC LIMIT ?`, channelRow, state, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var pub, start sql.NullTime
		if err := rows.Scan(&r.VideoID, &r.Kind, &r.Title, &pub, &start, &r.Slug, &r.State); err != nil {
			return nil, err
		}
		r.Published, r.Start = pub.Time, start.Time
		out = append(out, r)
	}
	return out, rows.Err()
}

// Video is one site's row for a video.
func (s *Store) Video(ctx context.Context, domainID, videoID string) (Row, error) {
	var r Row
	var pub, start sql.NullTime
	err := s.r.QueryRowContext(ctx, `SELECT video_id,kind,title,published_at,start_at,slug,state FROM youtube_videos WHERE domain_id=? AND video_id=?`, domainID, videoID).
		Scan(&r.VideoID, &r.Kind, &r.Title, &pub, &start, &r.Slug, &r.State)
	r.Published, r.Start = pub.Time, start.Time
	return r, err
}

// Counts is how many of a channel's videos are in each state.
func (s *Store) Counts(ctx context.Context, channelRow string) (map[string]int, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT state, COUNT(1) FROM youtube_videos WHERE channel=? GROUP BY state`, channelRow)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// Started is the site's upcoming videos whose start has passed, to be
// rewritten as the recording.
func (s *Store) Started(ctx context.Context, domainID string) ([]Row, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT video_id,kind,title,published_at,start_at,slug,state FROM youtube_videos WHERE domain_id=? AND state=? AND start_at<=?`, domainID, StateUpcoming, s.now().UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var pub, start sql.NullTime
		if err := rows.Scan(&r.VideoID, &r.Kind, &r.Title, &pub, &start, &r.Slug, &r.State); err != nil {
			return nil, err
		}
		r.Published, r.Start = pub.Time, start.Time
		out = append(out, r)
	}
	return out, rows.Err()
}

// Failing is how many followed channels have failed three checks or more in
// a row: the bell's one notice.
func (s *Store) Failing(ctx context.Context) int {
	var n int
	_ = s.r.QueryRowContext(ctx, `SELECT COUNT(1) FROM youtube_channels WHERE paused=0 AND failures>=3`).Scan(&n)
	return n
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// jitter spreads channels over three minutes, so a hundred followed at once
// are not all checked in the same second.
func jitter() time.Duration {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(3*time.Minute)))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}
