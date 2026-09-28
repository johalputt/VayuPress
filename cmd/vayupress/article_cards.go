// SPDX-License-Identifier: Apache-2.0

package main

// article_cards.go — what a listing shows of a post, read without its body.
//
// A tag page lists up to 200 posts and a home feed page 30. In articles every
// card field but the title and slug is stored after content, so reading a card
// from the row walks the whole body's overflow pages: on johal.in's 234k posts
// a crawler walking tag pages met p95 1.9 s, all of it cold reads of bodies no
// card shows. So a listing chooses its posts from an index alone, then reads
// their cards from article_cards (migration 103); a post without a card yet is
// read once, and its card stored for every listing after.

import (
	"context"
	"strings"

	"github.com/johalputt/vayupress/internal/api"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/seo"
)

// tagPageIDsSQL chooses a tag page's posts. The join is answered from
// idx_articles_id_status, so no article row is read.
const tagPageIDsSQL = `SELECT t.article_id FROM article_tags t CROSS JOIN articles a ON a.id=t.article_id WHERE t.tag_norm=? AND a.status='published'`

// homeFeedIDsSQL chooses a home feed page's posts from idx_articles_feed (or
// idx_articles_feed_domain for a hosted site), which carry the id.
const homeFeedIDsSQL = `SELECT id FROM articles WHERE is_page=0 AND status='published'`

// storeCardSQL stores a card only if its post is still the one just read: an
// edit between the read and this write deletes no card (there was none yet),
// so without the check the card of the old text would outlive the edit.
const storeCardSQL = `INSERT OR REPLACE INTO article_cards(article_id,title,slug,tags,created_at,excerpt,image) ` +
	`SELECT ?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM articles WHERE id=? AND CAST(updated_at AS TEXT)=?)`

// placeholders returns "?,?,…" for n values and the values as arguments.
func placeholders(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}

// listingCards returns the card of each post in ids, in that order. A post
// removed between choosing and reading is left out.
func listingCards(ctx context.Context, ids []string) []render.HomeArticle {
	if dbpkg.DB == nil || len(ids) == 0 {
		return nil
	}
	cards := make(map[string]render.HomeArticle, len(ids))
	in, args := placeholders(ids)
	if rows, err := dbpkg.Reader().QueryContext(ctx,
		`SELECT article_id,title,slug,tags,created_at,excerpt,image FROM article_cards WHERE article_id IN (`+in+`)`, args...); err == nil {
		for rows.Next() {
			var id, tags string
			var c render.HomeArticle
			if rows.Scan(&id, &c.Title, &c.Slug, &tags, &c.CreatedAt, &c.Excerpt, &c.Image) == nil {
				c.Tags = api.SplitTags(tags)
				cards[id] = c
			}
		}
		_ = rows.Err()
		_ = rows.Close()
	}

	var missing []string
	for _, id := range ids {
		if _, ok := cards[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		fillCards(ctx, missing, cards)
	}

	out := make([]render.HomeArticle, 0, len(ids))
	for _, id := range ids {
		if c, ok := cards[id]; ok {
			out = append(out, c)
		}
	}
	return out
}

// fillCards builds the cards of posts that have none from their bodies, and
// stores them. The operator's own excerpt and feature image win over those
// derived from the body.
func fillCards(ctx context.Context, ids []string, cards map[string]render.HomeArticle) {
	type built struct {
		id, tags, updated string
		card              render.HomeArticle
	}
	var fresh []built
	in, args := placeholders(ids)
	rows, err := dbpkg.Reader().QueryContext(ctx,
		`SELECT id,title,slug,content,tags,created_at,CAST(updated_at AS TEXT),COALESCE(excerpt,''),COALESCE(feature_image,'') FROM articles WHERE id IN (`+in+`)`, args...)
	if err != nil {
		return
	}
	for rows.Next() {
		var b built
		var content, excerpt, image string
		if rows.Scan(&b.id, &b.card.Title, &b.card.Slug, &content, &b.tags, &b.card.CreatedAt, &b.updated, &excerpt, &image) != nil {
			continue
		}
		b.card.Tags = api.SplitTags(b.tags)
		b.card.Excerpt = strings.TrimSpace(excerpt)
		if b.card.Excerpt == "" {
			b.card.Excerpt = excerptFromHTML(content, 160)
		}
		b.card.Image = strings.TrimSpace(image)
		if b.card.Image == "" {
			b.card.Image = seo.ExtractFirstImage(content)
		}
		cards[b.id] = b.card
		fresh = append(fresh, b)
	}
	_ = rows.Err()
	_ = rows.Close()
	if len(fresh) == 0 || dbpkg.WDB.DB == nil {
		return
	}

	tx, err := dbpkg.WDB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	for _, b := range fresh {
		_, _ = tx.ExecContext(ctx, storeCardSQL, b.id, b.card.Title, b.card.Slug, b.tags, b.card.CreatedAt, b.card.Excerpt, b.card.Image, b.id, b.updated)
	}
	_ = tx.Commit()
}
