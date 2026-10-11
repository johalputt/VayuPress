// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"

	"github.com/johalputt/vayupress/internal/textfix"
)

// OfferTextRepair records what textfix.Changes found in a post (migration
// 110): the post is listed on the Posts page for its author to restore or
// keep. Nothing in the post is changed here.
//
// A post the author kept as written stays kept while the runs it holds are
// the ones it was kept with (sig); different runs, written since, are offered
// again. A post with nothing left to restore leaves the list.
//
// x is the handle the post was written through: the server's WDB, or the
// database an import opened (the CLI importers, a content bundle).
func OfferTextRepair(ctx context.Context, x interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, slug string, changes []textfix.Change) error {
	if len(changes) == 0 {
		_, err := x.ExecContext(ctx, `DELETE FROM text_repair_offer WHERE slug=?`, slug)
		return err
	}
	h := sha256.New()
	for _, c := range changes {
		h.Write([]byte(c.From))
		h.Write([]byte{0})
	}
	sig := hex.EncodeToString(h.Sum(nil))
	_, err := x.ExecContext(ctx, `INSERT INTO text_repair_offer(slug, runs, sig) VALUES(?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET runs=excluded.runs, sig=excluded.sig,
		state=CASE WHEN text_repair_offer.state='kept' AND text_repair_offer.sig=excluded.sig THEN 'kept' ELSE 'offered' END`,
		slug, len(changes), sig)
	return err
}

// TextChanges is every run a post's title and body would have restored.
func TextChanges(title, content string) []textfix.Change {
	return append(textfix.Changes(title), textfix.Changes(content)...)
}

// KeepTextAsWritten records the author's choice to keep a post's text as it
// is: it is not offered again while those runs are what it holds.
func KeepTextAsWritten(ctx context.Context, slug string) error {
	_, err := WDB.ExecContext(ctx, `UPDATE text_repair_offer SET state='kept' WHERE slug=?`, slug)
	return err
}

// TextOfferWhere is the Posts page's filter for the posts waiting for review.
func TextOfferWhere() string {
	return "slug IN (SELECT slug FROM text_repair_offer WHERE state='offered')"
}
