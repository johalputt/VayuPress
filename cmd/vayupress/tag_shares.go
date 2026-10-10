// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/tagrules"
)

// tag_shares.go holds how many published posts carry each tag, counted off the
// request path: which tags are on so much of the site they say nothing about a
// post (render.Ubiquitous, the rule Halcyon's desks and related reading use)
// and which of a post's tags are the rarest when it is sent too many. Counting reads every
// tag link (4.6 million on johal.in, the whole of idx_article_tags_live in
// order), so it runs every tagSharesEvery in the background and requests read
// the last count; before the first one nothing is broad and every tag is rare,
// which is how the site behaved before the rules.

const tagSharesEvery = 10 * time.Minute

type tagShareCounts struct {
	posts map[string]int // tag key → published posts carrying it
	total int            // published posts
}

var tagShares atomic.Pointer[tagShareCounts]

// tagFrequency is how many published posts carry key, from the last count.
func tagFrequency(key string) int {
	if c := tagShares.Load(); c != nil {
		return c.posts[key]
	}
	return 0
}

// broadTag reports whether key is ubiquitous (render.Ubiquitous) on the
// published posts. Such a tag stays on its posts; its page is kept out of
// search and related posts do not go through it.
func broadTag(key string) bool {
	c := tagShares.Load()
	return c != nil && render.Ubiquitous(c.posts[key], c.total)
}

// countTagShares counts once, makes the result the one requests read, and
// returns it.
func countTagShares(ctx context.Context) (*tagShareCounts, error) {
	if dbpkg.DB == nil {
		return nil, nil
	}
	c := &tagShareCounts{posts: map[string]int{}}
	rows, err := dbpkg.Reader().QueryContext(ctx, `SELECT tag_norm, COUNT(1) FROM article_tags WHERE live=1 GROUP BY tag_norm`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			_ = rows.Close()
			return nil, err
		}
		// Summed by key: links written before the tag rules carry the old
		// lower-cased tag_norm ("machine learning") until the stored-tags pass
		// rewrites them.
		if key := tagrules.Key(k); key != "" {
			c.posts[key] += n
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if err := dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM articles WHERE status='published' AND is_page=0`).Scan(&c.total); err != nil {
		return nil, err
	}
	tagShares.Store(c)
	return c, nil
}

// startTagShares counts a minute after boot and every tagSharesEvery after.
func startTagShares(done <-chan struct{}) {
	go func() {
		wait := time.Minute
		for {
			select {
			case <-done:
				return
			case <-time.After(wait):
			}
			wait = tagSharesEvery
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			if _, err := countTagShares(ctx); err != nil {
				logging.LogWarn("tags", "counting tag shares failed; the last count stays: "+err.Error())
			}
			cancel()
		}
	}()
}
