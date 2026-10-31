// SPDX-License-Identifier: Apache-2.0

package youtube

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Poster makes and rewrites the posts of a site. cmd/vayupress gives the one
// that goes through the post queue, the media library and the publish path.
type Poster interface {
	// Post makes a video's post on a site, as a draft or published, and
	// returns its slug.
	Post(ctx context.Context, domainID string, v Video, draft bool) (string, error)
	// Refresh rewrites a post made ahead of a premiere or stream as the video
	// it became.
	Refresh(ctx context.Context, slug string, v Video) error
}

// Follower checks followed channels and makes their posts.
type Follower struct {
	Store  *Store
	Get    Getter                       // public pages and feeds
	Key    func(context.Context) string // the operator's API key, or ""
	Poster Poster
}

// ErrConsent is returned when YouTube answered with its consent page rather
// than the channel's, as it does to some servers.
var ErrConsent = errors.New("YouTube asked this server to accept its cookies first; paste the channel's /channel/UC… address, or add a YouTube API key")

func (fw *Follower) api(ctx context.Context) (API, bool) {
	if fw.Key == nil {
		return API{}, false
	}
	k := strings.TrimSpace(fw.Key(ctx))
	return API{Key: k, Get: fw.Get}, k != ""
}

// Resolve finds the channel a pasted address names, with what its feed says
// of it and its newest videos, for the operator to confirm.
func (fw *Follower) Resolve(ctx context.Context, raw string) (Channel, []Video, error) {
	ref, err := ParseAddress(raw)
	if err != nil {
		return Channel{}, nil, err
	}
	id := ref.ChannelID
	api, keyed := fw.api(ctx)
	switch {
	case id != "":
	case keyed && ref.Handle != "":
		ch, err := api.ChannelByHandle(ctx, ref.Handle)
		if err != nil {
			return Channel{}, nil, err
		}
		id = ch.ID
	case keyed:
		vs, err := api.Videos(ctx, []string{ref.VideoID})
		if err != nil {
			return Channel{}, nil, err
		}
		if len(vs) == 0 {
			return Channel{}, nil, ErrUnrecognised
		}
		id = vs[0].ChannelID
	case ref.Handle != "":
		if id, err = fw.pageID(ctx, HandlePageURL(ref.Handle), ChannelIDFromPage); err != nil {
			return Channel{}, nil, err
		}
	default:
		if id, err = fw.pageID(ctx, VideoPageURL(ref.VideoID), ChannelIDFromVideoPage); err != nil {
			return Channel{}, nil, err
		}
	}
	ch, vids, err := fw.feed(ctx, FeedURL(id), id)
	if err != nil {
		return Channel{}, nil, err
	}
	if keyed {
		if full, err := api.ChannelByID(ctx, id); err == nil {
			ch.Videos = full.Videos
		}
	}
	return ch, vids, nil
}

func (fw *Follower) pageID(ctx context.Context, u string, read func([]byte) (string, error)) (string, error) {
	status, body, err := fw.Get(ctx, u)
	if err != nil {
		return "", err
	}
	if status == 404 {
		return "", ErrUnrecognised
	}
	if status != 200 {
		return "", fmt.Errorf("YouTube answered %d", status)
	}
	id, err := read(body)
	if errors.Is(err, ErrNoChannelOnPage) {
		return "", ErrConsent
	}
	return id, err
}

// feed reads one feed. A playlist feed that does not exist (a channel with no
// Shorts has no Shorts playlist) is an empty one.
func (fw *Follower) feed(ctx context.Context, u, channelID string) (Channel, []Video, error) {
	status, body, err := fw.Get(ctx, u)
	if err != nil {
		return Channel{}, nil, err
	}
	switch status {
	case 200:
		return ParseFeed(body, channelID)
	case 404:
		if strings.Contains(u, "playlist_id=") {
			return Channel{ID: channelID}, nil, nil
		}
		return Channel{}, nil, errors.New("YouTube has no channel by that id now")
	}
	return Channel{}, nil, fmt.Errorf("YouTube answered %d", status)
}

// latest is a channel's newest videos from its feeds: every upload, with the
// Shorts told apart by the Shorts playlist, and the newest of each playlist
// that the all-uploads feed is too short to reach.
func (fw *Follower) latest(ctx context.Context, channelID string) (Channel, []Video, error) {
	ch, all, err := fw.feed(ctx, FeedURL(channelID), channelID)
	if err != nil {
		return Channel{}, nil, err
	}
	_, shorts, err := fw.feed(ctx, ShortsFeedURL(channelID), channelID)
	if err != nil {
		return Channel{}, nil, err
	}
	_, long, err := fw.feed(ctx, LongFeedURL(channelID), channelID)
	if err != nil {
		return Channel{}, nil, err
	}
	isShort := map[string]bool{}
	for _, v := range shorts {
		isShort[v.ID] = true
	}
	seen := map[string]bool{}
	var out []Video
	for _, set := range [][]Video{all, shorts, long} {
		for _, v := range set {
			if seen[v.ID] {
				continue
			}
			seen[v.ID] = true
			if isShort[v.ID] {
				v.Kind = KindShort
			}
			out = append(out, v)
		}
	}
	return ch, out, nil
}

