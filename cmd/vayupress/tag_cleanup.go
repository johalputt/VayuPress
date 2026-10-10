// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/tagrules"
)

// tag_cleanup.go is the one pass over the posts already stored that puts their
// tags under the tag rules (internal/tagrules): new writes are cleaned as they
// arrive (api.ArticleService), the posts written before are cleaned here. On
// johal.in that is 234k posts of 13–25 tags each, title words and one
// boilerplate set among them; each keeps its rarest MaxPerPost.
//
// It reads a post's tags from article_tags, never from articles: the tags
// column is stored after the body, so reading it walks the body's overflow
// pages. A changed post's tags column and links are rewritten in one
// transaction on the writer, which is also where it reads them, so an edit
// through the write queue lands wholly before or after it. updated_at is kept:
// the post was not edited. Its tags column as it was is kept in tag_originals
// (migration 109), so the pass can be undone post by post.
// Where it is (tag_cleanup) survives a restart, and once it has reached the
// end it never runs again.

const tagCleanupBatch = 200 // posts per transaction

// cleanStoredTags walks the posts from where the pass last stopped to the end;
// pause is the wait between batches.
func (a *App) cleanStoredTags(ctx context.Context, done <-chan struct{}, pause time.Duration) error {
	if dbpkg.DB == nil {
		return nil
	}
	var cursor string
	var finished sql.NullTime
	err := dbpkg.DB.QueryRowContext(ctx, `SELECT cursor, done_at FROM tag_cleanup WHERE id=1`).Scan(&cursor, &finished)
	if err == sql.ErrNoRows {
		_, err = dbpkg.DB.ExecContext(ctx, `INSERT INTO tag_cleanup(id) VALUES(1)`)
	}
	if err != nil || finished.Valid {
		return err
	}
	// One count for the whole pass, so which tags a post keeps does not depend
	// on how far the pass had got when it was reached.
	shares, err := countTagShares(ctx)
	if err != nil {
		return err
	}
	frequency := func(key string) int { return shares.posts[key] }
	for {
		for a.writeBacklog(ctx) > textRepairBacklog {
			if !waitOrStop(done, pause) {
				return nil
			}
		}
		next, scanned, err := cleanTagBatch(ctx, cursor, frequency)
		if err != nil {
			return err
		}
		if scanned == 0 {
			if _, err := dbpkg.DB.ExecContext(ctx, `UPDATE tag_cleanup SET done_at=CURRENT_TIMESTAMP WHERE id=1`); err != nil {
				return err
			}
			var total, fixed int
			_ = dbpkg.DB.QueryRowContext(ctx, `SELECT scanned, changed FROM tag_cleanup WHERE id=1`).Scan(&total, &fixed)
			// Every page shows tags or lists by them; each re-renders on its next
			// request rather than all at once.
			render.CachePurgeAll()
			_, err := countTagShares(ctx)
			dbpkg.AuditLog("content.tag_cleanup", "system", "", fmt.Sprintf("scanned=%d changed=%d", total, fixed))
			return err
		}
		cursor = next
		if !waitOrStop(done, pause) {
			return nil
		}
	}
}

// cleanTagBatch cleans the batch of tagged posts after cursor in one
// transaction, and records where it got to in the same one. It returns the
// last post id read and how many posts it read.
func cleanTagBatch(ctx context.Context, cursor string, frequency func(string) int) (string, int, error) {
	tx, err := dbpkg.DB.BeginTx(ctx, nil)
	if err != nil {
		return cursor, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var last sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT MAX(article_id) FROM (SELECT DISTINCT article_id FROM article_tags WHERE article_id > ? ORDER BY article_id LIMIT ?)`, cursor, tagCleanupBatch).Scan(&last); err != nil {
		return cursor, 0, err
	}
	if !last.Valid {
		return cursor, 0, nil
	}
	type post struct {
		id      string
		created time.Time
		tags    []string
		stale   bool // a link not stored under its tag's key
	}
	var posts []*post
	rows, err := tx.QueryContext(ctx, `SELECT article_id, tag, tag_norm, created_at FROM article_tags WHERE article_id > ? AND article_id <= ? ORDER BY article_id, rowid`, cursor, last.String)
	if err != nil {
		return cursor, 0, err
	}
	for rows.Next() {
		var id, tag, norm string
		var created time.Time
		if err := rows.Scan(&id, &tag, &norm, &created); err != nil {
			_ = rows.Close()
			return cursor, 0, err
		}
		if len(posts) == 0 || posts[len(posts)-1].id != id {
			posts = append(posts, &post{id: id, created: created})
		}
		p := posts[len(posts)-1]
		p.tags = append(p.tags, tag)
		p.stale = p.stale || norm != tagrules.Key(tag)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return cursor, 0, err
	}

	changed := 0
	for _, p := range posts {
		clean := tagrules.Clean(p.tags, frequency)
		if !p.stale && strings.Join(clean, ",") == strings.Join(p.tags, ",") {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO tag_originals(article_id, tags) SELECT id, COALESCE(tags,'') FROM articles WHERE id=?`, p.id); err != nil {
			return cursor, 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE articles SET tags=? WHERE id=?`, strings.Join(clean, ","), p.id); err != nil {
			return cursor, 0, err
		}
		if err := dbpkg.SyncArticleTagsByIDTx(tx, p.id, p.created, clean); err != nil {
			return cursor, 0, err
		}
		changed++
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tag_cleanup SET cursor=?, scanned=scanned+?, changed=changed+? WHERE id=1`, last.String, len(posts), changed); err != nil {
		return cursor, 0, err
	}
	if err := tx.Commit(); err != nil {
		return cursor, 0, err
	}
	return last.String, len(posts), nil
}
