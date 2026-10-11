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

// text_repair.go is the one pass over the posts already stored that finds
// garbled characters (internal/textfix). It changes no post: each with text
// that can be restored is offered on the Posts page (migration 110), and its
// author restores it or keeps it as written there. The 3.18.0 release audit
// found why: on johal.in the guide to mojibake quotes such text on purpose,
// and a repair applied unasked would have rewritten its examples. New writes
// are offered the same way as they arrive (api.ArticleService). Runs whose
// bytes were lost are listed to fix by hand (text_repair_lost).
//
// It reads every body, on johal.in some gigabytes, so it walks the table in
// batches with pauses, off the request path, and waits while the write queue
// is busy. Where it is (text_repair, migration 107) survives a restart, and
// once it has reached the end it never runs again.

const (
	textRepairBatch   = 200
	textRepairPause   = 500 * time.Millisecond
	textRepairBacklog = 100 // pending writes above which the pass waits
)

// startStoredPasses runs the passes over the posts already stored in the
// background, after boot has settled: the tags first (tag_cleanup.go), then
// this one, one after the other so that two walks over every body never load
// the box at once. This one writes no post (it only offers), so it cannot
// undo the tag pass's work.
func (a *App) startStoredPasses(done <-chan struct{}) {
	go func() {
		select {
		case <-done:
			return
		case <-time.After(30 * time.Second):
		}
		// A failed tag pass is no longer running, so this one cannot race it;
		// the tag pass resumes where it stopped on the next boot.
		if err := a.cleanStoredTags(context.Background(), done, textRepairPause); err != nil {
			logging.LogError("tagcleanup", "pass stopped", err.Error())
		}
		select {
		case <-done:
			return
		default:
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
		next, scanned, offered, err := a.repairTextBatch(ctx, cursor)
		if err != nil {
			return err
		}
		if scanned == 0 {
			_, err = dbpkg.DB.ExecContext(ctx, `UPDATE text_repair SET done_at=CURRENT_TIMESTAMP WHERE id=1`)
			var total, fixed, lost int
			_ = dbpkg.DB.QueryRowContext(ctx, `SELECT scanned, restored, (SELECT COUNT(1) FROM text_repair_lost) FROM text_repair WHERE id=1`).Scan(&total, &fixed, &lost)
			dbpkg.AuditLog("content.text_repair", "system", "", fmt.Sprintf("scanned=%d offered=%d unrestorable=%d", total, fixed, lost))
			return err
		}
		if _, err := dbpkg.DB.ExecContext(ctx, `UPDATE text_repair SET cursor=?, scanned=scanned+?, restored=restored+? WHERE id=1`, next, scanned, offered); err != nil {
			return err
		}
		cursor = next
		if !waitOrStop(done, pause) {
			return nil
		}
	}
}

// repairTextBatch reads the batch of posts after cursor and offers each with
// text that can be restored. It returns the last id read, how many it read and
// how many it offered.
func (a *App) repairTextBatch(ctx context.Context, cursor string) (string, int, int, error) {
	rows, err := dbpkg.Reader().QueryContext(ctx, `SELECT id, slug, title, content FROM articles WHERE id > ? ORDER BY id LIMIT ?`, cursor, textRepairBatch)
	if err != nil {
		return cursor, 0, 0, err
	}
	type found struct {
		slug    string
		changes int
		lost    int
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
		_, t := textfix.Repair(title)
		_, c := textfix.Repair(content)
		if t.Restored+c.Restored+t.Lost+c.Lost > 0 {
			hits = append(hits, found{slug, t.Restored + c.Restored, t.Lost + c.Lost})
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return cursor, 0, 0, err
	}
	offered := 0
	for _, h := range hits {
		if h.lost > 0 {
			if _, err := dbpkg.DB.ExecContext(ctx, `INSERT OR REPLACE INTO text_repair_lost(slug, runs) VALUES(?, ?)`, h.slug, h.lost); err != nil {
				return cursor, 0, 0, err
			}
		}
		if h.changes == 0 {
			continue
		}
		art, err := a.articles.Repo.Get(ctx, h.slug)
		if err != nil {
			continue // deleted since it was read
		}
		if err := dbpkg.OfferTextRepair(ctx, dbpkg.WDB, art.Slug, dbpkg.TextChanges(art.Title, art.Content)); err != nil {
			return cursor, 0, 0, err
		}
		offered++
	}
	return cursor, scanned, offered, nil
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
