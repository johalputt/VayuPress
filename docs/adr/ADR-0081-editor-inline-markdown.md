# ADR-0081: Block Editor Inline Rich Text via Markdown

**Status**: Accepted
**Date**: 2026-06-25
**Author**: @johalputt

## Context

The block editor stored and rendered block text as plain, HTML-escaped strings,
so there was no inline formatting at all — no bold, italic, links or inline
code. Authors expected a Ghost-style writing experience.

## Decision

1. Render inline Markdown (`**bold**`, `*italic*`, `` `code` ``, `[links](url)`,
   `~~strike~~`) for paragraph/heading/quote/callout/list text in
   `internal/blockrender`, using a dedicated goldmark instance
   (`inlineMD`, GFM strikethrough + linkify) and stripping the outer `<p>`.
2. The assembled fragment continues through the existing **bluemonday UGC
   sanitiser**, so the change upgrades plain-escaped text to safe rich inline
   HTML **without widening the XSS surface** (goldmark escapes raw HTML and
   bluemonday re-validates). Regression tests assert formatting renders and that
   `<script>`/`javascript:` are still stripped.
3. The editor (`admin-os-editor.js`) gains a selection toolbar, leading-Markdown
   shortcuts, continuous Enter/Backspace block flow, a filterable slash menu, and
   image paste / drag-and-drop upload via the existing media endpoint.

## Consequences

- Positive: real rich-text authoring; block model and storage contract are
  unchanged; security posture preserved (sanitiser is the single trust boundary).
- Trade-off: literal `*`/`_`/`` ` `` in pre-existing content now render as
  Markdown (matches Ghost); authors escape with `\*` when a literal is intended.