// Check reads a followed channel and makes a post of each video it has not
// seen, as the channel is set to: published or a draft, or held for Post when
// auto-post is off, and a Short skipped while Shorts are off. It records the
// check and returns how many videos were new.
func (fw *Follower) Check(ctx context.Context, f Followed) (int, error) {
	n, err := fw.check(ctx, f)
	if rerr := fw.Store.Checked(ctx, f, n, err); rerr != nil && err == nil {
		err = rerr
	}
	return n, err
}

func (fw *Follower) check(ctx context.Context, f Followed) (int, error) {
	ch, vids, err := fw.latest(ctx, f.ChannelID)
	if err != nil {
		return 0, err
	}
	if ch.Title != "" && ch.Title != f.Title {
		_ = fw.Store.SetTitle(ctx, f.ID, ch.Title)
	}
	api, keyed := fw.api(ctx)
	if keyed {
		// The back catalogue waits when the API refuses (a spent quota, a
		// revoked key) and goes on at a later check; new videos keep coming
		// from the feeds meanwhile, which need no key.
		if more, err := fw.backfill(ctx, api, f); err == nil {
			vids = append(vids, more...)
		}
	}
	fresh, err := fw.unseen(ctx, f, vids)
	if err != nil {
		return 0, err
	}
	if keyed && len(fresh) > 0 {
		fresh = fw.enrich(ctx, api, fresh)
	}
	// Oldest first, so a channel's posts are made in the order it made them.
	sort.SliceStable(fresh, func(i, j int) bool { return fresh[i].Published.Before(fresh[j].Published) })
	var failed error
	for _, v := range fresh {
		if err := fw.take(ctx, f, v); err != nil && failed == nil {
			failed = err
		}
	}
	if keyed {
		if err := fw.aired(ctx, api, f); err != nil && failed == nil {
			failed = err
		}
	}
	return len(fresh), failed
}

// unseen is the videos the site has no row for, each once.
func (fw *Follower) unseen(ctx context.Context, f Followed, vids []Video) ([]Video, error) {
	ids := make([]string, 0, len(vids))
	for _, v := range vids {
		ids = append(ids, v.ID)
	}
	known, err := fw.Store.Known(ctx, f.DomainID, ids)
	if err != nil {
		return nil, err
	}
	var out []Video
	for _, v := range vids {
		if !known[v.ID] {
			known[v.ID] = true
			out = append(out, v)
		}
	}
	return out, nil
}

// enrich reads new videos through the API, 50 at a time: the whole
// description, and whether each is a premiere or stream yet to start. A video
// keeps what its feed said when the API cannot be asked.
func (fw *Follower) enrich(ctx context.Context, api API, vids []Video) []Video {
	byID := map[string]Video{}
	for i := 0; i < len(vids); i += 50 {
		ids := make([]string, 0, 50)
		for _, v := range vids[i:min(i+50, len(vids))] {
			ids = append(ids, v.ID)
		}
		got, err := api.Videos(ctx, ids)
		if err != nil {
			break
		}
		for _, v := range got {
			byID[v.ID] = v
		}
	}
	for i, v := range vids {
		w, ok := byID[v.ID]
		if !ok || w.ChannelID != v.ChannelID {
			continue
		}
		// The API does not say what is a Short; the Shorts playlist did.
		if v.Kind == KindShort && w.Kind == KindVideo {
			w.Kind = KindShort
		}
		if w.Published.IsZero() {
			w.Published = v.Published
		}
		vids[i] = w
	}
	return vids
}

// take records one new video and, when the channel says so, makes its post.
func (fw *Follower) take(ctx context.Context, f Followed, v Video) error {
	state := StateClaimed
	switch {
	case v.Kind == KindShort && !f.Shorts:
		state = StateSkipped
	case !f.AutoPost:
		state = StateHeld
	}
	made, err := fw.Store.Claim(ctx, f.DomainID, f.ID, v, state)
	if err != nil || !made || state != StateClaimed {
		return err
	}
	return fw.post(ctx, f, v)
}

