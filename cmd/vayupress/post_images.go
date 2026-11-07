// SPDX-License-Identifier: Apache-2.0

package main

// post_images.go — card images from Pixabay. With the Pixabay key on API
// Keys and the switch on Tools and plugins, a background job gives each
// published post that has no card image one: the Pixabay picture its own
// HTML carries, while that address still answers, or else the first photo
// Pixabay finds for its title. The picture is copied into the media library
// (storeValidatedMedia), never linked: Pixabay's addresses are not for
// hotlinking, and readers then fetch only from the site.
//
// The job never runs on a request. It takes the host's pace (internal/pace),
// spaces its searches inside Pixabay's limit, and tries each post once: the
// tries are recorded with the post's date, and each pass goes on only from
// the newest and the oldest post tried, so no post's body is read twice.

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/pace"
	"github.com/johalputt/vayupress/internal/pixabay"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/settings"
)

const (
	// postImageSpacing keeps searches inside Pixabay's 100 requests a minute
	// with room for anything else the key is used for.
	postImageSpacing = 2 * time.Second
	// postImagesEvery is how often a pass starts; a pass ends when it runs
	// out of posts, so new posts get an image within this long.
	postImagesEvery = 10 * time.Minute
)

// Fetches for this feature: only Pixabay's own https addresses, before and
// after redirects. Variables so a test can answer for Pixabay.
var (
	pixabayFetchText  = pixabayGet(safefetch.New(safefetch.Options{MaxBytes: 1 << 20, Timeout: 20 * time.Second, AllowedSchemes: []string{"https"}}).Get)
	pixabayFetchImage = pixabayGet(safefetch.New(safefetch.Options{MaxBytes: 8 << 20, Timeout: 30 * time.Second, AllowedSchemes: []string{"https"}}).Get)
)

var errNotPixabay = errors.New("refused: not one of Pixabay's addresses")

func pixabayGet(get func(context.Context, string) (*safefetch.Result, error)) pixabay.Getter {
	return func(ctx context.Context, u string) (int, []byte, error) {
		if !pixabay.Fetchable(u) {
			return 0, nil, errNotPixabay
		}
		res, err := get(ctx, u)
		if err != nil {
			return 0, nil, err
		}
		if !pixabay.Fetchable(res.FinalURL) {
			return 0, nil, errNotPixabay
		}
		return res.Status, res.Body, nil
	}
}

// postImagesState is what the job has done since the server started, for
// the Tools page.
var postImagesState struct {
	mu            sync.Mutex
	given, missed int
	lastErr       string
}

// pixabayKey is the operator's sealed Pixabay key, or "".
func (a *App) pixabayKey(ctx context.Context) string {
	if a.secrets == nil {
		return ""
	}
	k, _ := a.secrets.ProviderSecret(ctx, secrets.ProviderPixabay)
	return k
}

// postImagesOn reports whether the switch is on.
func (a *App) postImagesOn(ctx context.Context) bool {
	return a.siteSettings != nil && a.siteSettings.FeatureEnabled(ctx, settings.ForPrimary(), settings.KeyFeaturePostImages)
}

