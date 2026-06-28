# ADR-0093: Indexed Tag Membership (article_tags join table)

**Status**: Accepted  
**Date**: 2026-06-28  
**Author**: @johalputt

## Context

VayuPress is meant to run a 1M+ post catalogue on a small VPS with smooth,
incremental updates. Tags, however, were stored only as a single comma-separated
string on each article row (`articles.tags`). Every "find the posts with tag X"
query therefore had to use `tags LIKE '%X%'`:

- the per-tag page (`/tags/{tag}`),
- related posts on each article,
- the topic index (`/tags`) — which additionally loaded **every** article's tags
  string into memory and counted them in Go,
- the JSON list endpoint's `?tag=` filter.

A leading-wildcard `LIKE` cannot use an index, so each of these read **every**
row of the articles table. On a real 234k-post / 12 GB database that scan reads
gigabytes of `content` and blows past the request timeout — surfacing as a 502
the moment a visitor (or VayuOS) opens a tag page. The single-column
`idx_articles_tags` index on the CSV column did nothing for these queries.

## Decision

Introduce a normalised membership table and resolve every tag lookup through it.

### Schema (migration 048)

```sql
CREATE TABLE article_tags(article_id TEXT, tag TEXT, tag_norm TEXT, created_at DATETIME);
CREATE INDEX idx_article_tags_norm_created ON article_tags(tag_norm, created_at DESC, article_id);
CREATE INDEX idx_article_tags_article      ON article_tags(article_id);
CREATE INDEX idx_article_tags_tag          ON article_tags(tag);
```

- `tag` keeps the original (trimmed) display casing for the topic-index counts.
- `tag_norm` is the lower-cased form for case-insensitive membership lookups.
- `created_at` is copied from the (immutable) article so the per-tag listing is
  served straight from the composite index in recency order — no sort, no scan.

### Consistency

Membership is rewritten inside the **same transaction** as every article
create / update / delete in the write-queue worker (and the integration test's
direct writer mirrors this), so the join table can never drift from the articles
row. A one-time, **resumable, batched** background backfill populates the table
for pre-existing or bulk-imported posts: it walks the primary key in bounded
batches, only touches articles still missing from the table (a `NOT EXISTS`
probe on the indexed `article_id`), pauses briefly between batches so it never
monopolises the single writer connection, and skips itself entirely once nothing
is missing.

### Query shape

All four lookups join `article_tags` to `articles`. To stop the planner from
ever choosing a full articles scan when a tag happens to be very common, the
read queries use `CROSS JOIN` with `article_tags` first, pinning it as the
driving table. The result is always an indexed range scan over the tag's rows
plus a primary-key join — cost bounded by *how many posts carry the tag*, not by
the table size. The topic-index count becomes a single `GROUP BY tag` over a
covering index rather than an in-memory aggregation of every article's tags.

## Consequences

- Tag pages, related posts, the topic index and the JSON tag filter stay fast at
  1M+ posts; the catalogue-wide 502 on tag surfaces is removed at its root.
- `EXPLAIN QUERY PLAN` was used to verify each query is index-driven (not a
  `SCAN articles`) for both rare and very common tags before shipping.
- A modest write amplification (a few small index rows per article write) and a
  one-time backfill cost, both bounded and paced for a low-RAM VPS.
- `article_tags` is the authoritative tag index going forward; the legacy
  `idx_articles_tags` on the CSV column is now only used by external import tools.
