# ADR-0175 — A YouTube channel as a blog, posting by itself

- **Status:** Accepted
- **Date:** 2026-10-31
- **Code:** `internal/youtube`, `cmd/vayupress/youtube.go`, `cmd/vayupress/admin_os_youtube.go`; migration 129.

## 0. The question

A creator whose work is on YouTube wants a site that carries it: every
video as a post, found by search engines, on their own domain, with nothing
to do by hand when a new video goes up. The site must stay the install's own
(no third party's script on its pages), and following a channel must not
become a way to make the server fetch anything else.

## 1. Decision

**Follow a channel; every video becomes a post through the editor's own
paths.** Content › YouTube takes a channel's address and a site (or a new
site, made in the same step), shows the channel to confirm, and follows it.
A post is the editor's own blocks: the click-to-load player
(`video-facade`, youtube-nocookie, already in the embed CSP), the
description, a link back. It is written by the post queue, its picture is
copied into the media library by `storeValidatedMedia`, and it is published
by `applyPostStatus`, so the cache purge, the feeds and the IndexNow ping
are the ones every post gets.

**The description is text, never markup.** Every ASCII punctuation mark is
escaped before it becomes Markdown; only http(s) addresses and timestamps
gain links, and hashtags become tags.

**The public feed before the API.** A channel's three public feeds (every
upload, its Shorts playlist `UUSH…`, its long videos `UULF…`) need no key and
no account, tell a Short from a video, and give the newest videos; that is
enough to follow a channel from now on. The YouTube Data API, with a key the
operator adds as a sealed credential, adds what the feed cannot give: the
back catalogue (three pages of fifty per check, so a large channel never
spends a day's quota at once) and premieres and streams, posted before they
start and rewritten once they have aired. Nothing is scraped to get round
the lack of a key.

**Only what it builds is fetched, from three hosts.** A pasted address is
parsed down to a channel id, handle or video id, each checked against its
own pattern; every URL is built from those on `www.youtube.com`,
`youtube.googleapis.com` or `i.ytimg.com`, over HTTPS (`youtube.Fetchable`).
The fetcher checks that before the request and again on the address any
redirect ended at. A feed's own links and picture addresses are read for
nothing. An API error never carries the request's address, which holds the
key.

**Each video is posted once.** A video's row (`youtube_videos`) is claimed
before its post is made, so two checks at once never make two posts, and is
kept after the post is deleted, so a deleted post is not made again. Each
site has its own rows.

**Nothing by hand, unless asked.** Auto-post and Publish at once are on by
default; per channel, auto-post can be turned off (new videos wait for Post
or Post all), posts can be kept as drafts, Shorts left out, the channel
paused. A channel is checked every fifteen minutes, spread over three, and
Fetch now checks it at once. A check that fails waits fifteen minutes, an
hour, six hours, then a day; a channel failing three in a row is one notice
in the bell.

**An administrator's page; never in the Tor world**, whose egress is Tor's.

## 2. Consequences

- A site made of a channel is an ordinary blog: its posts can be edited,
  retitled or deleted like any other, and a refresh after a premiere leaves
  the title and tags the operator gave it.
- A video removed from YouTube keeps its post; its player says so when
  pressed.
