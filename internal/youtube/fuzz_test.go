// SPDX-License-Identifier: Apache-2.0

package youtube

import (
	"regexp"
	"testing"
)

// What a feed or a pasted address may put into a page and a URL, stated here
// rather than with the package's own validators, so loosening one of them
// does not loosen the check in the same edit.
var (
	fuzzVideoID   = regexp.MustCompile(`^[0-9A-Za-z_-]{11}$`)
	fuzzChannelID = regexp.MustCompile(`^UC[0-9A-Za-z_-]{22}$`)
	fuzzHandle    = regexp.MustCompile(`^[0-9A-Za-z_.-]{3,30}$`)
)

const fuzzFeed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns:yt="http://www.youtube.com/xml/schemas/2015" xmlns:media="http://search.yahoo.com/mrss/" xmlns="http://www.w3.org/2005/Atom">
 <yt:channelId>UCaaaaaaaaaaaaaaaaaaaaaa</yt:channelId><title>A channel</title>
 <entry><yt:videoId>abcdefghijk</yt:videoId><yt:channelId>UCaaaaaaaaaaaaaaaaaaaaaa</yt:channelId><title>One</title>
  <published>2026-11-15T09:00:00+00:00</published><media:group><media:description>d</media:description></media:group></entry>
 <entry><yt:videoId>"><script></yt:videoId><yt:channelId>UCaaaaaaaaaaaaaaaaaaaaaa</yt:channelId></entry>
</feed>`

// A feed comes from YouTube's servers, or from whatever answers in their
// place: what it yields is at most MaxFeedEntries videos, each with a video
// id, all of the channel asked for.
func FuzzParseFeed(f *testing.F) {
	f.Add([]byte(fuzzFeed), "")
	f.Add([]byte(fuzzFeed), "UCaaaaaaaaaaaaaaaaaaaaaa")
	f.Add([]byte(fuzzFeed), "UCbbbbbbbbbbbbbbbbbbbbbb")
	f.Add([]byte("<feed><entry/></feed>"), "")
	f.Fuzz(func(t *testing.T, body []byte, want string) {
		ch, videos, err := ParseFeed(body, want)
		if err != nil {
			return
		}
		if want != "" && ch.ID != want {
			t.Fatalf("asked for %q, read the feed of %q", want, ch.ID)
		}
		if len(videos) > MaxFeedEntries {
			t.Fatalf("%d videos read, more than %d", len(videos), MaxFeedEntries)
		}
		for _, v := range videos {
			if !fuzzVideoID.MatchString(v.ID) {
				t.Fatalf("a video read with the id %q", v.ID)
			}
			if v.ChannelID != ch.ID {
				t.Fatalf("a video of %q read in the feed of %q", v.ChannelID, ch.ID)
			}
		}
	})
}

// A pasted address names exactly one thing, and each is in its own form.
func FuzzParseAddress(f *testing.F) {
	for _, s := range []string{
		"@someone", "UCaaaaaaaaaaaaaaaaaaaaaa", "https://www.youtube.com/channel/UCaaaaaaaaaaaaaaaaaaaaaa",
		"youtu.be/abcdefghijk", "https://www.youtube.com/watch?v=abcdefghijk", "https://youtube.com.evil.example/@x",
		"https://www.youtube.com/@a/videos", "javascript:alert(1)",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		r, err := ParseAddress(raw)
		if err != nil {
			return
		}
		set := 0
		if r.ChannelID != "" {
			set++
			if !fuzzChannelID.MatchString(r.ChannelID) {
				t.Fatalf("%q read as the channel %q", raw, r.ChannelID)
			}
		}
		if r.Handle != "" {
			set++
			if !fuzzHandle.MatchString(r.Handle) {
				t.Fatalf("%q read as the handle %q", raw, r.Handle)
			}
		}
		if r.VideoID != "" {
			set++
			if !fuzzVideoID.MatchString(r.VideoID) {
				t.Fatalf("%q read as the video %q", raw, r.VideoID)
			}
		}
		if set != 1 {
			t.Fatalf("%q read as %+v: not exactly one of a channel, a handle and a video", raw, r)
		}
	})
}
