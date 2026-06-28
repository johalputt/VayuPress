# ADR-0095: Startup Index Self-Check & Pages Composite Index

**Status**: Accepted  
**Date**: 2026-06-28  
**Author**: @johalputt

## Context

A run of scaling fixes removed the full-table scans that were 502-ing a large
catalog: a composite `(is_page, status, created_at)` index for the Posts manager
and feeds (ADR-0047 era), moving cold-render reads off the writer connection, and
an `article_tags` join table that replaced every `tags LIKE '%..%'` scan
(ADR-0093). Each was proven with `EXPLAIN QUERY PLAN`.

The remaining risk is **regression**. VayuPress depends on every hot read being
index-backed to serve 1M+ posts on a small VPS. A future feature can easily and
silently reintroduce a full scan — a `COALESCE(col, …)` wrapper that defeats an
index, a new `LIKE '%..%'` filter, or an `ORDER BY` on an unindexed column — and
it will look fine in development against a tiny database, only to 502 in
production once the catalog is large. There was no automated guard against this.

A focused audit of the current hot reads also turned up one query that filtered
via an index but still sorted in memory: the Pages manager
(`WHERE is_page=1 ORDER BY updated_at DESC`).

## Decision

### 1. Pages composite index (migration 049)

Add `idx_articles_pages(is_page, updated_at DESC)`. The Pages manager now serves
both the `is_page=1` filter and the `updated_at DESC` order directly from the
index, eliminating the temp-b-tree sort. `idx_articles_pagefeed` could not help
because it orders by `created_at`.

### 2. Startup index self-check

Add a read-only self-check (`internal/db/indexcheck.go`) that runs shortly after
boot on a background goroutine:

- It holds a curated list of the heaviest catalogue reads (Posts counts/list,
  home, pages, article-by-slug, JSON list, the tag-membership lookups, contact
  messages, comments). Inherently-unindexable reads — e.g. the LIKE-based SQLite
  search fallback — are intentionally excluded.
- For each, it runs `EXPLAIN QUERY PLAN` and inspects the plan. A plan line that
  begins with `SCAN <table>` and uses no index (no named index, covering index,
  or integer primary key) is a full table scan; the check logs a loud `warn` that
  names the query, and increments `MetricFullScanWarnings`.
- It is strictly read-only, never blocks startup, and skips any query whose table
  does not exist on a partial/older schema rather than treating it as a failure.

A unit test applies the real migrations to an in-memory database and asserts the
whole curated set is index-backed, so dropping a relied-upon index or adding a
scanning hot query fails CI rather than production.

## Consequences

- A full-scan regression is caught at boot (in logs and a metric) instead of as a
  production 502, and in CI by the test that runs the audit against the real
  schema.
- The curated query list must be kept in sync as new hot reads are added; the
  test makes that obligation explicit.
- The Pages manager is index-ordered with no sort, matching the Posts manager.
