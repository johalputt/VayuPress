package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// article_tags.go maintains the normalised article_tags join table (migration
// 048). The articles table keeps tags as a single comma-separated string, but
// every "posts with tag X" lookup needs an indexed membership test rather than a
// `tags LIKE '%X%'` full-table scan. These helpers keep the join table in sync
// transactionally with each article write, and a one-time batched backfill
// populates it for catalogues that predate the table (or were bulk-imported).

// normaliseTags trims each tag, drops blanks, and de-duplicates case-insensitively
// while preserving the first-seen display casing. The returned slice is the exact
// set of rows that should exist in article_tags for the article.
func normaliseTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, t)
	}
	return out
}

// replaceArticleTagsTx rewrites the article_tags rows for articleID inside tx:
// it deletes any existing membership rows and inserts one row per normalised tag,
// each stamped with the article's (immutable) created_at so per-tag listings can
// be served in recency order straight from the index. Running delete+insert in
// the caller's transaction keeps the join table exactly consistent with the
// articles row even under concurrent writers.
func replaceArticleTagsTx(tx *sql.Tx, articleID string, createdAt time.Time, tags []string) error {
	if articleID == "" {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM article_tags WHERE article_id=?`, articleID); err != nil {
		return fmt.Errorf("article_tags delete: %w", err)
	}
	for _, t := range normaliseTags(tags) {
		if _, err := tx.Exec(`INSERT INTO article_tags(article_id, tag, tag_norm, created_at) VALUES(?,?,?,?)`, articleID, t, strings.ToLower(t), createdAt); err != nil {
			return fmt.Errorf("article_tags insert: %w", err)
		}
	}
	return nil
}

// SyncArticleTagsByIDTx syncs membership for an article whose id and created_at
// are known (the insert path). It is exported for the write-queue worker.
func SyncArticleTagsByIDTx(tx *sql.Tx, articleID string, createdAt time.Time, tags []string) error {
	return replaceArticleTagsTx(tx, articleID, createdAt, tags)
}

// SyncArticleTagsBySlugTx resolves the article id and created_at from its slug and
// rewrites its membership rows (the update path, whose UPDATE keys on slug).
// Resolving inside the transaction means we never depend on possibly-stale values
// in the job payload. A missing row is not an error — the article write itself is
// the authority and will have failed first if the slug does not exist.
func SyncArticleTagsBySlugTx(tx *sql.Tx, slug string, tags []string) error {
	var id string
	var createdAt time.Time
	if err := tx.QueryRow(`SELECT id, created_at FROM articles WHERE slug=?`, slug).Scan(&id, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("article_tags resolve id: %w", err)
	}
	return replaceArticleTagsTx(tx, id, createdAt, tags)
}

// DeleteArticleTagsBySlugTx removes all membership rows for the article with the
// given slug (the delete path). It must run before the articles row is deleted so
// the slug still resolves.
func DeleteArticleTagsBySlugTx(tx *sql.Tx, slug string) error {
	if _, err := tx.Exec(`DELETE FROM article_tags WHERE article_id IN (SELECT id FROM articles WHERE slug=?)`, slug); err != nil {
		return fmt.Errorf("article_tags delete by slug: %w", err)
	}
	return nil
}

// StartArticleTagsBackfill launches a one-time background backfill that populates
// article_tags for any article missing from it. It is safe to run on every boot:
// it exits immediately once every tagged article has a membership row, and it
// processes rows in bounded batches with short pauses so it never monopolises the
// single writer connection on a low-resource VPS holding a multi-GB database.
func StartArticleTagsBackfill(doneCh <-chan struct{}) {
	go func() {
		// Let migrations settle and the worker pool warm up first.
		select {
		case <-doneCh:
			return
		case <-time.After(20 * time.Second):
		}
		if err := backfillArticleTags(doneCh); err != nil {
			logging.LogError("article-tags-backfill", "backfill failed", err.Error())
		}
	}()
}

// backfillArticleTags walks the articles table by id in batches, inserting
// membership rows for any tagged article that does not yet have them. It is
// resumable: each batch only touches articles still missing from article_tags,
// and a cursor over the primary key advances monotonically so progress is never
// repeated. The whole pass is skipped when nothing is missing.
func backfillArticleTags(doneCh <-chan struct{}) error {
	if DB == nil {
		return nil
	}
	if !articleTagsBackfillNeeded() {
		return nil
	}
	logging.LogInfo("article-tags-backfill", "starting (populating tag membership for existing posts)")
	const batch = 2000
	cursor := ""
	var totalArticles, totalRows int64
	start := time.Now()
	for {
		select {
		case <-doneCh:
			logging.LogInfo("article-tags-backfill", "stopping (shutdown)")
			return nil
		default:
		}
		rows, err := Reader().Query(
			`SELECT a.id, a.tags, a.created_at FROM articles a WHERE a.id > ? AND a.tags != '' AND NOT EXISTS(SELECT 1 FROM article_tags t WHERE t.article_id = a.id) ORDER BY a.id LIMIT ?`,
			cursor, batch)
		if err != nil {
			return fmt.Errorf("scan batch: %w", err)
		}
		type rec struct {
			id        string
			tags      string
			createdAt time.Time
		}
		var recs []rec
		var lastID string
		for rows.Next() {
			var r rec
			if err := rows.Scan(&r.id, &r.tags, &r.createdAt); err != nil {
				continue
			}
			recs = append(recs, r)
			lastID = r.id
		}
		_ = rows.Err()
		rows.Close()
		if len(recs) == 0 {
			break
		}
		// One transaction per batch keeps each write short.
		tx, err := DB.Begin()
		if err != nil {
			return fmt.Errorf("begin batch: %w", err)
		}
		for _, r := range recs {
			n, err := insertBackfillRow(tx, r.id, r.createdAt, r.tags)
			if err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("backfill row %s: %w", r.id, err)
			}
			totalRows += n
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit batch: %w", err)
		}
		totalArticles += int64(len(recs))
		cursor = lastID
		if len(recs) < batch {
			break
		}
		time.Sleep(75 * time.Millisecond)
	}
	logging.LogInfo("article-tags-backfill", fmt.Sprintf("done — %d articles, %d tag rows in %s", totalArticles, totalRows, time.Since(start).Round(time.Millisecond)))
	return nil
}

// insertBackfillRow inserts the membership rows for one article inside tx and
// returns how many rows it wrote. It does not pre-delete (the article is, by the
// NOT EXISTS filter, absent from the table), keeping the backfill write-light.
func insertBackfillRow(tx *sql.Tx, articleID string, createdAt time.Time, tagsCSV string) (int64, error) {
	var n int64
	for _, t := range normaliseTags(strings.Split(tagsCSV, ",")) {
		if _, err := tx.Exec(`INSERT INTO article_tags(article_id, tag, tag_norm, created_at) VALUES(?,?,?,?)`, articleID, t, strings.ToLower(t), createdAt); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// articleTagsBackfillNeeded reports whether at least one tagged article is still
// absent from article_tags. The LIMIT 1 keeps the check cheap even on a large
// catalogue once the backfill has completed.
func articleTagsBackfillNeeded() bool {
	var exists int
	err := Reader().QueryRow(
		`SELECT EXISTS(SELECT 1 FROM articles a WHERE a.tags != '' AND NOT EXISTS(SELECT 1 FROM article_tags t WHERE t.article_id = a.id))`,
	).Scan(&exists)
	if err != nil {
		// On error, prefer attempting the backfill (idempotent) over silently skipping.
		return true
	}
	return exists == 1
}
