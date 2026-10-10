// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/textfix"
)

// text_repair.go is the one pass over the posts already stored that restores
// garbled characters (internal/textfix): new writes are restored as they
// arrive (api.ArticleService), the posts written before are restored here.
//
// It reads every body, on johal.in some gigabytes, so it walks the table in
// batches with pauses, off the request path, and waits while the write queue
// is busy. A post is restored through the write queue like any edit, so its
// text before is kept as a "pre-update" version and its cached pages are
// purged. Where it is (text_repair, migration 107) survives a restart, and
// once it has reached the end it never runs again.

const (
	textRepairBatch   = 200
	textRepairPause   = 500 * time.Millisecond
	textRepairBacklog = 100 // pending writes above which the pass waits
)

// startTextRepair runs the pass in the background, after boot has settled.
func (a *App) startTextRepair(done <-chan struct{}) {
	go func() {
		select {
		case <-done:
			return
		case <-time.After(30 * time.Second):
		}
		if err := a.repairStoredText(context.Background(), done, textRepairPause); err != nil {
			logging.LogError("textrepair", "pass stopped", err.Error())
		}
	}()
}

// repairStoredText walks the posts from where the pass last stopped to the
// end; pause is the wait between batches.
func (a *App) repairStoredText(ctx context.Context, done <-chan struct{}, pause time.Duration) error {
	if dbpkg.DB == nil || a.articles == nil {
		return nil
	}
	var cursor string
	var finished sql.NullTime
	err := dbpkg.DB.QueryRowContext(ctx, `SELECT cursor, done_at FROM text_repair WHERE id=1`).Scan(&cursor, &finished)
	if err == sql.ErrNoRows {
		_, err = dbpkg.DB.ExecContext(ctx, `INSERT INTO text_repair(id) VALUES(1)`)
	}
	if err != nil || finished.Valid {
		return err
	}
	for {
		for a.writeBacklog(ctx) > textRepairBacklog {
			if !waitOrStop(done, pause) {
				return nil
			}
		}
		next, scanned, restored, err := a.repairTextBatch(ctx, cursor)
		if err != nil {
			return err
		}
		if scanned == 0 {
			_, err = dbpkg.DB.ExecContext(ctx, `UPDATE text_repair SET done_at=CURRENT_TIMESTAMP WHERE id=1`)
			var total, fixed, lost int
			_ = dbpkg.DB.QueryRowContext(ctx, `SELECT scanned, restored, (SELECT COUNT(1) FROM text_repair_lost) FROM text_repair WHERE id=1`).Scan(&total, &fixed, &lost)
			dbpkg.AuditLog("content.text_repair", "system", "", fmt.Sprintf("scanned=%d restored=%d unrestorable=%d", total, fixed, lost))
			return err
		}
		if _, err := dbpkg.DB.ExecContext(ctx, `UPDATE text_repair SET cursor=?, scanned=scanned+?, restored=restored+? WHERE id=1`, next, scanned, restored); err != nil {
			return err
		}
		cursor = next
		if !waitOrStop(done, pause) {
			return nil
		}
	}
}

// repairTextBatch repairs the batch of posts after cursor. It returns the last
// id read, how many it read and how many it queued restored.
func (a *App) repairTextBatch(ctx context.Context, cursor string) (string, int, int, error) {
	rows, err := dbpkg.Reader().QueryContext(ctx, `SELECT id, slug, title, content FROM articles WHERE id > ? ORDER BY id LIMIT ?`, cursor, textRepairBatch)
	if err != nil {
		return cursor, 0, 0, err
	}
	type found struct {
		slug, title, content string
		restored             bool
		lost                 int
	}
	var hits []found
	scanned := 0
	for rows.Next() {
		var id, slug, title, content string
		if err := rows.Scan(&id, &slug, &title, &content); err != nil {
			_ = rows.Close()
			return cursor, 0, 0, err
		}
		scanned++
		cursor = id
		newTitle, t := textfix.Repair(title)
		newContent, c := textfix.Repair(content)
		if t.Restored+c.Restored+t.Lost+c.Lost > 0 {
			hits = append(hits, found{slug, newTitle, newContent, t.Restored+c.Restored > 0, t.Lost + c.Lost})
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return cursor, 0, 0, err
	}
	restored := 0
	for _, h := range hits {
		if h.lost > 0 {
			if _, err := dbpkg.DB.ExecContext(ctx, `INSERT OR REPLACE INTO text_repair_lost(slug, runs) VALUES(?, ?)`, h.slug, h.lost); err != nil {
				return cursor, 0, 0, err
			}
		}
		if !h.restored {
			continue
		}
		art, err := a.articles.Repo.Get(ctx, h.slug)
		if err != nil {
			continue // deleted since it was read
		}
		art.Title, art.Content, art.UpdatedAt = h.title, h.content, time.Now().UTC()
		if err := a.articles.Queue.Enqueue(ctx, art, "update"); err != nil {
			return cursor, 0, 0, err
		}
		restored++
	}
	return cursor, scanned, restored, nil
}

// writeBacklog is how many writes are waiting in the queue.
func (a *App) writeBacklog(ctx context.Context) int {
	var n int
	_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&n)
	return n
}

// waitOrStop pauses for d, and reports false if the server is stopping.
func waitOrStop(done <-chan struct{}, d time.Duration) bool {
	select {
	case <-done:
		return false
	case <-time.After(d):
		return true
	}
}
