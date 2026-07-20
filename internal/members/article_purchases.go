package members

// article_purchases.go — per-post paid content (Phase 6).
//
// A post's access LEVEL (public/members/paid) gates who may read it; an optional
// per-post PRICE lets a reader buy one-time access to a single post without a
// subscription. Purchase settles asynchronously (Stripe one-time / webhook /
// operator-confirmed offline order) so a purchase row is opened `pending` at
// checkout and flipped to `paid` by the shared fulfilment path, keyed to the
// order reference. HasPurchasedArticle then unlocks the post for that member.

import (
	"context"
	"fmt"
	"strings"
)

// GetPostPriceCents returns the one-time price (minor units) for a post, or 0
// when it is not individually purchasable.
func (s *Store) GetPostPriceCents(ctx context.Context, slug string) int {
	var c int
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(price_cents,0) FROM article_access WHERE slug=?`, slug).Scan(&c)
	return c
}

// SetPostPrice sets a post's one-time price (0 disables individual purchase). It
// creates the access row at the default public level if none exists yet, so a
// price can be attached independently of the level control.
func (s *Store) SetPostPrice(ctx context.Context, slug string, cents int) error {
	if strings.TrimSpace(slug) == "" {
		return fmt.Errorf("slug required")
	}
	if cents < 0 {
		cents = 0
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO article_access(slug,level,price_cents) VALUES(?, 'public', ?) ON CONFLICT(slug) DO UPDATE SET price_cents=excluded.price_cents`,
		slug, cents)
	return err
}

// CreateArticlePurchase opens (or re-points) a pending purchase for a post, tied
// to the order reference. A purchase already marked paid is left untouched.
func (s *Store) CreateArticlePurchase(ctx context.Context, email, slug, orderRef string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	slug = strings.TrimSpace(slug)
	if email == "" || slug == "" {
		return fmt.Errorf("email and slug required")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO article_purchases(id,email,slug,order_ref,status) VALUES(?,?,?,?, 'pending')
		 ON CONFLICT(email,slug) DO UPDATE SET order_ref=excluded.order_ref WHERE article_purchases.status<>'paid'`,
		"apx_"+randHex(12), email, slug, strings.TrimSpace(orderRef))
	return err
}

// MarkArticlePurchasePaidByOrder flips a pending post purchase to paid on payment
// confirmation. Idempotent — an already-paid purchase is untouched.
func (s *Store) MarkArticlePurchasePaidByOrder(ctx context.Context, orderRef string) error {
	orderRef = strings.TrimSpace(orderRef)
	if orderRef == "" {
		return fmt.Errorf("order reference required")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE article_purchases SET status='paid', paid_at=CURRENT_TIMESTAMP WHERE order_ref=? AND status='pending'`,
		orderRef)
	return err
}

// PricedPost is a post the operator has put an individual price on.
type PricedPost struct {
	Slug       string
	Level      string
	PriceCents int
}

// ListPricedPosts returns posts with a one-time price set, for the operator's
// paid-content control panel.
func (s *Store) ListPricedPosts(ctx context.Context, limit int) ([]PricedPost, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT slug, level, price_cents FROM article_access WHERE price_cents>0 ORDER BY slug LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PricedPost
	for rows.Next() {
		var p PricedPost
		if rows.Scan(&p.Slug, &p.Level, &p.PriceCents) == nil {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

// HasPurchasedArticle reports whether a member has PAID for one-time access to a
// post, so the paywall can unlock it for them.
func (s *Store) HasPurchasedArticle(ctx context.Context, email, slug string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || slug == "" {
		return false
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM article_purchases WHERE email=? AND slug=? AND status='paid'`, email, slug).Scan(&n)
	return n > 0
}
