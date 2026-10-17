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
	"github.com/johalputt/vayupress/internal/pace"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/seo"
)

// tagPageIDsSQL chooses a tag page's posts from idx_article_tags_live alone, in
// their order, for the whole install or (domain_id) one hosted site.
const tagPageIDsSQL = `SELECT t.article_id FROM article_tags t WHERE t.tag_norm=? AND t.live=1`

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

// missingCardsSQL finds published posts without a card, newest first, from
// idx_articles_feed and article_cards' key alone; the cursor is the last
// post's created_at text and id, in the index's own order.
const (
	missingCardsSQL = `SELECT a.id, CAST(a.created_at AS TEXT) FROM articles a WHERE a.is_page=0 AND a.status='published' ` +
		`AND NOT EXISTS(SELECT 1 FROM article_cards c WHERE c.article_id=a.id)`
	missingCardsAfter = ` AND (a.created_at < ? OR (a.created_at = ? AND a.id > ?))`
	missingCardsOrder = ` ORDER BY a.created_at DESC, a.id LIMIT ?`
	cardFillBatch     = 25 // cards per pace unit
)

// newCardJob paces a card fill. A var so a test can pace it with its own pacer.
var newCardJob = func() *pace.Job {
	return pace.Host().NewJob(pace.JobConfig{Min: 1, Max: 8, Step: 1})
}

// fillMissingCards builds the card of every published post that has none, in
// batches the host pacer sizes, and reports how many it built. A card was
// built only when a listing first showed its post, so a crawler walking tag
// pages of a large archive read up to 200 bodies per page: on 235k posts a cold
// tag page cost 125 ms with its cards missing and 17 ms with them stored. An
// edit deletes its post's card (migration 103), and the next pass rebuilds it.
//
// complete is true only when the pass ran out of posts without a card. A pass
// the pacer stops (a busy host) or a cancelled one ends with the rest still
// missing, and said nothing: "the warmer builds them" was not readable as
// "the warmer has built them".
func fillMissingCards(ctx context.Context) (built int, complete bool) {
	if dbpkg.DB == nil {
		return 0, false
	}
	job := newCardJob()
	var created, last string
	for {
		n, err := job.Next(ctx)
		if err != nil {
			return built, false
		}
		q, args := missingCardsSQL, []any{}
		if last != "" {
			q += missingCardsAfter
			args = append(args, created, created, last)
		}
		rows, err := dbpkg.Reader().QueryContext(ctx, q+missingCardsOrder, append(args, n*cardFillBatch)...)
		if err != nil {
			return built, false
		}
		var ids []string
		for rows.Next() {
			if rows.Scan(&last, &created) == nil {
				ids = append(ids, last)
			}
		}
		_ = rows.Err()
		_ = rows.Close()
		if len(ids) == 0 {
			return built, true
		}
		cards := make(map[string]render.HomeArticle, len(ids))
		fillCards(ctx, ids, cards)
		built += len(cards)
	}
}
