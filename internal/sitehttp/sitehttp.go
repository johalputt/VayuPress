// SPDX-License-Identifier: Apache-2.0

// Package sitehttp is what the server measured of each site: its public
// requests by the status they were answered with, and the time it took to
// answer its pages.
//
// A request is counted in memory as it is answered and written once a minute
// (site_http_minutes, site_http_latency; migrations 119 and 120), so the hot path takes
// a lock and adds to a counter, never a database write. A page's server time
// is the time from the request reaching the server to the server finishing
// its answer, kept as a histogram whose buckets are each about a tenth wider
// than the last: a percentile is read as the bucket it falls in, and said as
// "at most" that bucket's bound, which is within a tenth of the true value.
package sitehttp

import (
	"context"
	"database/sql"
	"sort"
	"sync"
	"time"
)

// AllSites asks a read for every site's rows together.
const AllSites = "\x00*all*"

// bounds are the buckets' upper bounds in milliseconds: one bucket for each
// millisecond below 10, then each about a tenth wider than the last, up to
// the 30 s past which a request is a stream, not a page.
var bounds = func() []int64 {
	b := make([]int64, 0, 100)
	for i := int64(0); i < 10; i++ {
		b = append(b, i)
	}
	for v := int64(10); v < 30000; {
		b = append(b, v)
		next := v + v/10
		if next == v {
			next++
		}
		v = next
	}
	return append(b, 30000)
}()

// bucket is the index of the bucket ms falls in.
func bucket(ms int64) int {
	if ms < 0 {
		ms = 0
	}
	i := sort.Search(len(bounds), func(i int) bool { return bounds[i] >= ms })
	if i == len(bounds) {
		i = len(bounds) - 1
	}
	return i
}

type counts struct {
	requests, s2xx, s3xx, s4xx, s404, s5xx, pages, pageMS int64
	cached, cachedMS, rendered, renderedMS                int64
}

// Cache is how the page cache took part in a page: it answered it, or the
// page was rendered because the cache did not hold it, or the cache had no
// part (a page it does not keep).
type Cache int

const (
	NoCache Cache = iota
	Cached
	Rendered
)

type minuteKey struct{ site, minute string }
type hourKey struct{ site, hour string }

// Recorder gathers requests in memory until Flush writes them.
type Recorder struct {
	mu      sync.Mutex
	minutes map[minuteKey]*counts
	latency map[hourKey]map[int]int64
}

// Requests is the process's recorder; the request logger adds to it.
var Requests = &Recorder{}

// Record counts one request answered for site with status; page marks one
// of its pages, served in d, and cache how the page cache took part in it.
func (r *Recorder) Record(site string, status int, page bool, cache Cache, d time.Duration, now time.Time) {
	now = now.UTC()
	mk := minuteKey{site, now.Format("2006-01-02 15:04") + ":00"}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.minutes == nil {
		r.minutes, r.latency = map[minuteKey]*counts{}, map[hourKey]map[int]int64{}
	}
	c := r.minutes[mk]
	if c == nil {
		c = &counts{}
		r.minutes[mk] = c
	}
	c.requests++
	switch {
	case status >= 500:
		c.s5xx++
	case status == 404:
		c.s404++
		c.s4xx++
	case status >= 400:
		c.s4xx++
	case status >= 300:
		c.s3xx++
	case status >= 200:
		c.s2xx++
	}
	if !page {
		return
	}
	ms := d.Milliseconds()
	c.pages++
	c.pageMS += ms
	switch cache {
	case Cached:
		c.cached++
		c.cachedMS += ms
	case Rendered:
		c.rendered++
		c.renderedMS += ms
	}
	hk := hourKey{site, now.Format("2006-01-02 15") + ":00:00"}
	h := r.latency[hk]
	if h == nil {
		h = map[int]int64{}
		r.latency[hk] = h
	}
	h[bucket(ms)]++
}

// take empties the recorder and returns what it held.
func (r *Recorder) take() (map[minuteKey]*counts, map[hourKey]map[int]int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, l := r.minutes, r.latency
	r.minutes, r.latency = nil, nil
	return m, l
}