// post makes the post of a video already claimed for it.
func (fw *Follower) post(ctx context.Context, f Followed, v Video) error {
	slug, err := fw.Poster.Post(ctx, f.DomainID, v, f.AsDraft)
	if err != nil {
		_ = fw.Store.Release(ctx, f.DomainID, v.ID)
		return fmt.Errorf("a post of %q could not be made: %w", v.Title, err)
	}
	state := StatePosted
	if v.Kind == KindUpcoming || v.Kind == KindLive {
		state = StateUpcoming
	}
	return fw.Store.Settle(ctx, f.DomainID, v.ID, slug, state)
}

// PostHeld makes the post of a video held while auto-post was off.
func (fw *Follower) PostHeld(ctx context.Context, f Followed, videoID string) error {
	row, err := fw.Store.Video(ctx, f.DomainID, videoID)
	if err != nil {
		return err
	}
	if row.State != StateHeld && row.State != StateSkipped {
		return errors.New("that video has a post already")
	}
	v := Video{ID: row.VideoID, ChannelID: f.ChannelID, Title: row.Title, Published: row.Published, Kind: row.Kind, Start: row.Start}
	if api, keyed := fw.api(ctx); keyed {
		if got, err := api.Videos(ctx, []string{videoID}); err == nil && len(got) == 1 && got[0].ChannelID == f.ChannelID {
			kind := v.Kind
			v = got[0]
			if kind == KindShort && v.Kind == KindVideo {
				v.Kind = KindShort
			}
		}
	} else if _, vids, err := fw.latest(ctx, f.ChannelID); err == nil {
		for _, w := range vids {
			if w.ID == videoID {
				v.Description = w.Description
			}
		}
	}
	if err := fw.Store.Settle(ctx, f.DomainID, videoID, "", StateClaimed); err != nil {
		return err
	}
	return fw.post(ctx, f, v)
}

// aired rewrites each post made ahead of a premiere or stream, once the API
// says it has aired.
func (fw *Follower) aired(ctx context.Context, api API, f Followed) error {
	rows, err := fw.Store.Started(ctx, f.DomainID)
	if err != nil || len(rows) == 0 {
		return err
	}
	ids := make([]string, 0, len(rows))
	slugs := map[string]string{}
	for _, r := range rows {
		ids = append(ids, r.VideoID)
		slugs[r.VideoID] = r.Slug
	}
	got, err := api.Videos(ctx, ids[:min(50, len(ids))])
	if err != nil {
		return err
	}
	for _, v := range got {
		if v.ChannelID != f.ChannelID || v.Kind == KindUpcoming || v.Kind == KindLive {
			continue
		}
		if err := fw.Poster.Refresh(ctx, slugs[v.ID], v); err != nil {
			return err
		}
		if err := fw.Store.Settle(ctx, f.DomainID, v.ID, slugs[v.ID], StatePosted); err != nil {
			return err
		}
	}
	return nil
}

// backfillPages is how many pages of the back catalogue one check reads: 150
// videos for six units of quota, so a channel of thousands is read over a
// few hours without spending a day's quota at once.
const backfillPages = 3

// The back catalogue is read playlist by playlist, Shorts first so each video
// is known for what it is: the Shorts, the long videos, the past streams.
var backfillLists = []struct{ prefix, kind string }{{"UUSH", KindShort}, {"UULF", KindVideo}, {"UULV", KindVideo}}

// backfill reads the next pages of a channel's back catalogue through the API
// and returns the videos on them; where it got to is kept on the channel.
func (fw *Follower) backfill(ctx context.Context, api API, f Followed) ([]Video, error) {
	if f.BackfillDone {
		return nil, nil
	}
	list, token := 0, ""
	if i := strings.IndexByte(f.BackfillToken, ':'); i > 0 {
		_, _ = fmt.Sscan(f.BackfillToken[:i], &list)
		token = f.BackfillToken[i+1:]
	}
	seen, total := f.BackfillSeen, f.BackfillTotal
	var out []Video
	for page := 0; page < backfillPages && list < len(backfillLists); page++ {
		bl := backfillLists[list]
		ids, next, n, err := api.PlaylistPage(ctx, bl.prefix+f.ChannelID[2:], token)
		if err != nil {
			return out, err
		}
		if token == "" {
			total += n
		}
		vs, err := api.Videos(ctx, ids)
		if err != nil {
			return out, err
		}
		for _, v := range vs {
			if v.ChannelID != f.ChannelID {
				continue
			}
			if bl.kind == KindShort {
				v.Kind = KindShort
			}
			out = append(out, v)
		}
		seen += len(ids)
		token = next
		if token == "" {
			list++
		}
	}
	done := list >= len(backfillLists)
	saved := ""
	if !done {
		saved = fmt.Sprint(list) + ":" + token
	}
	return out, fw.Store.Backfilled(ctx, f.ID, saved, seen, total, done)
}
