// SPDX-License-Identifier: Apache-2.0

package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/blockrender"
)

const (
	chanID  = "UCabcdefghijklmnopqrstuv"
	otherID = "UCzzzzzzzzzzzzzzzzzzzzzz"
)

func TestParseAddressReadsEveryShape(t *testing.T) {
	cases := map[string]Ref{
		"@vayu.press":                                         {Handle: "vayu.press"},
		chanID:                                                {ChannelID: chanID},
		"https://www.youtube.com/@vayupress":                  {Handle: "vayupress"},
		"youtube.com/@vayupress/videos":                       {Handle: "vayupress"},
		"https://m.youtube.com/channel/" + chanID:             {ChannelID: chanID},
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=10":    {VideoID: "dQw4w9WgXcQ"},
		"https://youtu.be/dQw4w9WgXcQ?si=abc":                 {VideoID: "dQw4w9WgXcQ"},
		"https://www.youtube.com/shorts/dQw4w9WgXcQ":          {VideoID: "dQw4w9WgXcQ"},
		"https://www.youtube.com/live/dQw4w9WgXcQ":            {VideoID: "dQw4w9WgXcQ"},
		"https://www.youtube.com/c/VayuPress":                 {Handle: "VayuPress"},
		"youtube.com/johalputt":                               {Handle: "johalputt"},
		"  https://music.youtube.com/channel/" + chanID + " ": {ChannelID: chanID},
	}
	for in, want := range cases {
		got, err := ParseAddress(in)
		if err != nil || got != want {
			t.Errorf("ParseAddress(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
}

// Each refused address is refused for its own reason, so loosening one check
// is seen on its own.
func TestParseAddressRefusesOtherHostsAndBadIDs(t *testing.T) {
	cases := map[string]error{
		"https://evil.example/@vayupress":                                 ErrNotYouTube,
		"https://youtube.com.evil.example/@vayupress":                     ErrNotYouTube,
		"https://evil.example/?u=https://youtube.com/watch?v=dQw4w9WgXcQ": ErrNotYouTube,
		"https://notyoutube.com/@vayupress":                               ErrNotYouTube,
		"ftp://www.youtube.com/@vayupress":                                ErrNotYouTube,
		"https://www.youtube.com/channel/UCshort":                         ErrUnrecognised,
		"https://www.youtube.com/watch?v=dQw4w9WgXc":                      ErrUnrecognised, // 10 characters
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ%22":                  ErrUnrecognised,
		"@a":                                    ErrUnrecognised,
		"@vayu/../../x":                         ErrUnrecognised,
		"https://www.youtube.com/feed/trending": ErrUnrecognised,
		"https://www.youtube.com/results":       ErrUnrecognised,
		"https://www.youtube.com/premium":       ErrUnrecognised,
	}
	for in, want := range cases {
		if _, err := ParseAddress(in); !errors.Is(err, want) {
			t.Errorf("ParseAddress(%q) = %v; want %v", in, err, want)
		}
	}
}

func TestOnlyTheThreeHostsAreFetchable(t *testing.T) {
	for _, u := range []string{FeedURL(chanID), ShortsFeedURL(chanID), LongFeedURL(chanID), HandlePageURL("vayu"), VideoPageURL("dQw4w9WgXcQ"), ThumbURLs("dQw4w9WgXcQ")[0], "https://youtube.googleapis.com/youtube/v3/videos?id=x"} {
		if !Fetchable(u) {
			t.Errorf("%s is built here and must be fetchable", u)
		}
	}
	for _, u := range []string{
		"http://www.youtube.com/feeds/videos.xml",      // not HTTPS
		"https://youtube.com/feeds/videos.xml",         // not the one page host
		"https://i1.ytimg.com/vi/x/hqdefault.jpg",      // a feed's own thumbnail host
		"https://www.youtube.com:8443/",                // a port
		"https://user@www.youtube.com/",                // credentials
		"https://www.youtube.com.evil.example/",        // a longer host
		"https://www.googleapis.com/youtube/v3/videos", // the wider API host
	} {
		if Fetchable(u) {
			t.Errorf("%s must not be fetchable", u)
		}
	}
}

func TestPlaylistAddressesSwapThePrefix(t *testing.T) {
	if got := ShortsFeedURL(chanID); !strings.HasSuffix(got, "playlist_id=UUSHabcdefghijklmnopqrstuv") {
		t.Errorf("Shorts feed: %s", got)
	}
	if got := LongFeedURL(chanID); !strings.HasSuffix(got, "playlist_id=UULFabcdefghijklmnopqrstuv") {
		t.Errorf("long feed: %s", got)
	}
}

func entry(id, ch, title, desc, pub string) string {
	return `<entry><id>yt:video:` + id + `</id><yt:videoId>` + id + `</yt:videoId><yt:channelId>` + ch + `</yt:channelId>` +
		`<title>` + title + `</title><link rel="alternate" href="https://evil.example/watch?v=` + id + `"/>` +
		`<published>` + pub + `</published><media:group><media:title>` + title + `</media:title>` +
		`<media:thumbnail url="https://evil.example/t.jpg" width="480" height="360"/>` +
		`<media:description>` + desc + `</media:description></media:group></entry>`
}

func feed(ch string, entries ...string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?><feed xmlns:yt="http://www.youtube.com/xml/schemas/2015" xmlns:media="http://search.yahoo.com/mrss/" xmlns="http://www.w3.org/2005/Atom">` +
		`<yt:channelId>` + ch + `</yt:channelId><title>Vayu &amp; Friends</title>` + strings.Join(entries, "") + `</feed>`)
}

func TestParseFeedGivesExactlyItsVideos(t *testing.T) {
	body := feed(chanID,
		entry("aaaaaaaaaaa", chanID, "First &lt;b&gt;bold&lt;/b&gt;", "Line one\nLine two", "2026-10-30T10:00:00+00:00"),
		entry("bbbbbbbbbbb", chanID, "Second", "", "2026-10-29T09:30:00+00:00"),
	)
	ch, vids, err := ParseFeed(body, chanID)
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID != chanID || ch.Title != "Vayu & Friends" {
		t.Errorf("channel %+v", ch)
	}
	want := []Video{
		{ID: "aaaaaaaaaaa", ChannelID: chanID, Title: "First <b>bold</b>", Description: "Line one\nLine two", Published: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC), Kind: KindVideo},
		{ID: "bbbbbbbbbbb", ChannelID: chanID, Title: "Second", Published: time.Date(2026, 10, 29, 9, 30, 0, 0, time.UTC), Kind: KindVideo},
	}
	if fmt.Sprint(vids) != fmt.Sprint(want) {
		t.Errorf("videos:\n got %+v\nwant %+v", vids, want)
	}
}

func TestParseFeedLeavesOutWhatIsNotTheChannels(t *testing.T) {
	_, vids, err := ParseFeed(feed(chanID,
		entry("ccccccccccc", otherID, "another channel's", "", "2026-10-30T10:00:00Z"),
		entry("bad id here", chanID, "a malformed id", "", "2026-10-30T10:00:00Z"),
		entry("ddddddddddd", chanID, "kept", "", "2026-10-30T10:00:00Z"),
	), chanID)
	if err != nil || len(vids) != 1 || vids[0].ID != "ddddddddddd" {
		t.Errorf("got %+v, %v; want only ddddddddddd", vids, err)
	}
	if _, _, err := ParseFeed(feed(otherID), chanID); err == nil {
		t.Error("a feed for another channel was read as this one's")
	}
	if _, _, err := ParseFeed([]byte(`<html>consent</html>`), chanID); !errors.Is(err, ErrNotAFeed) {
		t.Errorf("a page that is not a feed: %v", err)
	}
	if _, _, err := ParseFeed(append(feed(chanID), make([]byte, MaxFeedBytes)...), chanID); !errors.Is(err, ErrNotAFeed) {
		t.Errorf("an oversized feed: %v", err)
	}
}

func TestParseFeedStopsAtTheEntryCap(t *testing.T) {
	var es []string
	for i := 0; i < MaxFeedEntries+20; i++ {
		es = append(es, entry(fmt.Sprintf("v%010d", i), chanID, "t", "", "2026-10-30T10:00:00Z"))
	}
	_, vids, err := ParseFeed(feed(chanID, es...), chanID)
	if err != nil || len(vids) != MaxFeedEntries {
		t.Errorf("got %d videos, %v; want %d", len(vids), err, MaxFeedEntries)
	}
}

func TestChannelIDFromPage(t *testing.T) {
	page := []byte(`<html><head><link rel="canonical" href="https://www.youtube.com/channel/` + chanID + `"></head></html>`)
	if id, err := ChannelIDFromPage(page); err != nil || id != chanID {
		t.Errorf("canonical: %s, %v", id, err)
	}
	if id, err := ChannelIDFromPage([]byte(`{"externalId":"` + chanID + `"}`)); err != nil || id != chanID {
		t.Errorf("externalId: %s, %v", id, err)
	}
	if _, err := ChannelIDFromPage([]byte(`<html>Before you continue to YouTube</html>`)); !errors.Is(err, ErrNoChannelOnPage) {
		t.Errorf("a consent page named a channel: %v", err)
	}
	if id, err := ChannelIDFromVideoPage([]byte(`..."channelId":"` + chanID + `"...`)); err != nil || id != chanID {
		t.Errorf("video page: %s, %v", id, err)
	}
}

// fakeAPI answers like the Data API, recording the addresses asked for.
func fakeAPI(status int, body any, asked *[]string) Getter {
	return func(_ context.Context, u string) (int, []byte, error) {
		if asked != nil {
			*asked = append(*asked, u)
		}
		b, _ := json.Marshal(body)
		return status, b, nil
	}
}

const secretKey = "AIzaSyTHE-OPERATORS-KEY-0000000000000"

func TestAPIErrorsNeverCarryTheKey(t *testing.T) {
	api := API{Key: secretKey, Get: func(context.Context, string) (int, []byte, error) {
		return 0, nil, errors.New(`Get "https://youtube.googleapis.com/youtube/v3/videos?key=` + secretKey + `": dial tcp: timeout`)
	}}
	_, err := api.Videos(context.Background(), []string{"dQw4w9WgXcQ"})
	if err == nil || strings.Contains(err.Error(), secretKey) {
		t.Errorf("a transport error quoted the key: %v", err)
	}
	api.Get = fakeAPI(403, map[string]any{"error": map[string]any{"errors": []map[string]string{{"reason": "quotaExceeded"}}, "message": "key " + secretKey}}, nil)
	_, err = api.ChannelByID(context.Background(), chanID)
	var ae *APIError
	if !errors.As(err, &ae) || !ae.QuotaSpent() || strings.Contains(err.Error(), secretKey) {
		t.Errorf("quota refusal: %v", err)
	}
	api.Get = fakeAPI(400, map[string]any{"error": map[string]any{"errors": []map[string]string{{"reason": "<script>" + secretKey}}}}, nil)
	_, err = api.ChannelByID(context.Background(), chanID)
	if err == nil || strings.Contains(err.Error(), "script") || strings.Contains(err.Error(), secretKey) {
		t.Errorf("a reason that is not a word reached the message: %v", err)
	}
}

func TestAPIReadsUpcomingAndAsksOnlyItsHost(t *testing.T) {
	var asked []string
	api := API{Key: secretKey, Get: fakeAPI(200, map[string]any{"items": []map[string]any{
		{"id": "aaaaaaaaaaa", "snippet": map[string]any{"channelId": chanID, "title": "Premiere", "publishedAt": "2026-10-30T10:00:00Z", "liveBroadcastContent": "upcoming"}, "liveStreamingDetails": map[string]any{"scheduledStartTime": "2026-11-02T18:00:00Z"}},
		{"id": "bbbbbbbbbbb", "snippet": map[string]any{"channelId": chanID, "title": "Live", "liveBroadcastContent": "live"}},
		{"id": "ccccccccccc", "snippet": map[string]any{"channelId": chanID, "title": "Plain", "liveBroadcastContent": "none"}},
		{"id": "../../../xx", "snippet": map[string]any{"channelId": chanID}},
	}}, &asked)}
	vids, err := api.Videos(context.Background(), []string{"aaaaaaaaaaa", "bbbbbbbbbbb", "ccccccccccc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vids) != 3 || vids[0].Kind != KindUpcoming || !vids[0].Start.Equal(time.Date(2026, 11, 2, 18, 0, 0, 0, time.UTC)) ||
		vids[1].Kind != KindLive || vids[2].Kind != KindVideo {
		t.Errorf("videos %+v", vids)
	}
	for _, u := range asked {
		if !Fetchable(u) {
			t.Errorf("the API client asked for %s", u)
		}
	}
}

func TestHashtags(t *testing.T) {
	got := Hashtags("Watch #Go and #go again, #rust_lang. Not a#tag, not #2026, nor #x\n#नमस्ते")
	if want := []string{"go", "rust_lang", "नमस्ते"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v want %v", got, want)
	}
	var many []string
	for i := 0; i < 20; i++ {
		many = append(many, fmt.Sprintf("#tag%c%c", 'a'+i, 'a'+i))
	}
	if n := len(Hashtags(strings.Join(many, " "))); n != maxTags {
		t.Errorf("%d tags kept; want %d", n, maxTags)
	}
}

// The description is the channel's text. Its post must say exactly that,
// with links where it gave addresses and timestamps, and no markup of its own.
func render(t *testing.T, v Video) string {
	t.Helper()
	b, err := json.Marshal(Blocks(v, "/media/ab/cover.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	h, _, err := blockrender.Render(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestADescriptionIsTextWithLinks(t *testing.T) {
	v := Video{ID: "dQw4w9WgXcQ", Title: "A video", Kind: KindVideo, Description: "Read https://example.com/a?b=1&c=2. More (see https://example.org/x)\n\n" +
		"03:15 the chorus\n1:02:03 the end\n99:99 is not a time\n\n" +
		"https:///no-host and https://a.example/x\\y\n\n" +
		"**not bold** <b>not markup</b> [not](https://link.example) <script>alert(1)</script> javascript:alert(1)"}
	h := render(t, v)
	for _, want := range []string{
		`href="https://example.com/a?b=1&amp;c=2"`,
		`href="https://example.org/x"`,
		`href="https://www.youtube.com/watch?v=dQw4w9WgXcQ&amp;t=195s"`,
		`href="https://www.youtube.com/watch?v=dQw4w9WgXcQ&amp;t=3723s"`,
		`**not bold**`,
		`&lt;b&gt;not markup&lt;/b&gt;`,
		`&lt;script&gt;alert(1)&lt;/script&gt;`,
		`<br`,
		`class="video-facade"`,
		`data-embed-src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"`,
		`Watch on YouTube`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("the post lacks %s:\n%s", want, h)
		}
	}
	for _, banned := range []string{"<b>", "<script", "<strong>", `href="javascript`, "t=5999s", "t=6039s", `href="https:///no-host"`, `href="https://a.example/x`} {
		if strings.Contains(h, banned) {
			t.Errorf("the post holds %s:\n%s", banned, h)
		}
	}
	if strings.Contains(h, `href="https://link.example)`) {
		t.Errorf("a bracket was taken into an address:\n%s", h)
	}
}

func TestAShortIsDrawnUpright(t *testing.T) {
	h := render(t, Video{ID: "dQw4w9WgXcQ", Title: "S", Kind: KindShort})
	if !strings.Contains(h, `class="video-facade video-facade--tall"`) || !strings.Contains(h, "/shorts/dQw4w9WgXcQ") {
		t.Errorf("a Short:\n%s", h)
	}
	if h := render(t, Video{ID: "dQw4w9WgXcQ", Title: "V", Kind: KindVideo}); strings.Contains(h, "--tall") {
		t.Errorf("a video drawn as a Short:\n%s", h)
	}
}
