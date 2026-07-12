package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// newMigratedDB spins up a fresh in-memory database with every migration
// applied through the real runner (so the articles table carries domain_id from
// migration 060), pinned to one connection so :memory: is shared.
func newMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })
	old := DB
	DB = d
	t.Cleanup(func() { DB = old })
	if err := runMigrations(); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}
	return d
}

func TestArticleDomainScoping(t *testing.T) {
	d := newMigratedDB(t)
	repo := &sqliteArticleRepo{db: d, rdb: d}
	ctx := context.Background()
	now := time.Now()
	mk := func(slug, domainID string) {
		if err := repo.Create(ctx, Article{ID: slug, Title: slug, Slug: slug, Content: "x", CreatedAt: now, UpdatedAt: now, Status: "published", DomainID: domainID}); err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
	}
	mk("home", "")                 // primary / unassigned
	mk("about", "")                // primary / unassigned
	mk("shop-welcome", "dom-shop") // secondary

	// ScopeAll (admin / JSON API) sees every article — byte-identical to the
	// pre-Stage-2 behaviour.
	if _, total, err := repo.List(ctx, 1, 50, ""); err != nil || total != 3 {
		t.Fatalf("List (ScopeAll) total=%d err=%v, want 3", total, err)
	}

	// Primary scope ("") serves only the unassigned rows.
	prim, ptot, err := repo.ListScoped(ctx, "", 1, 50, "")
	if err != nil || ptot != 2 {
		t.Fatalf("primary scope total=%d err=%v, want 2", ptot, err)
	}
	for _, a := range prim {
		if a.Slug == "shop-welcome" {
			t.Fatal("primary domain leaked a secondary-domain article")
		}
	}

	// Secondary scope serves only its own rows.
	sec, stot, err := repo.ListScoped(ctx, "dom-shop", 1, 50, "")
	if err != nil || stot != 1 || len(sec) != 1 || sec[0].Slug != "shop-welcome" {
		t.Fatalf("secondary scope total=%d rows=%d err=%v, want the one shop post", stot, len(sec), err)
	}

	// GetScoped enforces isolation both ways.
	if _, err := repo.GetScoped(ctx, "dom-shop", "home"); err != ErrNotFound {
		t.Fatalf("secondary reached a primary article: err=%v", err)
	}
	if _, err := repo.GetScoped(ctx, "", "shop-welcome"); err != ErrNotFound {
		t.Fatalf("primary reached a secondary article: err=%v", err)
	}
	if a, err := repo.GetScoped(ctx, ScopeAll, "shop-welcome"); err != nil || a.DomainID != "dom-shop" {
		t.Fatalf("ScopeAll should reach any article: a=%+v err=%v", a, err)
	}

	// SetDomain moves a post between domains; counts reflect it.
	if err := repo.SetDomain(ctx, "about", "dom-shop"); err != nil {
		t.Fatalf("SetDomain: %v", err)
	}
	counts, err := repo.CountsByDomain(ctx)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts[""] != 1 || counts["dom-shop"] != 2 {
		t.Fatalf("counts after reassign: primary=%d shop=%d, want 1 and 2", counts[""], counts["dom-shop"])
	}

	// Still byte-identical for the admin path.
	if _, total, _ := repo.List(ctx, 1, 50, ""); total != 3 {
		t.Fatalf("List (ScopeAll) after reassign total=%d, want 3", total)
	}
}
