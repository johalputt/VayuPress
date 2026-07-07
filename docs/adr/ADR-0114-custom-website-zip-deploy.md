# ADR-0114: One-Click Custom Website (.zip) Deploy

**Status**: Accepted  
**Date**: 2026-07-07  
**Author**: @johalputt  
**Relates to**: [ADR-0113](ADR-0113-memory-footprint-budget-and-search-index-windowing.md), the business-site modes (VayuOS → Website)

## Context

VayuPress can serve the blog at the root, or a built-in business-site template at
the root. Operators also wanted to publish a **fully custom** website — hand-made
or AI-generated — without a build step, terminal, or redeploy of the binary,
while keeping the blog and existing post URLs intact.

The site's whole premise is a single sovereign Go binary with no Node/npm/build
step. So "custom website" must mean: upload a **static** bundle and serve it —
not run an arbitrary app.

## Decision

Add a fourth site mode, **`custom`**: the operator uploads a `.zip` static site
in VayuOS → Website; VayuPress validates and serves it at the domain root. The
blog moves to `/blog` and posts keep their `/slug` URLs (reusing the
`business_subpath` routing from ADR-0113's release).

- **`internal/customsite`** owns validation, extraction, and serving:
  - Zip-slip proof: entry names are rejected if absolute or containing a `..`
    segment, and the resolved path is confined under the target directory.
  - Regular files only (symlinks/specials rejected); extension allowlist of
    static web asset types; per-file (25 MiB), total (50 MiB decompressed) and
    file-count (3000) caps; a hard copy limit defeats zip bombs regardless of the
    declared sizes; `index.html` at the root is required.
  - Atomic activation: extraction happens in a staging dir and is swapped in only
    on full success; the prior deployment is retained as `previous/` for
    **one-click rollback**.
  - Serving is traversal-safe, sets `X-Content-Type-Options: nosniff`, never
    lists directories, and only reads from the confined `current/` directory.
- Storage lives under the **data root** (sibling of `MEDIA_DIR`), never under
  `CACHE_DIR` — the render cache is purged on content changes and would otherwise
  delete the uploaded site.
- Serving integrates at two points only: the root handler serves the bundle's
  `index.html`, and the shared 404 fallback serves any other bundle page/asset
  for a path that is not a real post or reserved route. Reserved dynamic paths
  (`/blog`, `/api`, `/os`, `/media`, `/search`, `/tags`, feeds, etc.) always win.
- A downloadable **AI build guide** (served from the console) states exactly the
  rules the validator enforces, so an operator can hand it to any AI assistant
  and get a compliant bundle.

## Consequences

- Positive: anyone can publish a bespoke site in one click, self-contained and
  offline-capable, with instant rollback — no build step, no new runtime, still a
  single binary. The blog and every existing post URL are untouched.
- Security: the upload is untrusted and handled with defence in depth (traversal,
  symlink, type, size/count, zip-bomb, confinement, nosniff). Switching to
  `custom` mode is refused unless a bundle is actually deployed.
- Trade-off: bundles are **static only** (no server code) by design; inline
  scripts in a custom bundle run under the bundle's own pages, so operators own
  the content they upload. Bundle storage is on the filesystem (not the SQLite
  backup set); it can be re-uploaded at any time and is covered by filesystem
  backups of the data root.
