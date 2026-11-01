// SPDX-License-Identifier: Apache-2.0

package youtube

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// testStore is the store over a database built from migration 129 itself.
func testStore(t *testing.T, now *time.Time) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "yt.db")+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	up, err := os.ReadFile("../db/migrations/129-youtube-channels.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(up), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "--") {
			if _, err := db.Exec(line); err != nil {
				t.Fatalf("%s: %v", line, err)
			}
		}
	}
	return NewStore(db, db, func() time.Time { return *now })
}

// web answers fixed bodies by address, and records what was asked.
type web struct {
	mu    sync.Mutex
	pages map[string]string
	asked []string
}

func (w *web) get(_ context.Context, u string) (int, []byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.asked = append(w.asked, u)
	if !Fetchable(u) {
		return 0, nil, fmt.Errorf("asked for %s", u)
	}
	// The key is checked, then left out of the address the pages are filed by.
	if strings.Contains(u, "youtube.googleapis.com") {
		if !strings.Contains(u, "key="+secretKey) {
			return 400, nil, nil
		}
		u = strings.NewReplacer("key="+secretKey+"&", "", "&key="+secretKey, "").Replace(u)
	}
	b, ok := w.pages[u]
	if !ok {
		return 404, nil, nil
	}
	return 200, []byte(b), nil
}

// poster records the posts made.
type poster struct {
	mu        sync.Mutex
	posts     []string // domain/video/draft
	refreshed []string
	fail      bool
}

func (p *poster) Post(_ context.Context, domainID string, v Video, draft bool) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		return "", errors.New("the queue is full")
	}
	p.posts = append(p.posts, fmt.Sprintf("%s/%s/%s/%v", domainID, v.ID, v.Kind, draft))
	return "post-" + v.ID, nil
}

func (p *poster) Refresh(_ context.Context, slug string, v Video) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refreshed = append(p.refreshed, slug+"/"+v.Kind)
	return nil
}

func ts(day int) string { return fmt.Sprintf("2026-10-%02dT10:00:00Z", day) }

// channelWeb is a channel with three uploads, one of them a Short, and an
// older Short only its Shorts feed still lists.
func channelWeb() *web {
	return &web{pages: map[string]string{
		FeedURL(chanID): string(feed(chanID,
			entry("vvvvvvvvvv3", chanID, "Third", "", ts(30)),
			entry("sssssssssss", chanID, "A Short", "#shorts", ts(29)),
			entry("vvvvvvvvvv1", chanID, "First", "", ts(20)))),
		ShortsFeedURL(chanID): string(feed(chanID,
			entry("sssssssssss", chanID, "A Short", "#shorts", ts(29)),
			entry("ssssssssss0", chanID, "An old Short", "", ts(2)))),
		LongFeedURL(chanID): string(feed(chanID,
			entry("vvvvvvvvvv3", chanID, "Third", "", ts(30)),
			entry("vvvvvvvvvv1", chanID, "First", "", ts(20)))),
	}}
}

func follower(t *testing.T, w *web, p *poster, now *time.Time) *Follower {
	return &Follower{Store: testStore(t, now), Get: w.get, Poster: p}
}

func TestACheckPostsEveryVideoOnceOldestFirst(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := &poster{}
	fw := follower(t, channelWeb(), p, &now)
	f, err := fw.Store.Follow(context.Background(), "", Channel{ID: chanID, Title: "Vayu"}, true, false, true)
	if err != nil {
		t.Fatal(err)
	}
	n, err := fw.Check(context.Background(), f)
	if err != nil || n != 4 {
		t.Fatalf("first check: %d new, %v", n, err)
	}
	want := []string{"/ssssssssss0/short/false", "/vvvvvvvvvv1/video/false", "/sssssssssss/short/false", "/vvvvvvvvvv3/video/false"}
	if fmt.Sprint(p.posts) != fmt.Sprint(want) {
		t.Errorf("posts:\n got %v\nwant %v", p.posts, want)
	}
	// Run again, and the posts are the same posts: nothing is made twice.
	if n, err := fw.Check(context.Background(), f); err != nil || n != 0 || len(p.posts) != 4 {
		t.Errorf("second check: %d new, %v, %d posts", n, err, len(p.posts))
	}
	got, _ := fw.Store.Channel(context.Background(), f.ID)
	if got.LastFound != 0 || got.Failures != 0 || !got.NextCheck.After(now.Add(CheckEvery-time.Second)) || got.Title != "Vayu & Friends" {
		t.Errorf("recorded %+v", got)
	}
}

