# ADR-0179 — A site's own code runs by its hash

- **Status:** Accepted
- **Date:** 2026-11-15
- **Amends:** ADR-0164 §4, which left code on a templated site undecided
- **Code:** `internal/render/snippet.go`, `cmd/vayupress/site_snippets.go`, `cmd/vayupress/middleware_site_csp.go`, `cmd/vayupress/admin_os_services.go`.

## 0. The question

An operator who wants an analytics tag, a chat widget or a verification tag
on a templated site has nowhere to put it. Those snippets pair a script from
another site with a few inline lines, and the strict policy runs no inline
script. ADR-0164 drew the line at "code only from the vetted catalogue",
which keeps out a typed script origin; it said nothing of code the operator
writes.

## 1. Decision

A site has two snippets, edited on its Outside services page by an
administrator: one put in at the end of `<head>`, one at the end of `<body>`.

- **Each inline script runs by its own hash.** Its `'sha256-…'` is added to
  that site's `script-src` and nothing wider: no `'unsafe-inline'`, so text
  an injection puts into a page still does not run. A hash, unlike a nonce,
  is the same on every request, so a page from the cache stays valid.
- **A script from another site still needs its service.** A `<script src>`
  loads only from an origin a vetted service allows; on save, the page names
  any origin the site does not allow yet.
- **What a hash cannot allow is refused on save:** an inline event attribute
  and a `javascript:` address, which would need `'unsafe-hashes'`; a `<base>`,
  which redirects every link; a `<meta http-equiv>`, which overrides how the
  page is served; a script over plain http; more than 16 KB.
- **Public pages only.** The snippets follow the policy's refusal list
  (ADR-0164 §2): never the console, sign-in, member, checkout or mail pages.
  Nor a VayuShield challenge, nor anything but a successful HTML page.

This grants nothing new: an administrator could already publish any script
in a hand-built site. It is operator-written code, allowed by exactly its
text.

## 2. Where it is enforced

- `render.ParseSnippet` reads a snippet as a browser will: line breaks made
  `\n` before hashing, as a browser does; the refusals above.
- `siteCSPMiddleware` adds the hashes to the site's policy, a strict one
  included, and `snippetWriter` puts the snippets into each page as it goes
  out, fresh or from the cache.
- A save marks every cached page stale, so a page a browser kept is not
  answered 304 without the new code.
- A browser spec saves a snippet and reads that Chromium ran it under the
  page's real policy; it fails when the hash is wrong.
