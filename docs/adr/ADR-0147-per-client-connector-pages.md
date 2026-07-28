# ADR-0147 — One page per client; VayuMCP keeps the protocol

Status: Accepted (shipped in v3.15.87)
Date: 2026-07-28
Deciders: VayuPress core

## Context

`/os/connector` (VayuMCP, ADR-0139) grew to carry four "Connect a client"
accordions: one-click Connect on claude.ai, Claude Desktop, Claude Code CLI, and
a proxy/WAF reference. Then ADR-0146 added Buzz, and the obvious next move was a
fifth.

That shape has two problems, and they get worse with each client added.

**It makes a page about a protocol spend most of its height on one vendor.** A
reader connecting Cursor scrolled past three Claude sections to find the generic
JSON they needed — which was never given its own block, only implied by the
Claude Desktop example.

**It buries the best route.** The one-click path needs no key at all: Claude
signs in through this site's own OAuth 2.1 server and the operator approves a
scope on screen. There is no token to leak, paste wrongly, or forget to revoke.
It was one of four equal-looking accordions.

The Buzz page (ADR-0146) had already demonstrated the alternative: a client gets
its own page, in the Monetization design language, walking one audience through
one setup. It worked, and it left `/os/connector` with a Claude section that was
now the odd one out.

## Decision

**Each client with a non-trivial setup gets its own page. `/os/connector` keeps
the endpoint, the grants, the generic configuration, the proxy reference and the
list of connected clients.**

- `/os/claudecode` — **Claude Code** (new). Three routes in, best first:
  one-click on claude.ai (open by default, marked Recommended, needs no key),
  then the Claude Code CLI command, then the Claude Desktop config block.
- `/os/buzz` — Buzz agents (ADR-0146, unchanged).
- `/os/connector` — the protocol surface, plus signposts to both.

**VayuMCP remains the name of the connector.** These pages are named for the
*clients* they configure, exactly as the Buzz page is. Nothing is renamed to
"Claude Connector" — the naming rule in the contributor notes is about what the
connector is called, not about whether a client may have a setup page.

### Shared scaffolding, not a third copy

Two pages doing the same job would have meant two copies of the same
mint-key-then-fill-the-snippet controller; the Claude page would have made three,
and they drift the first time one is fixed. `admin_os_mcpclient.go` now holds the
mechanism once — element IDs, token banner, stat strip, grant tile, snippet
renderer, controller — and each page carries only its own prose and capability
presets.

The snippet contract changed with it. The old script rebuilt two specific blocks
by `id` from a `data-endpoint` attribute, so **every new snippet had to be taught
to the function or it silently kept its placeholder**. Snippets now carry their
own template in `data-tpl` with a `__KEY__` marker, and the controller rewrites
whatever it finds. `/os/connector` was moved onto the same mechanism.

### Two pages, two different defaults — deliberately

The Buzz page leads with **Author**; the Claude page leads with **Full control**.
That is not an inconsistency to be tidied away:

- A Buzz agent sits in a shared channel and acts for more than one person, so the
  safe grant should be the one that looks like the default.
- The Claude page's common case is an operator connecting their *own* assistant
  to their *own* site, where the point is that it can do the work.

Both are pinned by tests, in both directions, so neither can be "fixed" to match
the other without the reason being reconsidered.

### Counting

Each page counts only the keys it minted, by label prefix (`Claude …`,
`Buzz agent …`). A key granted to Claude is not a Buzz agent; an operator
auditing which clients can reach their site needs them separated rather than
summed. A test asserts the two do not cross-count, including that a Claude
full-control key does not raise the warning tone on the Buzz page.

## Consequences

**Good.** Each audience gets a page that is entirely theirs, and the keyless
route is now the first thing a Claude operator sees. `/os/connector` finally has
a generic configuration block of its own instead of implying it through a
vendor-specific example. Adding the next client is a new file plus a nav card,
not another accordion on a crowded page.

**The split has to stay split.** The risk is a well-meaning edit re-adding Claude
instructions to `/os/connector`, leaving two copies to drift. A test asserts both
halves: the page must link to `/os/claudecode` and `/os/buzz`, and must **not**
contain `claude mcp add` or `claude_desktop_config.json`.

**Access.** `/os/claudecode` mints API keys, so it joins `connector`, `apikeys`
and `buzz` in `osPathMinLevel`'s admin list. Verified by removing the entry and
watching the gate test report level 1 instead of admin — without it the page
would have inherited the permissive author default.

**No new surface.** Same as ADR-0146: no dependency, no secret, no outbound path,
no backend route beyond one authenticated GET. Minting still goes through the
existing CSRF-protected API-key endpoints.

## References

- ADR-0139 — VayuMCP connector
- ADR-0140 — VayuMCP OAuth 2.1 (the one-click route)
- ADR-0146 — Buzz connector, and the per-client page precedent
- The VayuOS page design house style, in the contributor notes
