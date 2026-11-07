# ADR-0177 — A post as Markdown for agents, from the post's own content

- **Status:** Accepted
- **Date:** 2026-11-07
- **Code:** `internal/mdrender`, `cmd/vayupress/markdown_pages.go`, `cmd/vayupress/mcp_server.go` (`get_post`).

## 0. The question

Agents read a site through VayuMCP and the public pages. A post's page is
built for a reader's browser: johal.in's posts carry their own style sheet,
code-copy buttons and layout, so `get_post` returned tens of kilobytes of HTML
and CSS around a few kilobytes of text, and a page fetched from the web
carried the theme and its scripts too.

## 1. Decision

**One model, two renderings.** A post's Markdown is made from what its HTML
is made from: an editor post from its blocks (whose text is already the
inline Markdown the HTML renderer converts), a post written as HTML from
that HTML. Nothing reads a rendered page, so nothing a theme, a script or
the page around a post adds reaches the Markdown, and the two cannot drift.

**Two public addresses.** `/<slug>.md` is a post; `/index.md` is the site's
newest thirty posts. A slug never holds a dot, so neither shadows a post.
They answer only for a published post, on its own site, that a signed-out
reader may read; anything else is Not found, never a preview. VayuMCP's
`get_post` takes `format: "markdown"`, with HTML still the default because
`update_post` edits that.

**The page cache's discipline.** Each Markdown file is written beside its
post's page and removed by the same purges (`CachePurgePost`, `CachePurge`,
`CachePurgeListings`), and honours the install-wide stale mark. Its
addresses name the site's registered host, never the request's `Host`
header: the file is cached, so a forged header would otherwise reach every
reader. It is served as `text/markdown` with `nosniff` and a policy that
allows nothing.

## 2. Consequences

- `llms.txt` stays as it is; it can list the `.md` addresses later.
- Embeds become links, galleries a list of images, diagrams their source in
  a code block: an agent loses no text a reader has, only the drawing.
- The view models for topics and search come later, in the same package.
