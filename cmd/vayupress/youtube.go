// SPDX-License-Identifier: Apache-2.0

package main

// youtube.go — a YouTube channel as a blog: the fetcher, the post maker and
// the loop that checks followed channels (internal/youtube holds the rules;
// admin_os_youtube.go is the page).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/blockrender"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/youtube"
)

// The fetchers, with their caps: a feed is a few dozen kilobytes and a
// channel page a megabyte or two; a cover is a JPEG of under half a
// megabyte. Variables so a test can answer for YouTube.
var (
	youtubeFetchText  = youtubeGet(safefetch.New(safefetch.Options{MaxBytes: 4 << 20, Timeout: 20 * time.Second, AllowedSchemes: []string{"https"}}).Get)
	youtubeFetchImage = youtubeGet(safefetch.New(safefetch.Options{MaxBytes: 8 << 20, Timeout: 20 * time.Second, AllowedSchemes: []string{"https"}}).Get)
)

// errNotYouTubeHost is a fetch refused before it was made, or an answer that
// came from a host it was not asked of.
var errNotYouTubeHost = errors.New("refused: not one of YouTube's hosts")

// youtubeGet is the one way this feature fetches: only an address
// youtube.Fetchable allows, and only an answer that ended on one, so a
// redirect cannot carry a request anywhere else.
func youtubeGet(get func(context.Context, string) (*safefetch.Result, error)) youtube.Getter {
	return func(ctx context.Context, u string) (int, []byte, error) {
		if !youtube.Fetchable(u) {
			return 0, nil, errNotYouTubeHost
		}
		res, err := get(ctx, u)
		if err != nil {
			return 0, nil, err
		}
		if !youtube.Fetchable(res.FinalURL) {
			return 0, nil, errNotYouTubeHost
		}
		return res.Status, res.Body, nil
	}
}

// youtubeFollower is the follower over the install's store, fetcher, sealed
// key and posts.
func (a *App) youtubeFollower() *youtube.Follower {
	return &youtube.Follower{
		Store:  a.youtube,
		Get:    youtubeFetchText,
		Key:    a.youtubeKey,
		Poster: youtubePoster{a: a, get: youtubeFetchImage},
	}
}

// youtubeKey is the operator's YouTube Data API key from the sealed
// credential store, or "".
func (a *App) youtubeKey(ctx context.Context) string {
	if a.secrets == nil {
		return ""
	}
	k, _ := a.secrets.ProviderSecret(ctx, secrets.ProviderYouTube)
	return k
}

// youtubeAvailable says whether this install can follow channels at all: not
// in a Tor world, whose egress is Tor's, and not while clearnet egress is
// blocked.
func (a *App) youtubeAvailable() bool {
	return a.youtube != nil && !config.Cfg.OnionMode && !safefetch.ClearnetBlocked()
}

// youtubePoster makes posts through the same paths as the editor: the post
// queue, the media library, the publish step with its cache purge and
// IndexNow ping.
type youtubePoster struct {
	a   *App
	get youtube.Getter
}

// cover copies a video's picture into the media library, largest first.
func (p youtubePoster) cover(ctx context.Context, videoID string) string {
	for _, u := range youtube.ThumbURLs(videoID) {
		status, body, err := p.get(ctx, u)
		if err != nil || status != 200 {
			continue
		}
		if stored, err := storeValidatedMedia(body, false); err == nil {
			return stored.URL
		}
	}
	return ""
}

func (p youtubePoster) Post(ctx context.Context, domainID string, v youtube.Video, draft bool) (string, error) {
	a := p.a
	cover := p.cover(ctx, v.ID)
	content, excerpt, blocks, err := youtubeRender(v, cover)
	if err != nil {
		return "", err
	}
	tags := append(youtube.Hashtags(v.Description), youtubeKindTag(v.Kind))
	title := v.Title
	if strings.TrimSpace(title) == "" {
		title = "Video " + v.ID
	}
	slug := a.uniqueArticleSlug(ctx, title)
	if _, err := a.articles.CreateDraft(ctx, title, slug, content, tags); err != nil {
		return "", err
	}
	if err := awaitArticle(ctx, slug); err != nil {
		return "", err
	}
	if domainID != "" {
		if err := a.articles.SetDomain(ctx, slug, domainID); err != nil {
			return "", err
		}
	}
	when := v.Published
	if when.IsZero() {
		when = time.Now().UTC()
	}
	if _, err := dbpkg.WDB.Exec(`UPDATE articles SET blocks_json=?, feature_image=?, canonical_url=?, excerpt=?, created_at=? WHERE slug=?`,
		blocks, cover, youtubeLink(v), excerpt, when.UTC(), slug); err != nil {
		return "", err
	}
	if draft {
		render.CachePurge(slug, tags, generateSitemap, generateRSS, generateRobots)
		return slug, nil
	}
	return slug, a.applyPostStatus(ctx, slug, "published")
}

