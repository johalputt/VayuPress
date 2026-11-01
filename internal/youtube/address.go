// SPDX-License-Identifier: Apache-2.0

// Package youtube reads a YouTube channel for VayuPress: what a pasted address
// names, the channel's public feeds, and, with an operator's key, the YouTube
// Data API. It makes posts of nothing itself; cmd/vayupress does that.
//
// No address taken from input is ever fetched. A pasted address is parsed down
// to a channel id, a handle or a video id, each checked against its own
// pattern, and every URL this package asks for is built from those on one of
// three hosts (Fetchable). A feed's own links and thumbnail addresses are read
// for nothing but the ids in them, so a hostile feed cannot point a fetch
// anywhere.
package youtube

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	channelIDRe = regexp.MustCompile(`^UC[0-9A-Za-z_-]{22}$`)
	videoIDRe   = regexp.MustCompile(`^[0-9A-Za-z_-]{11}$`)
	// A handle is 3 to 30 letters, digits, underscores, hyphens and full stops.
	handleRe = regexp.MustCompile(`^[0-9A-Za-z_.-]{3,30}$`)
)

// ValidChannelID reports whether s is a channel id, UC and 22 more characters.
func ValidChannelID(s string) bool { return channelIDRe.MatchString(s) }

// ValidVideoID reports whether s is an 11-character video id.
func ValidVideoID(s string) bool { return videoIDRe.MatchString(s) }

// Ref is what a pasted address names: exactly one of a channel id, a handle or
// a video id.
type Ref struct {
	ChannelID string
	Handle    string // without the @
	VideoID   string
}

// ErrNotYouTube is returned for an address on any host but YouTube's.
var ErrNotYouTube = errors.New("that is not a YouTube address")

// ErrUnrecognised is returned for a YouTube address that names no channel or
// video this package can follow.
var ErrUnrecognised = errors.New("that address names no YouTube channel or video")

// pastedHosts are the hosts a pasted address may be on. They are compared
// whole: a host that merely contains "youtube.com" is someone else's.
var pastedHosts = map[string]bool{
	"youtube.com": true, "www.youtube.com": true, "m.youtube.com": true,
	"music.youtube.com": true, "youtu.be": true,
}

// ParseAddress reads what an operator pasted: a channel, handle or video
// address, or a bare "@handle" or channel id.
func ParseAddress(raw string) (Ref, error) {
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		return Ref{}, ErrUnrecognised
	case strings.HasPrefix(s, "@"):
		return handleRef(s[1:])
	case ValidChannelID(s):
		return Ref{ChannelID: s}, nil
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return Ref{}, ErrUnrecognised
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return Ref{}, ErrNotYouTube
	}
	host := strings.ToLower(u.Hostname())
	if !pastedHosts[host] {
		return Ref{}, ErrNotYouTube
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if host == "youtu.be" {
		return videoRef(parts[0])
	}
	switch first := parts[0]; {
	case strings.HasPrefix(first, "@"):
		return handleRef(first[1:])
	case first == "channel" && len(parts) > 1:
		if ValidChannelID(parts[1]) {
			return Ref{ChannelID: parts[1]}, nil
		}
	case first == "watch":
		return videoRef(u.Query().Get("v"))
	case (first == "shorts" || first == "live" || first == "embed") && len(parts) > 1:
		return videoRef(parts[1])
	case (first == "c" || first == "user") && len(parts) > 1:
		// A legacy name is resolved like a handle: YouTube serves the same page
		// for /c/name and /@name, and the API's forHandle takes it.
		return handleRef(parts[1])
	case len(parts) == 1 && !reservedPaths[first]:
		// youtube.com/name, the oldest custom address, still opens the channel.
		return handleRef(first)
	}
	return Ref{}, ErrUnrecognised
}

// reservedPaths are YouTube's own pages, never a channel's short name.
var reservedPaths = map[string]bool{
	"watch": true, "feed": true, "results": true, "playlist": true, "shorts": true, "live": true,
	"channel": true, "c": true, "user": true, "embed": true, "about": true, "account": true,
	"premium": true, "music": true, "gaming": true, "kids": true, "t": true, "redirect": true,
}

func handleRef(h string) (Ref, error) {
	if !handleRe.MatchString(h) {
		return Ref{}, ErrUnrecognised
	}
	return Ref{Handle: h}, nil
}

func videoRef(id string) (Ref, error) {
	if !ValidVideoID(id) {
		return Ref{}, ErrUnrecognised
	}
	return Ref{VideoID: id}, nil
}

// The three hosts anything is fetched from.
const (
	pageHost  = "www.youtube.com"
	apiHost   = "youtube.googleapis.com"
	thumbHost = "i.ytimg.com"
)

// Fetchable reports whether u may be fetched: HTTPS, on one of the three
// hosts (compared with any port, so a port is refused too), with no
// credentials. The fetcher checks it before the
// request and again on the address any redirect ended at.
func Fetchable(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.User != nil {
		return false
	}
	switch p.Host {
	case pageHost, apiHost, thumbHost:
		return true
	}
	return false
}

// FeedURL is the public feed of a channel's newest uploads.
func FeedURL(channelID string) string {
	return "https://" + pageHost + "/feeds/videos.xml?channel_id=" + channelID
}

// UploadsFeedURL is the public feed of a channel's uploads playlist (UU),
// the same videos as FeedURL's. YouTube's channel feed has answered 404 for
// channels that exist while this one answered, so it stands in.
func UploadsFeedURL(channelID string) string {
	return "https://" + pageHost + "/feeds/videos.xml?playlist_id=UU" + channelID[2:]
}

// ShortsFeedURL is the public feed of a channel's newest Shorts: its UUSH
// playlist, the channel id with its UC prefix swapped.
func ShortsFeedURL(channelID string) string {
	return "https://" + pageHost + "/feeds/videos.xml?playlist_id=UUSH" + channelID[2:]
}

// LongFeedURL is the public feed of a channel's newest videos that are not
// Shorts: its UULF playlist.
func LongFeedURL(channelID string) string {
	return "https://" + pageHost + "/feeds/videos.xml?playlist_id=UULF" + channelID[2:]
}

// HandlePageURL is a handle's public channel page, read only for its id.
func HandlePageURL(handle string) string {
	return "https://" + pageHost + "/@" + url.PathEscape(handle)
}

// VideoPageURL is a video's public page, read only for its channel's id.
func VideoPageURL(videoID string) string {
	return "https://" + pageHost + "/watch?v=" + videoID
}

// WatchURL is where a post links for its video.
func WatchURL(videoID string) string { return VideoPageURL(videoID) }

// ShortURL is where a post links for a Short.
func ShortURL(videoID string) string { return "https://" + pageHost + "/shorts/" + videoID }

// ChannelURL is a channel's address.
func ChannelURL(channelID string) string { return "https://" + pageHost + "/channel/" + channelID }

// ThumbURLs are a video's cover pictures, largest first: the largest is
// missing for many videos, and the fetcher falls through to the next.
func ThumbURLs(videoID string) []string {
	base := "https://" + thumbHost + "/vi/" + videoID + "/"
	return []string{base + "maxresdefault.jpg", base + "sddefault.jpg", base + "hqdefault.jpg"}
}
