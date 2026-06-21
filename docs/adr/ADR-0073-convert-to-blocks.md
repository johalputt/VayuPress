# ADR-0073: Convert-to-blocks legacy import

- Status: Accepted
- Date: 2026-06-21
- Relates to: ADR-0068 (Admin v3 block editor), ADR-0069 (Admin v2 retirement)

## Context

ADR-0069 Stage 1 ("Parity") requires an explicit **Convert to blocks** action so
that legacy Markdown/HTML articles can move into the native v3 block editor
without forcing operators to keep the v2 editor alive. Until now, opening a
legacy post in `/admin/v3/editor/{slug}` fell back to the v2 editor; there was no
path to adopt the block model for existing content.

The retirement plan's hard guardrail is **no silent data loss**: legacy posts
must never be auto-rewritten, and a conversion that looks wrong must be
abandonable.

## Decision

Add a non-destructive importer and a gated UI action.

1. **Importer** — `blockrender.ImportHTML(content string) []Block` parses the
   article's stored HTML with `golang.org/x/net/html` and maps block-level
   elements to editor blocks: headings (clamped to levels 2–4), paragraphs,
   ordered/unordered lists, blockquotes, `<pre><code>` (with `language-*` hint),
   images (including lone `<img>` inside `<p>`/`<figure>`), and `<hr>`. Unknown
   elements degrade to a paragraph carrying their text, so no visible prose is
   dropped. Inline formatting (`<strong>`, `<a>`) is flattened to plain text —
   acceptable because the action is explicit and reversible.

2. **Endpoint** — `POST /admin/v3/api/editor/convert {slug}` (session-gated,
   CSRF-protected) imports the article and writes **only** the `blocks_json`
   side-car. It never touches the rendered `articles.content`. The next editor
   load detects the block document and opens the native block editor.

3. **UI** — the v3 legacy-editor view shows a "Convert to blocks" banner for
   existing posts. The action requires a `confirm()` and explains that published
   content is unchanged until the operator saves in the new editor.

## Consequences

- **Reversible**: because conversion writes only the side-car, an unsatisfactory
  import can be abandoned by navigating away — the live article is untouched
  until an explicit Save re-renders blocks → content through the authoritative
  article pipeline.
- **Safe**: imported blocks pass through the same `blockrender.Render` UGC
  sanitiser as hand-authored blocks; scripts and event handlers in legacy HTML
  cannot survive conversion (covered by tests).
- **Parity**: with this action, ADR-0069 Stage 1 editorial parity is complete;
  the project can proceed to Stage 2 (soft deprecation) for the 1.5.0 line.
- `golang.org/x/net` becomes a direct dependency (previously indirect).