func TestTwoChecksAtOnceMakeOnePostEach(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := &poster{}
	fw := follower(t, channelWeb(), p, &now)
	f, _ := fw.Store.Follow(context.Background(), "", Channel{ID: chanID}, true, false, true)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = fw.Check(context.Background(), f) }()
	}
	wg.Wait()
	sort.Strings(p.posts)
	if len(p.posts) != 4 {
		t.Errorf("four checks at once made %d posts: %v", len(p.posts), p.posts)
	}
}

func TestEachSiteHasItsOwnPosts(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := &poster{}
	fw := follower(t, channelWeb(), p, &now)
	a, _ := fw.Store.Follow(context.Background(), "", Channel{ID: chanID}, true, false, true)
	b, _ := fw.Store.Follow(context.Background(), "site-b", Channel{ID: chanID}, true, false, true)
	_, _ = fw.Check(context.Background(), a)
	if n, err := fw.Check(context.Background(), b); err != nil || n != 4 {
		t.Errorf("the second site found %d new, %v: one site's videos were taken as another's", n, err)
	}
	if _, err := fw.Store.Follow(context.Background(), "site-b", Channel{ID: chanID}, true, false, true); !errors.Is(err, ErrFollowed) {
		t.Errorf("following twice on one site: %v", err)
	}
}

func TestAutoPostOffHoldsAndPostMakesIt(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := &poster{}
	fw := follower(t, channelWeb(), p, &now)
	f, _ := fw.Store.Follow(context.Background(), "", Channel{ID: chanID}, false, false, true)
	if n, _ := fw.Check(context.Background(), f); n != 4 || len(p.posts) != 0 {
		t.Fatalf("auto-post off: %d found, posts %v", n, p.posts)
	}
	held, _ := fw.Store.Videos(context.Background(), f.ID, StateHeld, 10)
	if len(held) != 4 {
		t.Fatalf("%d held", len(held))
	}
	if err := fw.PostHeld(context.Background(), f, "vvvvvvvvvv3"); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(p.posts) != "[/vvvvvvvvvv3/video/false]" {
		t.Errorf("Post made %v", p.posts)
	}
	if err := fw.PostHeld(context.Background(), f, "vvvvvvvvvv3"); err == nil || len(p.posts) != 1 {
		t.Errorf("a video posted twice by Post: %v %v", err, p.posts)
	}
}

func TestShortsOffAndDraftsAreKept(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := &poster{}
	fw := follower(t, channelWeb(), p, &now)
	f, _ := fw.Store.Follow(context.Background(), "", Channel{ID: chanID}, true, true, false)
	_, _ = fw.Check(context.Background(), f)
	if want := "[/vvvvvvvvvv1/video/true /vvvvvvvvvv3/video/true]"; fmt.Sprint(p.posts) != want {
		t.Errorf("Shorts off, drafts on: %v; want %s", p.posts, want)
	}
	if c, _ := fw.Store.Counts(context.Background(), f.ID); c[StateSkipped] != 2 {
		t.Errorf("counts %v", c)
	}
}

func TestAFailedPostIsTriedAgain(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p := &poster{fail: true}
	fw := follower(t, channelWeb(), p, &now)
	f, _ := fw.Store.Follow(context.Background(), "", Channel{ID: chanID}, true, false, true)
	if _, err := fw.Check(context.Background(), f); err == nil {
		t.Fatal("a check whose posts all failed reported success")
	}
	p.fail = false
	f, _ = fw.Store.Channel(context.Background(), f.ID)
	if _, err := fw.Check(context.Background(), f); err != nil || len(p.posts) != 4 {
		t.Errorf("after the queue recovered: %v, %v", err, p.posts)
	}
}

