# A YouTube channel as a blog

Content › YouTube makes a YouTube channel a site's blog. Every video the
channel has posted becomes a post, and so does every video it posts from then
on, with nothing done by hand. This guide is for the operator: how to follow
a channel, what a post holds, the switches, what the API key adds, and what
the feature fetches. The decision behind it is ADR-0175.

## Follow a channel

1. Open **Content › YouTube** and press **Follow a channel**.
2. Paste the channel's address. Any of these works: its page
   (`youtube.com/@name`, `youtube.com/channel/UC…`, `youtube.com/c/name`), its
   handle alone (`@name`), or the address of any of its videos or Shorts.
3. Choose the site to post on: this site, one of your hosted blogs, or **A new
   site…** with its address. A new site is made in the same step; it shows its
   posts once its address points to this server and its certificate is issued
   (Site › Domains).
4. Press **Find the channel**. The channel is shown with its name, how many
   videos it has, and its newest titles. Check it is the right one, set the
   switches, and press **Follow**.

The first check runs at once: the channel's newest videos and Shorts become
posts within a minute or two.

## What a post holds

- The video's **title**.
- The **player**, which shows the video's picture and loads YouTube's player
  (from `youtube-nocookie.com`) only when a reader presses play. A Short is
  drawn upright.
- The **description**, as text: its paragraphs and line breaks kept, its web
  addresses as links, and its timestamps (`3:15`, `1:02:03`) as links that
  open the video at that moment. Nothing in it is read as markup.
- The description's **hashtags** as the post's tags (up to ten), and a
  `videos` or `shorts` tag.
- The video's **picture**, copied into your media library as the post's
  image. It is not loaded from YouTube by your readers.
- The video's **date** as the post's date, the video's address as its
  canonical link, and a **Watch on YouTube** link.

A post is an ordinary post: open it in the editor to change anything. A
video is posted once. A post you delete is not made again, and two checks
running at once never make two posts.

## The switches

Each followed channel has its own:

| Switch | On (the default) | Off |
| --- | --- | --- |
| Auto-post | New videos become posts by themselves | New videos wait under **Waiting for Post**, each with **Post**, and **Post all** |
| Publish at once | Posts are published | Posts are kept as drafts for you to publish |
| Shorts | Shorts become posts | Shorts are left out |

**Pause** stops checking the channel; **Resume** checks it at once.
**Fetch now** checks it at once rather than at its next check. **Stop
following** ends it; its posts stay.

## How often it is checked

Every 15 minutes, the channel's own feed's freshness, spread so many channels
are not checked in the same second. A check that fails waits 15 minutes, then
an hour, then six hours, then a day, and the channel's page says why it
failed. A channel failing three checks in a row is one notice in the bell,
gone once it reads again.

## Without a key, and with one

**Without a key**, a channel is read from its public feeds: every upload, its
Shorts, and its long videos. That gives its newest videos when it is first
followed, and every new one after.

**With a YouTube Data API key** (Settings › API keys › YouTube Data API; it is
kept sealed and only ever sent to YouTube):

- the **back catalogue** is read too, three pages of fifty videos at each
  check, so the page shows "312 of 1,140 read so far" until it is whole;
- **premieres and streams** are posted before they start, saying when, and
  rewritten as the video once they have aired;
- each new video's **whole description** comes from the API.

A key is made in Google Cloud with the YouTube Data API v3 enabled. Reading a
channel this way costs a few units of its free daily quota per check. When
the quota is spent, or the key is refused, the back catalogue waits and goes
on at a later check; new videos keep coming from the feeds, which need no
key.

## What is fetched

Only three hosts, over HTTPS: `www.youtube.com` (feeds, and a channel's or
video's page to find its channel), `youtube.googleapis.com` (the API, with a
key), and `i.ytimg.com` (pictures). Every address is built by VayuPress from
a checked channel or video id; nothing a pasted address or a feed names is
fetched, and an answer that a redirect ended anywhere else is refused.

YouTube sometimes answers a server with its cookie-consent page instead of a
channel's page. When it does, paste the channel's `/channel/UC…` address, or
add a key: both avoid the page.

## Who can use it

Following a channel makes posts on a site and reads the install's YouTube
key, so the page is an administrator's. It is not in the Tor world, whose
traffic goes through Tor.

Follow a channel you own or may republish. The posts show the video through
YouTube's own player and link back to it.
