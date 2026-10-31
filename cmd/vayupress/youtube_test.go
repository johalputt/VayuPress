// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/youtube"
)

// nowQueue writes a post at once through the real article repository, where
// the post queue writes it a tenth of a second later: the importer's own
// steps are what is under test.
type nowQueue struct {
	repo interface {
		Create(context.Context, dbpkg.Article) error
	}
}

func (q nowQueue) Enqueue(ctx context.Context, art dbpkg.Article, _ string) error {
	return q.repo.Create(ctx, art)
}

const ytChannel = "UCabcdefghijklmnopqrstuv"

func youtubeTestApp(t *testing.T) *App {
	t.Helper()
	openMigratedDB(t)
	prev := config.Cfg.MediaDir
	config.Cfg.MediaDir = filepath.Join(t.TempDir(), "media")
	t.Cleanup(func() { config.Cfg.MediaDir = prev })
	repo := dbpkg.NewArticleRepo(dbpkg.DB)
	a := &App{articles: &api.ArticleService{Repo: repo, Queue: nowQueue{repo}}}
	a.youtube = youtube.NewStore(dbpkg.DB, dbpkg.Reader(), nil)
	a.youtubePoke = make(chan struct{}, 1)
	// Publishing pings IndexNow and rebuilds the feeds off the request; both
	// end before the next test changes the config they read.
	t.Cleanup(func() { a.bgWG.Wait(); render.WaitForPurges() })
	return a
}