// Refresh rewrites a post made ahead of a premiere or stream as the video it
// became: its player, description and date. Its title, tags and status stay
// as the operator may have left them.
func (p youtubePoster) Refresh(ctx context.Context, slug string, v youtube.Video) error {
	var cover string
	var tagsCSV string
	if err := dbpkg.Reader().QueryRowContext(ctx, `SELECT feature_image, COALESCE(tags,'') FROM articles WHERE slug=?`, slug).Scan(&cover, &tagsCSV); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // the operator deleted it
		}
		return err
	}
	content, excerpt, blocks, err := youtubeRender(v, cover)
	if err != nil {
		return err
	}
	if _, err := dbpkg.WDB.Exec(`UPDATE articles SET content=?, blocks_json=?, excerpt=?, created_at=COALESCE(?, created_at), updated_at=? WHERE slug=?`,
		content, blocks, excerpt, nullableTime(v.Published), time.Now().UTC(), slug); err != nil {
		return err
	}
	render.CachePurge(slug, splitCSVTags(tagsCSV), generateSitemap, generateRSS, generateRobots)
	return nil
}

// youtubeRender is a video's post: its HTML, excerpt and block document.
func youtubeRender(v youtube.Video, cover string) (content, excerpt, blocks string, err error) {
	b, err := json.Marshal(youtube.Blocks(v, cover))
	if err != nil {
		return "", "", "", err
	}
	content, excerpt, err = blockrender.Render(string(b))
	return content, excerpt, string(b), err
}

func youtubeKindTag(kind string) string {
	if kind == youtube.KindShort {
		return "shorts"
	}
	return "videos"
}

func youtubeLink(v youtube.Video) string {
	if v.Kind == youtube.KindShort {
		return youtube.ShortURL(v.ID)
	}
	return youtube.WatchURL(v.ID)
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}

// awaitArticle waits for the post queue to write a post's row, which it does
// within a tick of a tenth of a second; the follow-up writes need the row.
func awaitArticle(ctx context.Context, slug string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		var one int
		if err := dbpkg.Reader().QueryRowContext(ctx, `SELECT 1 FROM articles WHERE slug=?`, slug).Scan(&one); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("the post queue did not write the post in time")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// youtubeLoopEvery is how often the loop looks for channels whose check is
// due; Check now and a new channel wake it at once.
const youtubeLoopEvery = time.Minute

// runYouTube checks followed channels as they come due, one at a time, until
// ctx ends.
func (a *App) runYouTube(ctx context.Context) {
	t := time.NewTicker(youtubeLoopEvery)
	defer t.Stop()
	for {
		a.youtubeRound(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-a.youtubePoke:
		}
	}
}

// youtubeRound checks every channel now due.
func (a *App) youtubeRound(ctx context.Context) {
	if !a.youtubeAvailable() {
		return
	}
	due, err := a.youtube.Due(ctx, 20)
	if err != nil {
		logging.LogError("youtube", "listing due channels failed", err.Error())
		return
	}
	fw := a.youtubeFollower()
	for _, f := range due {
		if ctx.Err() != nil {
			return
		}
		if n, err := fw.Check(ctx, f); err != nil {
			logging.LogError("youtube", "checking "+f.ChannelID+" failed", err.Error())
		} else if n > 0 {
			logging.LogInfo("youtube", f.ChannelID+": "+strconv.Itoa(n)+" new")
		}
	}
}

// pokeYouTube wakes the loop without waiting for its tick.
func (a *App) pokeYouTube() {
	select {
	case a.youtubePoke <- struct{}{}:
	default:
	}
}