// putBack returns counts a failed flush could not write, so the next one
// writes them: a database that is briefly busy loses no request.
func (r *Recorder) putBack(m map[minuteKey]*counts, l map[hourKey]map[int]int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.minutes == nil {
		r.minutes, r.latency = map[minuteKey]*counts{}, map[hourKey]map[int]int64{}
	}
	for k, c := range m {
		d := r.minutes[k]
		if d == nil {
			d = &counts{}
			r.minutes[k] = d
		}
		d.requests += c.requests
		d.s2xx += c.s2xx
		d.s3xx += c.s3xx
		d.s4xx += c.s4xx
		d.s404 += c.s404
		d.s5xx += c.s5xx
		d.pages += c.pages
		d.pageMS += c.pageMS
		d.cached += c.cached
		d.cachedMS += c.cachedMS
		d.rendered += c.rendered
		d.renderedMS += c.renderedMS
	}
	for k, h := range l {
		d := r.latency[k]
		if d == nil {
			d = map[int]int64{}
			r.latency[k] = d
		}
		for b, n := range h {
			d[b] += n
		}
	}
}

// Flush writes what the recorder holds to db, adding to the rows there.
func (r *Recorder) Flush(ctx context.Context, db *sql.DB) error {
	m, l := r.take()
	if len(m) == 0 && len(l) == 0 {
		return nil
	}
	if err := write(ctx, db, m, l); err != nil {
		r.putBack(m, l)
		return err
	}
	return nil
}

func write(ctx context.Context, db *sql.DB, m map[minuteKey]*counts, l map[hourKey]map[int]int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for k, c := range m {
		if _, err := tx.ExecContext(ctx, `INSERT INTO site_http_minutes(domain_id,minute,requests,s2xx,s3xx,s4xx,s404,s5xx,pages,page_ms,cached_pages,cached_ms,rendered_pages,rendered_ms)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(domain_id,minute) DO UPDATE SET requests=requests+excluded.requests, s2xx=s2xx+excluded.s2xx, s3xx=s3xx+excluded.s3xx,
			s4xx=s4xx+excluded.s4xx, s404=s404+excluded.s404, s5xx=s5xx+excluded.s5xx, pages=pages+excluded.pages, page_ms=page_ms+excluded.page_ms,
			cached_pages=cached_pages+excluded.cached_pages, cached_ms=cached_ms+excluded.cached_ms,
			rendered_pages=rendered_pages+excluded.rendered_pages, rendered_ms=rendered_ms+excluded.rendered_ms`,
			k.site, k.minute, c.requests, c.s2xx, c.s3xx, c.s4xx, c.s404, c.s5xx, c.pages, c.pageMS, c.cached, c.cachedMS, c.rendered, c.renderedMS); err != nil {
			return err
		}
	}
	for k, h := range l {
		for b, n := range h {
			if _, err := tx.ExecContext(ctx, `INSERT INTO site_http_latency(domain_id,hour,bucket,n) VALUES(?,?,?,?)
				ON CONFLICT(domain_id,hour,bucket) DO UPDATE SET n=n+excluded.n`, k.site, k.hour, b, n); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Start writes the recorder once a minute until done closes, then once more.
func (r *Recorder) Start(db *sql.DB, done <-chan struct{}, onError func(error)) {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-done:
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := r.Flush(ctx, db); err != nil && onError != nil {
					onError(err)
				}
				cancel()
				return
			case <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				if err := r.Flush(ctx, db); err != nil && onError != nil {
					onError(err)
				}
				cancel()
			}
		}
	}()
}

// Purge deletes the rows older than days, as the analytics purge does.
func Purge(ctx context.Context, db *sql.DB, days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	cut := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02") + " 00:00:00"
	var total int64
	for _, q := range []string{`DELETE FROM site_http_minutes WHERE minute<?`, `DELETE FROM site_http_latency WHERE hour<?`} {
		res, err := db.ExecContext(ctx, q, cut)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}
