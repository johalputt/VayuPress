# ADR-0075 — Draft/Publish Workflow and Public-Surface Isolation

**Status:** Accepted  
**Date:** 2026-06-22  
**Deciders:** Ankush Choudhary Johal

---

## Context

VayuPress had no native draft concept before v1.7.0. Operators working on
in-progress articles were forced to either publish immediately or keep content
out of the system entirely. There was also no systematic protection preventing a
draft — once written to the DB — from leaking through the API, the on-disk
render cache, or the comment API.

Three specific leak paths were identified during the v1.7.0 audit:

- **LEAK-1 (Critical):** `GET /api/v1/articles/{slug}` returned the full article
  payload including draft content to any caller with the slug.
- **LEAK-2 (High):** The write-queue worker pre-rendered and cached the HTML for
  any article job, including drafts, making draft content accessible at the
  static cache path even after an explicit unpublish.
- **LEAK-3 (Low):** The comment submission and listing endpoints looked up the
  article by slug only, allowing the existence of a draft to be confirmed by
  observing the difference between a 404 (missing slug) and a 200/empty response
  (valid slug, no comments).

---

## Decision

### 1. Schema — `articles.status` column (migration 030)

Add a `status TEXT NOT NULL DEFAULT 'published'` column to `articles`. The only
accepted values are `'published'` and `'draft'`. An index on `(status,
created_at DESC)` accelerates the public-list query. All existing rows default
to `'published'` — the upgrade is non-breaking.

### 2. Public surface contract

Every query that feeds a public surface **must** filter
`COALESCE(status,'published')='published'`. Covered surfaces:

- Article list API (`/api/v1/articles`)
- Article page renderer (`/{slug}`)
- RSS feed (`/feed.xml`)
- Sitemap (`/sitemap.xml`)
- Full-text search index build
- Related articles widget
- Homepage article list

### 3. API visibility rule (LEAK-1 fix)

`handleGetArticle` returns 404 for any article whose status is `'draft'` unless
the request is authenticated (`isAdminRequest`). This makes drafts
indistinguishable from missing articles for all external callers.

### 4. Render-cache guard (LEAK-2 fix)

The write-queue worker queries `COALESCE(status,'published')` from the DB
immediately before writing the rendered HTML to the on-disk cache. Only
`'published'` articles are cached. A draft that transitions
published → draft triggers a cache purge via `render.CachePurge`; the worker
then skips the cache write for any subsequent job while the article remains a
draft.

### 5. Comment-API guard (LEAK-3 fix)

Both `handleCommentSubmit` and `handleCommentList` resolve the article ID via a
query that includes `AND COALESCE(status,'published')='published'`. A request
targeting a draft slug receives 404 — the same response as a non-existent slug.

### 6. VayuOS post manager

A new operator page (`/os/posts`) lists all articles (published and draft) with
status pills. Each row provides Edit and Publish/Unpublish actions. Status
transitions call `POST /os/api/posts/status` (CSRF-protected), which updates the
DB, purges the relevant render caches (`posts/<slug>.html`, homepage, tag pages,
sitemap, feed), and logs an audit entry.

---

## Consequences

- Drafts are invisible to all public surfaces; only authenticated operators can
  observe them.
- The on-disk cache never contains draft HTML; a cache-poisoning attack via a
  publish-then-draft sequence is not possible.
- The comment API cannot be used to enumerate draft slug existence.
- Migration 030 is backwards-compatible (all existing rows default to
  `published`).
- Three integration tests (`TestDraftNotLeakedVia*`) permanently guard the three
  fixed leak paths.

---

## Alternatives Considered

**Separate drafts table** — Rejected. Adds join complexity on every public
query and complicates the audit trail. A status column is simpler and equally
expressive for the two-state model needed.

**Client-side draft storage** — Rejected. Operator data would not survive
browser sessions or device switches; no audit trail.
