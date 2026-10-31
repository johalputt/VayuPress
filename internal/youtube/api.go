// SPDX-License-Identifier: Apache-2.0

package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Getter fetches one URL that Fetchable allowed and returns its status and
// body. cmd/vayupress gives one built on internal/safefetch.
type Getter func(ctx context.Context, rawURL string) (status int, body []byte, err error)

// API is the YouTube Data API v3 with an operator's key. Every call costs one
// unit of the key's daily quota (10,000 by default); nothing here uses search,
// which costs a hundred.
type API struct {
	Key string
	Get Getter
}

// APIError is the API's refusal. It never carries the request's address, which
// holds the key.
type APIError struct {
	Status int
	Reason string // the API's own reason, as "quotaExceeded" or "keyInvalid"
}

func (e *APIError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("the YouTube API refused the request (%d, %s)", e.Status, e.Reason)
	}
	return fmt.Sprintf("the YouTube API refused the request (%d)", e.Status)
}

// QuotaSpent reports whether the key's daily quota is used up, which lifts at
// midnight Pacific time.
func (e *APIError) QuotaSpent() bool {
	return e.Reason == "quotaExceeded" || e.Reason == "dailyLimitExceeded" || e.Reason == "rateLimitExceeded"
}

// errFetch stands for a request that never got an answer. The underlying error
// is not kept: a transport error quotes the URL, and the URL holds the key.
var errFetch = errors.New("the YouTube API could not be reached")

func (a API) call(ctx context.Context, resource string, q url.Values, out any) error {
	q.Set("key", a.Key)
	u := "https://" + apiHost + "/youtube/v3/" + resource + "?" + q.Encode()
	status, body, err := a.Get(ctx, u)
	if err != nil {
		return errFetch
	}
	if status != 200 {
		var e struct {
			Error struct {
				Errors []struct {
					Reason string `json:"reason"`
				} `json:"errors"`
			} `json:"error"`
		}
		reason := ""
		if json.Unmarshal(body, &e) == nil && len(e.Error.Errors) > 0 {
			reason = e.Error.Errors[0].Reason
			if !reasonRe(reason) {
				reason = ""
			}
		}
		return &APIError{Status: status, Reason: reason}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.New("the YouTube API's answer could not be read")
	}
	return nil
}

// reasonRe keeps a reason to the API's own word shape, so nothing else the
// body held reaches a page.
func reasonRe(s string) bool {
	if len(s) == 0 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

type apiChannels struct {
	Items []struct {
		ID      string `json:"id"`
		Snippet struct {
			Title string `json:"title"`
		} `json:"snippet"`
		Statistics struct {
			VideoCount string `json:"videoCount"`
		} `json:"statistics"`
	} `json:"items"`
}

func (c apiChannels) first() (Channel, error) {
	if len(c.Items) == 0 || !ValidChannelID(c.Items[0].ID) {
		return Channel{}, ErrUnrecognised
	}
	it := c.Items[0]
	n := 0
	_, _ = fmt.Sscan(it.Statistics.VideoCount, &n)
	return Channel{ID: it.ID, Title: clip(it.Snippet.Title, maxTitleRunes), Videos: n}, nil
}

// ChannelByHandle resolves a handle to its channel.
func (a API) ChannelByHandle(ctx context.Context, handle string) (Channel, error) {
	var r apiChannels
	if err := a.call(ctx, "channels", url.Values{"part": {"snippet,statistics"}, "forHandle": {"@" + handle}}, &r); err != nil {
		return Channel{}, err
	}
	return r.first()
}

// ChannelByID reads a channel by its id.
func (a API) ChannelByID(ctx context.Context, id string) (Channel, error) {
	var r apiChannels
	if err := a.call(ctx, "channels", url.Values{"part": {"snippet,statistics"}, "id": {id}}, &r); err != nil {
		return Channel{}, err
	}
	return r.first()
}

// PlaylistPage is one page of a playlist's video ids, 50 at most, with the
// token for the next page ("" after the last) and the playlist's length.
func (a API) PlaylistPage(ctx context.Context, playlistID, token string) (ids []string, next string, total int, err error) {
	q := url.Values{"part": {"contentDetails"}, "maxResults": {"50"}, "playlistId": {playlistID}}
	if token != "" {
		q.Set("pageToken", token)
	}
	var r struct {
		NextPageToken string `json:"nextPageToken"`
		PageInfo      struct {
			TotalResults int `json:"totalResults"`
		} `json:"pageInfo"`
		Items []struct {
			ContentDetails struct {
				VideoID string `json:"videoId"`
			} `json:"contentDetails"`
		} `json:"items"`
	}
	if err := a.call(ctx, "playlistItems", q, &r); err != nil {
		var ae *APIError
		// A channel with no Shorts has no Shorts playlist: an empty page.
		if errors.As(err, &ae) && ae.Status == 404 {
			return nil, "", 0, nil
		}
		return nil, "", 0, err
	}
	for _, it := range r.Items {
		if ValidVideoID(it.ContentDetails.VideoID) {
			ids = append(ids, it.ContentDetails.VideoID)
		}
	}
	if len(r.NextPageToken) > 200 {
		r.NextPageToken = ""
	}
	return ids, r.NextPageToken, r.PageInfo.TotalResults, nil
}

// Videos reads up to 50 videos: title, description, date, and whether each is
// a premiere or stream still to start, or on now.
func (a API) Videos(ctx context.Context, ids []string) ([]Video, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 50 {
		ids = ids[:50]
	}
	var r struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				ChannelID            string `json:"channelId"`
				Title                string `json:"title"`
				Description          string `json:"description"`
				PublishedAt          string `json:"publishedAt"`
				LiveBroadcastContent string `json:"liveBroadcastContent"`
			} `json:"snippet"`
			Live struct {
				ScheduledStartTime string `json:"scheduledStartTime"`
			} `json:"liveStreamingDetails"`
		} `json:"items"`
	}
	if err := a.call(ctx, "videos", url.Values{"part": {"snippet,liveStreamingDetails"}, "id": {strings.Join(ids, ",")}}, &r); err != nil {
		return nil, err
	}
	var out []Video
	for _, it := range r.Items {
		if !ValidVideoID(it.ID) || !ValidChannelID(it.Snippet.ChannelID) {
			continue
		}
		pub, _ := time.Parse(time.RFC3339, it.Snippet.PublishedAt)
		v := Video{
			ID: it.ID, ChannelID: it.Snippet.ChannelID,
			Title:       clip(it.Snippet.Title, maxTitleRunes),
			Description: clip(it.Snippet.Description, maxDescRunes),
			Published:   pub.UTC(), Kind: KindVideo,
		}
		switch it.Snippet.LiveBroadcastContent {
		case "upcoming":
			v.Kind = KindUpcoming
		case "live":
			v.Kind = KindLive
		}
		if st, err := time.Parse(time.RFC3339, it.Live.ScheduledStartTime); err == nil {
			v.Start = st.UTC()
		}
		out = append(out, v)
	}
	return out, nil
}

// UploadsPlaylist is a channel's every upload, UU and the id's tail.
func UploadsPlaylist(channelID string) string { return "UU" + channelID[2:] }

// ShortsPlaylist is a channel's Shorts, UUSH and the id's tail.
func ShortsPlaylist(channelID string) string { return "UUSH" + channelID[2:] }