// startPostImages runs a pass every postImagesEvery while the switch is on,
// the key is set and clearnet is reachable.
func (a *App) startPostImages(ctx context.Context) {
	go func() {
		t := time.NewTicker(postImagesEvery)
		defer t.Stop()
		for {
			if a.postImagesOn(ctx) && a.pixabayKey(ctx) != "" && !safefetch.ClearnetBlocked() {
				a.postImagesPass(ctx, pace.Host().NewJob(pace.JobConfig{Min: 1, Max: 10, Step: 1, Floor: true}), postImageSpacing)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// postImageCandidate is one post without a card image. created is its date
// as the database holds it, so the tries' bounds compare with created_at as
// stored, never through a reformatted time.
type postImageCandidate struct {
	slug, title, content, created string
}

// postImagesPass gives card images to the posts not yet tried, newest first
// above the newest tried, then below the oldest tried, in batches the host
// allows, and stops when none are left or the switch goes off.
func (a *App) postImagesPass(ctx context.Context, job *pace.Job, spacing time.Duration) {
	for {
		n, err := job.Next(ctx)
		if err != nil {
			return
		}
		batch := postImageCandidates(ctx, n)
		if len(batch) == 0 {
			return
		}
		given := 0
		for _, c := range batch {
			if !a.postImagesOn(ctx) {
				return
			}
			key := a.pixabayKey(ctx)
			if key == "" {
				return
			}
			gave, recorded := a.givePostImage(ctx, key, c)
			if gave {
				given++
			}
			if !recorded {
				// A try that is not recorded would be the next batch's first
				// post again, asked of Pixabay every few seconds for ever: the
				// pass ends, and the next one tries again.
				if given > 0 {
					render.CachePurgeListings()
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(spacing):
			}
		}
		if given > 0 {
			render.CachePurgeListings()
		}
	}
}

// postImageNewerSQL and postImageOlderSQL read the posts with no card image
// above and below a bound, newest first: ranges of idx_articles_pagefeed.
const (
	postImageNewerSQL = `SELECT slug, title, content, CAST(created_at AS TEXT) FROM articles
		WHERE is_page = 0 AND status = 'published' AND feature_image = '' AND created_at > ?
		ORDER BY created_at DESC LIMIT ?`
	postImageOlderSQL = `SELECT slug, title, content, CAST(created_at AS TEXT) FROM articles
		WHERE is_page = 0 AND status = 'published' AND feature_image = '' AND created_at < ?
		ORDER BY created_at DESC LIMIT ?`
)

// postImageCandidates returns up to n posts not yet tried: those newer than
// the newest tried, newest first, then those older than the oldest tried.
func postImageCandidates(ctx context.Context, n int) []postImageCandidate {
	var newest, oldest string
	_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COALESCE(MAX(post_created),''), COALESCE(MIN(post_created),'') FROM post_image_tries`).Scan(&newest, &oldest)
	var out []postImageCandidate
	read := func(query, bound string) {
		if len(out) >= n {
			return
		}
		rows, err := dbpkg.Reader().QueryContext(ctx, query, bound, n-len(out))
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var c postImageCandidate
			if rows.Scan(&c.slug, &c.title, &c.content, &c.created) == nil {
				out = append(out, c)
			}
		}
		_ = rows.Err()
	}
	if newest == "" {
		// Nothing tried yet: every post, from the newest.
		read(postImageOlderSQL, "\uffff")
		return out
	}
	read(postImageNewerSQL, newest)
	read(postImageOlderSQL, oldest)
	return out
}

// givePostImage finds, copies and sets c's card image, records the try, and
// reports whether it gave one and whether the try was recorded.
func (a *App) givePostImage(ctx context.Context, key string, c postImageCandidate) (gave, recorded bool) {
	media, why := a.findPostImage(ctx, key, c)
	if media != "" {
		res, err := dbpkg.WDB.ExecContext(ctx, `UPDATE articles SET feature_image = ? WHERE slug = ? AND feature_image = ''`, media, c.slug)
		if err != nil {
			media, why = "", "not saved: "+err.Error()
		} else if n, _ := res.RowsAffected(); n == 0 {
			media, why = "", "given an image meanwhile"
		} else {
			render.CachePurgePost(c.slug)
		}
	}
	result := media
	if media == "" {
		result = why
	}
	_, err := dbpkg.WDB.ExecContext(ctx, `INSERT INTO post_image_tries(slug, post_created, tried_at, result) VALUES(?,?,?,?)
		ON CONFLICT(slug) DO UPDATE SET tried_at = excluded.tried_at, result = excluded.result`,
		c.slug, c.created, time.Now().UTC(), result)
	recorded = err == nil
	postImagesState.mu.Lock()
	defer postImagesState.mu.Unlock()
	if !recorded {
		postImagesState.lastErr = "the try was not recorded: " + err.Error()
		logging.LogError("post-images", "try not recorded for "+c.slug, err.Error())
	}
	if media != "" {
		postImagesState.given++
		return true, recorded
	}
	postImagesState.missed++
	if why != "no photo found" {
		postImagesState.lastErr = why
		logging.LogError("post-images", "no card image for "+c.slug, why)
	}
	return false, recorded
}

// findPostImage returns the media URL of c's picture once copied, or why
// there is none: the post's own Pixabay picture first, then a search.
func (a *App) findPostImage(ctx context.Context, key string, c postImageCandidate) (string, string) {
	if link := pixabay.LinkIn(c.content); link != "" {
		if media, err := copyPixabayImage(ctx, link); err == nil {
			return media, ""
		}
	}
	hit, ok, err := pixabay.Search(ctx, pixabayFetchText, key, pixabay.Query(c.title))
	if err != nil {
		return "", err.Error()
	}
	if !ok {
		return "", "no photo found"
	}
	media, err := copyPixabayImage(ctx, hit.Image)
	if err != nil {
		return "", err.Error()
	}
	return media, ""
}

// copyPixabayImage fetches a Pixabay image and stores it in the media
// library, which checks it is an image.
func copyPixabayImage(ctx context.Context, link string) (string, error) {
	status, body, err := pixabayFetchImage(ctx, link)
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", errors.New("pixabay: image answered " + strconv.Itoa(status))
	}
	stored, err := storeValidatedMedia(body, false)
	if err != nil {
		return "", err
	}
	return stored.URL, nil
}

// postImagesSummary is the Tools row's line: what the job has done since
// the server started, or what it is waiting for.
func (a *App) postImagesSummary(ctx context.Context) string {
	const what = "Posts with no card image get one from Pixabay in the background: the picture a post already links to, or the first photo found for its title, copied into the media library."
	if a.pixabayKey(ctx) == "" {
		return what + " Needs the Pixabay key on API Keys."
	}
	postImagesState.mu.Lock()
	defer postImagesState.mu.Unlock()
	line := what + " Since the server started: " + strconv.Itoa(postImagesState.given) + " given, " + strconv.Itoa(postImagesState.missed) + " without."
	if postImagesState.lastErr != "" {
		line += " Last failure: " + postImagesState.lastErr + "."
	}
	return line
}