func TestFailingChecksWaitLongerAndRingTheBell(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	fw := follower(t, &web{pages: map[string]string{}}, &poster{}, &now)
	f, _ := fw.Store.Follow(context.Background(), "", Channel{ID: chanID}, true, false, true)
	for i, wait := range []time.Duration{15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour, 24 * time.Hour} {
		if _, err := fw.Check(context.Background(), f); err == nil {
			t.Fatal("a channel with no feed was read")
		}
		f, _ = fw.Store.Channel(context.Background(), f.ID)
		if f.Failures != i+1 || !f.NextCheck.Equal(now.Add(wait)) || f.LastError == "" {
			t.Errorf("after %d failures: %+v; want the next in %s", i+1, f, wait)
		}
		if want := 0; i >= 2 {
			want = 1
			if got := fw.Store.Failing(context.Background()); got != want {
				t.Errorf("after %d failures the bell counts %d", i+1, got)
			}
		} else if got := fw.Store.Failing(context.Background()); got != want {
			t.Errorf("after %d failures the bell counts %d", i+1, got)
		}
	}
	fw.Get = channelWeb().get
	if _, err := fw.Check(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if f, _ = fw.Store.Channel(context.Background(), f.ID); f.Failures != 0 || f.LastError != "" || fw.Store.Failing(context.Background()) != 0 {
		t.Errorf("a good check did not clear the failures: %+v", f)
	}
}

func TestResolveWithoutAKey(t *testing.T) {
	now := time.Now()
	w := channelWeb()
	w.pages[HandlePageURL("vayu")] = `<link rel="canonical" href="https://www.youtube.com/channel/` + chanID + `">`
	w.pages[VideoPageURL("vvvvvvvvvv1")] = `"channelId":"` + chanID + `"`
	w.pages[HandlePageURL("consent")] = `<html>Before you continue to YouTube</html>`
	fw := follower(t, w, &poster{}, &now)
	for _, in := range []string{"@vayu", "https://youtu.be/vvvvvvvvvv1", "https://www.youtube.com/channel/" + chanID} {
		ch, vids, err := fw.Resolve(context.Background(), in)
		if err != nil || ch.ID != chanID || len(vids) != 3 {
			t.Errorf("Resolve(%s) = %+v, %d videos, %v", in, ch, len(vids), err)
		}
	}
	if _, _, err := fw.Resolve(context.Background(), "@consent"); !errors.Is(err, ErrConsent) {
		t.Errorf("a consent page: %v", err)
	}
	if _, _, err := fw.Resolve(context.Background(), "https://evil.example/@vayu"); !errors.Is(err, ErrNotYouTube) {
		t.Errorf("another host: %v", err)
	}
	for _, u := range w.asked {
		if !Fetchable(u) {
			t.Errorf("asked for %s", u)
		}
	}
}

// apiWeb answers the Data API for a channel whose back catalogue is two pages
// of Shorts and one of long videos, one an upcoming premiere.
func apiWeb() *web {
	w := channelWeb()
	api := "https://youtube.googleapis.com/youtube/v3/"
	item := func(id, live string, start string) string {
		return `{"id":"` + id + `","snippet":{"channelId":"` + chanID + `","title":"T ` + id + `","description":"From the API","publishedAt":"2025-01-01T00:00:00Z","liveBroadcastContent":"` + live + `"},"liveStreamingDetails":{"scheduledStartTime":"` + start + `"}}`
	}
	pl := func(ids []string, next string, total int) string {
		var items []string
		for _, id := range ids {
			items = append(items, `{"contentDetails":{"videoId":"`+id+`"}}`)
		}
		return `{"nextPageToken":"` + next + `","pageInfo":{"totalResults":` + fmt.Sprint(total) + `},"items":[` + strings.Join(items, ",") + `]}`
	}
	w.pages[api+"playlistItems?maxResults=50&part=contentDetails&playlistId=UUSH"+chanID[2:]] = pl([]string{"bbbbbbbbbb1"}, "P2", 2)
	w.pages[api+"playlistItems?maxResults=50&pageToken=P2&part=contentDetails&playlistId=UUSH"+chanID[2:]] = pl([]string{"bbbbbbbbbb2"}, "", 2)
	w.pages[api+"playlistItems?maxResults=50&part=contentDetails&playlistId=UULF"+chanID[2:]] = pl([]string{"bbbbbbbbbb3", "uuuuuuuuuuu"}, "", 2)
	w.pages[api+"videos?id=bbbbbbbbbb1&part=snippet%2CliveStreamingDetails"] = `{"items":[` + item("bbbbbbbbbb1", "none", "") + `]}`
	w.pages[api+"videos?id=bbbbbbbbbb2&part=snippet%2CliveStreamingDetails"] = `{"items":[` + item("bbbbbbbbbb2", "none", "") + `]}`
	w.pages[api+"videos?id=bbbbbbbbbb3%2Cuuuuuuuuuuu&part=snippet%2CliveStreamingDetails"] = `{"items":[` + item("bbbbbbbbbb3", "none", "") + `,` + item("uuuuuuuuuuu", "upcoming", "2026-11-02T18:00:00Z") + `,` +
		strings.Replace(item("xxxxxxxxxxx", "none", ""), chanID, otherID, 1) + `]}`
	return w
}

func TestWithAKeyTheBackCatalogueIsReadOverChecks(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	w := apiWeb()
	p := &poster{}
	fw := follower(t, w, p, &now)
	fw.Key = func(context.Context) string { return secretKey }
	f, _ := fw.Store.Follow(context.Background(), "", Channel{ID: chanID}, true, false, true)
	// The first check reads three pages: both pages of Shorts, then the
	// long videos.
	if _, err := fw.Check(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	f, _ = fw.Store.Channel(context.Background(), f.ID)
	if f.BackfillDone || f.BackfillSeen != 4 || f.BackfillTotal != 4 || !strings.HasPrefix(f.BackfillToken, "2:") {
		t.Errorf("after one check: %+v", f)
	}
	posted := strings.Join(p.posts, " ")
	if strings.Contains(posted, "xxxxxxxxxxx") {
		t.Errorf("another channel's video was posted: %v", p.posts)
	}
	for _, want := range []string{"/bbbbbbbbbb1/short/", "/bbbbbbbbbb2/short/", "/bbbbbbbbbb3/video/", "/uuuuuuuuuuu/upcoming/"} {
		if !strings.Contains(posted, want) {
			t.Errorf("no post %s in %v", want, p.posts)
		}
	}
	// The second check finds the streams playlist empty and is done.
	if _, err := fw.Check(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if f, _ = fw.Store.Channel(context.Background(), f.ID); !f.BackfillDone {
		t.Errorf("the back catalogue was not finished: %+v", f)
	}
	if r, _ := fw.Store.Video(context.Background(), "", "uuuuuuuuuuu"); r.State != StateUpcoming {
		t.Errorf("the premiere's row: %+v", r)
	}
	// Started but still streaming: the post stays as it is.
	now = time.Date(2026, 11, 2, 18, 30, 0, 0, time.UTC)
	w.pages["https://youtube.googleapis.com/youtube/v3/videos?id=uuuuuuuuuuu&part=snippet%2CliveStreamingDetails"] =
		`{"items":[{"id":"uuuuuuuuuuu","snippet":{"channelId":"` + chanID + `","title":"Live","liveBroadcastContent":"live"}}]}`
	if _, err := fw.Check(context.Background(), f); err != nil || len(p.refreshed) != 0 {
		t.Fatalf("a stream still live was rewritten as aired: %v %v", err, p.refreshed)
	}
	// After the premiere airs, its post is rewritten as the video.
	now = time.Date(2026, 11, 2, 19, 0, 0, 0, time.UTC)
	w.pages["https://youtube.googleapis.com/youtube/v3/videos?id=uuuuuuuuuuu&part=snippet%2CliveStreamingDetails"] =
		`{"items":[{"id":"uuuuuuuuuuu","snippet":{"channelId":"` + chanID + `","title":"Aired","liveBroadcastContent":"none"}}]}`
	if _, err := fw.Check(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(p.refreshed) != "[post-uuuuuuuuuuu/video]" {
		t.Errorf("refreshed %v", p.refreshed)
	}
	if r, _ := fw.Store.Video(context.Background(), "", "uuuuuuuuuuu"); r.State != StatePosted {
		t.Errorf("after airing: %+v", r)
	}
	for _, u := range w.asked {
		if !Fetchable(u) {
			t.Errorf("asked for %s", u)
		}
	}
}

func TestDueIsTheChannelsWhoseTimeHasCome(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	st := testStore(t, &now)
	ctx := context.Background()
	a, _ := st.Follow(ctx, "", Channel{ID: chanID}, true, false, true)
	b, _ := st.Follow(ctx, "site-b", Channel{ID: chanID}, true, false, true)
	c, _ := st.Follow(ctx, "site-c", Channel{ID: chanID}, true, false, true)
	_ = st.Configure(ctx, b.ID, true, false, true, true) // paused
	_ = st.Checked(ctx, c, 0, nil)                       // checked just now
	due, err := st.Due(ctx, 10)
	if err != nil || len(due) != 1 || due[0].ID != a.ID {
		t.Errorf("due %+v, %v; want only the unpaused channel not yet checked", due, err)
	}
	now = now.Add(CheckEvery + 4*time.Minute)
	if due, _ := st.Due(ctx, 10); len(due) != 2 {
		t.Errorf("after the interval, %d due; want 2 (never the paused one)", len(due))
	}
	_ = st.CheckNow(ctx, b.ID)
	if due, _ := st.Due(ctx, 10); len(due) != 2 {
		t.Errorf("Check now made a paused channel due: %d", len(due))
	}
}

// The claim is the dedupe: whichever check claims a video first makes its
// post, and every later claim, by a check running alongside or after, makes
// nothing, whatever state it asks for.
func TestOnlyTheFirstClaimMakesARow(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	st := testStore(t, &now)
	ctx := context.Background()
	v := Video{ID: "vvvvvvvvvv1", Kind: KindVideo, Title: "First"}
	if made, err := st.Claim(ctx, "", "row", v, StateClaimed); !made || err != nil {
		t.Fatalf("first claim: %v %v", made, err)
	}
	if made, err := st.Claim(ctx, "", "row", v, StateHeld); made || err != nil {
		t.Errorf("a second claim made a row: %v %v", made, err)
	}
	if r, _ := st.Video(ctx, "", v.ID); r.State != StateClaimed {
		t.Errorf("a second claim changed the first: %+v", r)
	}
	if made, _ := st.Claim(ctx, "site-b", "row", v, StateClaimed); !made {
		t.Error("another site's claim of the same video was refused")
	}
}

// A spent quota stops the back catalogue, not the channel: its new videos
// still come from the feeds, and the back catalogue goes on later.
func TestASpentQuotaLeavesTheFeedsWorking(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	w := channelWeb() // no API pages: every API call is refused
	p := &poster{}
	fw := follower(t, w, p, &now)
	fw.Key = func(context.Context) string { return secretKey }
	f, _ := fw.Store.Follow(context.Background(), "", Channel{ID: chanID}, true, false, true)
	w.pages["https://youtube.googleapis.com/youtube/v3/playlistItems?maxResults=50&part=contentDetails&playlistId=UUSH"+chanID[2:]] = "x"
	if _, err := fw.Check(context.Background(), f); err != nil {
		t.Fatalf("a refused back catalogue failed the check: %v", err)
	}
	if len(p.posts) != 4 {
		t.Errorf("the feeds' videos were not posted: %v", p.posts)
	}
	if f, _ = fw.Store.Channel(context.Background(), f.ID); f.BackfillDone || f.Failures != 0 {
		t.Errorf("the back catalogue was marked read, or the check failed: %+v", f)
	}
}

// YouTube has answered 404 for the channel feed of a channel that exists;
// its uploads playlist's feed stands in, for a follow and for a check.
func TestAMissingChannelFeedIsReadFromTheUploadsPlaylist(t *testing.T) {
	now := time.Now()
	w := channelWeb()
	w.pages[UploadsFeedURL(chanID)] = w.pages[FeedURL(chanID)]
	delete(w.pages, FeedURL(chanID))
	w.pages[HandlePageURL("vayu")] = `<link rel="canonical" href="https://www.youtube.com/channel/` + chanID + `">`
	fw := follower(t, w, &poster{}, &now)
	ch, vids, err := fw.Resolve(context.Background(), "youtube.com/vayu")
	if err != nil || ch.ID != chanID || len(vids) != 3 {
		t.Fatalf("Resolve = %+v, %d videos, %v", ch, len(vids), err)
	}
	if _, all, err := fw.latest(context.Background(), chanID); err != nil || len(all) != 4 {
		t.Fatalf("latest = %d videos, %v", len(all), err)
	}
	delete(w.pages, UploadsFeedURL(chanID))
	if _, _, err := fw.Resolve(context.Background(), "youtube.com/vayu"); !errors.Is(err, errNoFeed) {
		t.Errorf("with neither feed: %v", err)
	}
}
