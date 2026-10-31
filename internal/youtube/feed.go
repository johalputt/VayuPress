// SPDX-License-Identifier: Apache-2.0

package youtube

import (
	"bytes"
	"encoding/xml"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Kinds of video, as a post is made of each.
const (
	KindVideo    = "video"
	KindShort    = "short"
	KindUpcoming = "upcoming" // a premiere or stream that has not started
	KindLive     = "live"     // a stream that is on now
)

// Video is one upload, as read from a feed or the API.
type Video struct {
	ID          string
	ChannelID   string
	Title       string
	Description string
	Published   time.Time
	Kind        string
	Start       time.Time // a premiere's or stream's scheduled start
}

// Channel is what a feed or the API says of a channel.
type Channel struct {
	ID     string
	Title  string
	Videos int // the channel's own count, from the API; 0 when unknown
}

// Limits on what a feed may make this package hold. A feed is 15 entries;
// the cap is there for one that is not what it says.
const (
	MaxFeedBytes   = 4 << 20
	MaxFeedEntries = 50
	maxTitleRunes  = 300
	maxDescRunes   = 10000 // YouTube's own limit is 5000
)

// ErrNotAFeed is returned for a body that is not a YouTube Atom feed.
var ErrNotAFeed = errors.New("the answer was not a YouTube feed")

type atomFeed struct {
	XMLName   xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	ChannelID string      `xml:"http://www.youtube.com/xml/schemas/2015 channelId"`
	Title     string      `xml:"http://www.w3.org/2005/Atom title"`
	Entries   []atomEntry `xml:"http://www.w3.org/2005/Atom entry"`
}

type atomEntry struct {
	VideoID   string `xml:"http://www.youtube.com/xml/schemas/2015 videoId"`
	ChannelID string `xml:"http://www.youtube.com/xml/schemas/2015 channelId"`
	Title     string `xml:"http://www.w3.org/2005/Atom title"`
	Published string `xml:"http://www.w3.org/2005/Atom published"`
	Group     struct {
		Description string `xml:"http://search.yahoo.com/mrss/ description"`
	} `xml:"http://search.yahoo.com/mrss/ group"`
}

// ParseFeed reads a channel or playlist feed. Entries for another channel,
// with an id that is not a video id, or past MaxFeedEntries are left out; a
// feed for a channel other than want (when want is set) is refused whole.
func ParseFeed(body []byte, want string) (Channel, []Video, error) {
	if len(body) > MaxFeedBytes {
		return Channel{}, nil, ErrNotAFeed
	}
	var f atomFeed
	d := xml.NewDecoder(bytes.NewReader(body))
	d.Strict = true
	if err := d.Decode(&f); err != nil {
		return Channel{}, nil, ErrNotAFeed
	}
	if !ValidChannelID(f.ChannelID) {
		return Channel{}, nil, ErrNotAFeed
	}
	if want != "" && f.ChannelID != want {
		return Channel{}, nil, errors.New("the feed is for another channel")
	}
	ch := Channel{ID: f.ChannelID, Title: clip(f.Title, maxTitleRunes)}
	var out []Video
	for _, e := range f.Entries {
		if len(out) == MaxFeedEntries {
			break
		}
		if !ValidVideoID(e.VideoID) || e.ChannelID != f.ChannelID {
			continue
		}
		pub, _ := time.Parse(time.RFC3339, strings.TrimSpace(e.Published))
		out = append(out, Video{
			ID:          e.VideoID,
			ChannelID:   e.ChannelID,
			Title:       clip(e.Title, maxTitleRunes),
			Description: clip(e.Group.Description, maxDescRunes),
			Published:   pub.UTC(),
			Kind:        KindVideo,
		})
	}
	return ch, out, nil
}

// The ways a channel page names its own id, most specific first.
var pageIDRes = []*regexp.Regexp{
	regexp.MustCompile(`<link rel="canonical" href="https://www\.youtube\.com/channel/(UC[0-9A-Za-z_-]{22})"`),
	regexp.MustCompile(`<meta itemprop="(?:channelId|identifier)" content="(UC[0-9A-Za-z_-]{22})"`),
	regexp.MustCompile(`"externalId":"(UC[0-9A-Za-z_-]{22})"`),
}

// videoPageIDRe is how a video's page names its channel.
var videoPageIDRe = regexp.MustCompile(`"channelId":"(UC[0-9A-Za-z_-]{22})"`)

// ErrNoChannelOnPage is returned when a page names no channel, as YouTube's
// consent page does.
var ErrNoChannelOnPage = errors.New("the page did not name its channel")

// ChannelIDFromPage reads a handle's channel page for the channel's id.
func ChannelIDFromPage(body []byte) (string, error) {
	for _, re := range pageIDRes {
		if m := re.FindSubmatch(body); m != nil {
			return string(m[1]), nil
		}
	}
	return "", ErrNoChannelOnPage
}

// ChannelIDFromVideoPage reads a video's page for its channel's id.
func ChannelIDFromVideoPage(body []byte) (string, error) {
	if m := videoPageIDRe.FindSubmatch(body); m != nil {
		return string(m[1]), nil
	}
	return "", ErrNoChannelOnPage
}

// clip trims s and cuts it to n runes, dropping invalid UTF-8.
func clip(s string, n int) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n]))
}