func aJPEG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 9)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// A video's post is made on its own site, published, with its picture in the
// media library, its date, its tags, a link back, and the player.
func TestAVideoBecomesAPostOnItsSite(t *testing.T) {
	a := youtubeTestApp(t)
	pic := aJPEG(t)
	var asked []string
	p := youtubePoster{a: a, get: func(_ context.Context, u string) (int, []byte, error) {
		asked = append(asked, u)
		if strings.HasSuffix(u, "/hqdefault.jpg") {
			return 200, pic, nil
		}
		return 404, nil, nil
	}}
	when := time.Date(2026, 10, 29, 9, 30, 0, 0, time.UTC)
	v := youtube.Video{ID: "dQw4w9WgXcQ", ChannelID: ytChannel, Title: "A Short about Go", Kind: youtube.KindShort, Published: when,
		Description: "Learn #Go in a minute. https://go.dev/doc"}
	slug, err := p.Post(context.Background(), "site-b", v, false)
	if err != nil {
		t.Fatal(err)
	}
	var domainID, status, cover, canonical, tags, content, blocks string
	var created time.Time
	if err := dbpkg.DB.QueryRow(`SELECT domain_id,status,feature_image,canonical_url,tags,content,blocks_json,created_at FROM articles WHERE slug=?`, slug).
		Scan(&domainID, &status, &cover, &canonical, &tags, &content, &blocks, &created); err != nil {
		t.Fatal(err)
	}
	if domainID != "site-b" {
		t.Errorf("the post is on site %q; want site-b", domainID)
	}
	if status != "published" {
		t.Errorf("status %q; want published", status)
	}
	if !strings.HasPrefix(cover, "/media/") {
		t.Errorf("cover %q is not in the media library", cover)
	}
	if canonical != "https://www.youtube.com/shorts/dQw4w9WgXcQ" {
		t.Errorf("canonical %q", canonical)
	}
	if !created.Equal(when) {
		t.Errorf("dated %s; want the video's %s", created, when)
	}
	if tags != "go,shorts" {
		t.Errorf("tags %q", tags)
	}
	for _, want := range []string{`video-facade video-facade--tall`, `data-embed-src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"`, `href="https://go.dev/doc"`, cover} {
		if !strings.Contains(content, want) {
			t.Errorf("the post lacks %s:\n%s", want, content)
		}
	}
	if !strings.Contains(blocks, `"embedSrc"`) {
		t.Errorf("the post cannot be opened in the editor: blocks %q", blocks)
	}
	if len(asked) != 3 {
		t.Errorf("asked for %v; want the three sizes, largest first", asked)
	}

	// Kept as a draft when the channel says so.
	v.ID, v.Title = "aaaaaaaaaaa", "A draft"
	slug, err = p.Post(context.Background(), "", v, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := dbpkg.DB.QueryRow(`SELECT status FROM articles WHERE slug=?`, slug).Scan(&status); err != nil || status != "draft" {
		t.Errorf("a draft channel's post is %q, %v", status, err)
	}
}

// What is fetched is only what youtube.Fetchable allows, and an answer that
// ended anywhere else is refused, so a redirect cannot carry a request off.
func TestTheFetcherKeepsToYouTubesHosts(t *testing.T) {
	calls := 0
	get := youtubeGet(func(_ context.Context, u string) (*safefetch.Result, error) {
		calls++
		final := u
		if strings.Contains(u, "redirect") {
			final = "https://evil.example/"
		}
		return &safefetch.Result{Status: 200, Body: []byte("x"), FinalURL: final}, nil
	})
	if _, _, err := get(context.Background(), "https://evil.example/feed"); !errors.Is(err, errNotYouTubeHost) || calls != 0 {
		t.Errorf("another host: %v after %d requests", err, calls)
	}
	if _, _, err := get(context.Background(), "https://www.youtube.com/feeds/videos.xml?redirect=1"); !errors.Is(err, errNotYouTubeHost) {
		t.Errorf("an answer from another host was taken: %v", err)
	}
	if st, _, err := get(context.Background(), youtube.FeedURL(ytChannel)); err != nil || st != 200 {
		t.Errorf("YouTube's own feed: %d %v", st, err)
	}
}

func ytFeed(entries ...string) string {
	return `<?xml version="1.0"?><feed xmlns:yt="http://www.youtube.com/xml/schemas/2015" xmlns:media="http://search.yahoo.com/mrss/" xmlns="http://www.w3.org/2005/Atom">` +
		`<yt:channelId>` + ytChannel + `</yt:channelId><title>Vayu Videos</title>` + strings.Join(entries, "") + `</feed>`
}

func ytEntry(id, title string) string {
	return `<entry><yt:videoId>` + id + `</yt:videoId><yt:channelId>` + ytChannel + `</yt:channelId><title>` + title +
		`</title><published>2026-10-30T10:00:00Z</published><media:group><media:description>d</media:description></media:group></entry>`
}

// fakeYouTube answers for YouTube while a test runs.
func fakeYouTube(t *testing.T, pages map[string]string) {
	t.Helper()
	prevT, prevI := youtubeFetchText, youtubeFetchImage
	t.Cleanup(func() { youtubeFetchText, youtubeFetchImage = prevT, prevI })
	youtubeFetchText = func(_ context.Context, u string) (int, []byte, error) {
		if b, ok := pages[u]; ok {
			return 200, []byte(b), nil
		}
		return 404, nil, nil
	}
	youtubeFetchImage = func(context.Context, string) (int, []byte, error) { return 404, nil, nil }
}

func ytPost(h http.HandlerFunc, path string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestFindingAChannelShowsItToConfirm(t *testing.T) {
	a := youtubeTestApp(t)
	fakeYouTube(t, map[string]string{
		youtube.HandlePageURL("vayu"):    `<link rel="canonical" href="https://www.youtube.com/channel/` + ytChannel + `">`,
		youtube.FeedURL(ytChannel):       ytFeed(ytEntry("vvvvvvvvvv1", "First &lt;b&gt;video&lt;/b&gt;"), ytEntry("vvvvvvvvvv2", "Second")),
		youtube.ShortsFeedURL(ytChannel): ytFeed(),
		youtube.LongFeedURL(ytChannel):   ytFeed(),
	})
	body := ytPost(a.handleOSYouTubeResolve, "/os/api/youtube/resolve", url.Values{"address": {"https://www.youtube.com/@vayu"}, "site": {""}}).Body.String()
	for _, want := range []string{"Vayu Videos", "2 videos in its feed now", `name="channel" value="` + ytChannel + `"`, "First &lt;b&gt;video&lt;/b&gt;", `name="auto" value="1" checked`, `name="publish" value="1" checked`} {
		if !strings.Contains(body, want) {
			t.Errorf("the confirmation lacks %s:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<b>video") {
		t.Errorf("a title's markup reached the page:\n%s", body)
	}
	body = ytPost(a.handleOSYouTubeResolve, "/os/api/youtube/resolve", url.Values{"address": {"https://evil.example/@vayu"}, "site": {""}}).Body.String()
	if !strings.Contains(body, "That address is not on YouTube.") {
		t.Errorf("another host:\n%s", body)
	}
	body = ytPost(a.handleOSYouTubeResolve, "/os/api/youtube/resolve", url.Values{"address": {"@vayu"}, "site": {"no-such-site"}}).Body.String()
	if !strings.Contains(body, "Choose a site to post on.") {
		t.Errorf("a site that is not one:\n%s", body)
	}
}

// Following reads the channel's name from the channel, not the form, and
// wakes the loop so the first posts are made at once.
func TestFollowingAChannelChecksItAtOnce(t *testing.T) {
	a := youtubeTestApp(t)
	fakeYouTube(t, map[string]string{youtube.FeedURL(ytChannel): ytFeed(ytEntry("vvvvvvvvvv1", "First"))})
	w := ytPost(a.handleOSYouTubeFollow, "/os/api/youtube/follow", url.Values{"channel": {ytChannel}, "site": {""}, "auto": {"1"}, "publish": {"1"}, "title": {"<script>"}})
	if w.Code != 200 {
		t.Fatalf("follow: %d %s", w.Code, w.Body)
	}
	chans, _ := a.youtube.Channels(context.Background())
	if len(chans) != 1 || chans[0].Title != "Vayu Videos" || !chans[0].AutoPost || chans[0].AsDraft || chans[0].Shorts {
		t.Errorf("followed %+v", chans)
	}
	select {
	case <-a.youtubePoke:
	default:
		t.Error("the loop was not woken")
	}
	if w := ytPost(a.handleOSYouTubeFollow, "/os/api/youtube/follow", url.Values{"channel": {ytChannel}, "site": {""}}); w.Code != http.StatusConflict {
		t.Errorf("following twice: %d", w.Code)
	}
	if w := ytPost(a.handleOSYouTubeFollow, "/os/api/youtube/follow", url.Values{"channel": {"UC../../x"}, "site": {""}}); w.Code != http.StatusBadRequest {
		t.Errorf("a malformed channel: %d", w.Code)
	}
}

func TestTheYouTubePageShowsEachChannel(t *testing.T) {
	a := youtubeTestApp(t)
	ctx := context.Background()
	f, _ := a.youtube.Follow(ctx, "", youtube.Channel{ID: ytChannel, Title: "Vayu <Videos>"}, false, false, true)
	_, _ = a.youtube.Claim(ctx, "", f.ID, youtube.Video{ID: "hhhhhhhhhhh", Title: "Held one", Kind: youtube.KindVideo, Published: time.Now()}, youtube.StateHeld)
	_ = a.youtube.Checked(ctx, f, 0, errors.New("YouTube answered 500"))
	_ = a.youtube.Checked(ctx, youtube.Followed{ID: f.ID, Failures: 1}, 0, errors.New("YouTube answered 500"))
	_ = a.youtube.Checked(ctx, youtube.Followed{ID: f.ID, Failures: 2}, 0, errors.New("YouTube answered 500"))
	body := a.youtubeBody(ctx)
	for _, want := range []string{"1 channel followed", "Vayu &lt;Videos&gt;", "Fetch now", "Waiting for Post", "Held one", "Post all",
		`/os/api/youtube/` + f.ID + `/set?f=auto`, "YouTube answered 500", "Cannot be read", "Add a key"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	if strings.Contains(body, "<Videos>") {
		t.Error("a channel's name reached the page as markup")
	}
	s := a.getOSSettings(ctx)
	var found bool
	for _, n := range a.osNotifications(ctx, s) {
		if n.Href == youtubePage {
			found = true
		}
	}
	if !found {
		t.Error("the bell does not say a channel cannot be read")
	}
}

func TestTheSwitchesSetOnlyTheirOwn(t *testing.T) {
	a := youtubeTestApp(t)
	ctx := context.Background()
	f, _ := a.youtube.Follow(ctx, "", youtube.Channel{ID: ytChannel}, true, false, true)
	set := func(field, on string) {
		r := httptest.NewRequest(http.MethodPost, "/os/api/youtube/"+f.ID+"/set?f="+field, strings.NewReader("on="+on))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", f.ID)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()
		a.handleOSYouTubeSet(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", field, w.Code, w.Body)
		}
	}
	set("auto", "")
	set("publish", "")
	got, _ := a.youtube.Channel(ctx, f.ID)
	if got.AutoPost || !got.AsDraft || !got.Shorts || got.Paused {
		t.Errorf("after auto off and publish off: %+v", got)
	}
	set("shorts", "")
	set("paused", "1")
	if got, _ = a.youtube.Channel(ctx, f.ID); got.Shorts || !got.Paused || got.AutoPost || !got.AsDraft {
		t.Errorf("after shorts off and pause: %+v", got)
	}
}

func TestYouTubeIsAnAdministratorsPage(t *testing.T) {
	for _, p := range []string{"/os/youtube", "/os/api/youtube/resolve", "/os/api/youtube/abc/post-all"} {
		if lvl := osPathMinLevel(p); lvl != accessAdmin {
			t.Errorf("%s opens at level %d; want administrators only", p, lvl)
		}
	}
}

// One click for a new site: the follow makes the site and follows the
// channel onto it. A bad address, or one already served, makes nothing; nor
// does a channel that cannot be read.
func TestFollowingOntoANewSiteMakesTheSite(t *testing.T) {
	a := youtubeTestApp(t)
	a.domains = domain.New(dbpkg.DB, dbpkg.RDB)
	fakeYouTube(t, map[string]string{youtube.FeedURL(ytChannel): ytFeed(ytEntry("vvvvvvvvvv1", "First"))})
	ctx := context.Background()
	sites := func() int { l, _ := a.domains.List(ctx); return len(l) }
	before := sites()

	for host, want := range map[string]string{
		"not a host!":        "write it as blog.example.com",
		config.Cfg.Domain:    "own address; choose it",
		"videos.example.com": "",
	} {
		body := ytPost(a.handleOSYouTubeResolve, "/os/api/youtube/resolve", url.Values{"address": {ytChannel}, "site": {youtubeNewSite}, "new_host": {host}}).Body.String()
		if want != "" && !strings.Contains(body, want) {
			t.Errorf("resolve onto %q: %s", host, body)
		}
		if want == "" && !strings.Contains(body, "Follow on videos.example.com") {
			t.Errorf("resolve onto a new site: %s", body)
		}
	}
	if sites() != before {
		t.Fatal("finding the channel made a site")
	}
	fakeYouTube(t, map[string]string{})
	if w := ytPost(a.handleOSYouTubeFollow, "/os/api/youtube/follow", url.Values{"channel": {ytChannel}, "site": {youtubeNewSite}, "new_host": {"videos.example.com"}}); w.Code != http.StatusBadGateway || sites() != before {
		t.Fatalf("a channel that cannot be read: %d, %d sites", w.Code, sites())
	}
	fakeYouTube(t, map[string]string{youtube.FeedURL(ytChannel): ytFeed(ytEntry("vvvvvvvvvv1", "First"))})
	if w := ytPost(a.handleOSYouTubeFollow, "/os/api/youtube/follow", url.Values{"channel": {ytChannel}, "site": {youtubeNewSite}, "new_host": {"videos.example.com"}, "auto": {"1"}, "publish": {"1"}}); w.Code != 200 {
		t.Fatalf("follow onto a new site: %d %s", w.Code, w.Body)
	}
	d, err := a.domains.Resolve(ctx, "videos.example.com")
	if err != nil || d.SiteType != domain.SiteBlog {
		t.Fatalf("the new site: %+v %v", d, err)
	}
	chans, _ := a.youtube.Channels(ctx)
	if len(chans) != 1 || chans[0].DomainID != d.ID {
		t.Errorf("the channel is not followed on the new site: %+v", chans)
	}
	if body := a.youtubeBody(ctx); !strings.Contains(body, "On videos.example.com") || !strings.Contains(body, "its certificate is issued") {
		t.Errorf("the page does not say the new site waits for its address:\n%s", body)
	}
	if body := ytPost(a.handleOSYouTubeResolve, "/os/api/youtube/resolve", url.Values{"address": {ytChannel}, "site": {youtubeNewSite}, "new_host": {"videos.example.com"}}).Body.String(); !strings.Contains(body, "exists already") {
		t.Errorf("a new site at an address already served: %s", body)
	}
}
