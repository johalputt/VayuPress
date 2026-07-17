package domain

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// schemaSQL mirrors migration 059-vayudomains.up.sql plus the columns later
// migrations add (064: sync_state). Kept in sync by hand so the test exercises
// the exact table shape the migrations create.
const schemaSQL = `CREATE TABLE domains(id TEXT PRIMARY KEY,host TEXT NOT NULL UNIQUE,site_type TEXT NOT NULL DEFAULT 'blog',mail_enabled INTEGER NOT NULL DEFAULT 0,tls_state TEXT NOT NULL DEFAULT 'pending',sync_state TEXT NOT NULL DEFAULT 'approved',config_json TEXT NOT NULL DEFAULT '',is_primary INTEGER NOT NULL DEFAULT 0,status TEXT NOT NULL DEFAULT 'active',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatalf("schema: %v", err)
	}
	// rdb nil → reader() falls back to db (each :memory: conn is distinct, so a
	// second connection would see an empty database).
	return New(db, nil)
}

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"Example.com":       "example.com",
		"example.com:8080":  "example.com",
		"example.com.":      "example.com",
		" JOHAL.IN ":        "johal.in",
		"talk.johal.in:443": "talk.johal.in",
		"":                  "",
	}
	for in, want := range cases {
		if got := NormalizeHost(in); got != want {
			t.Errorf("NormalizeHost(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestEnsurePrimaryIdempotent(t *testing.T) {
	r := newTestRegistry(t)
	ctx := context.Background()

	if err := r.EnsurePrimary(ctx, "johal.in", "blog"); err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	if err := r.EnsurePrimary(ctx, "johal.in", "blog"); err != nil {
		t.Fatalf("seed 2: %v", err)
	}
	n, err := r.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 domain after double seed, got %d", n)
	}
	p, ok := r.Primary(ctx)
	if !ok {
		t.Fatal("expected a primary domain")
	}
	if p.Host != "johal.in" || !p.IsPrimary || p.TLSState != TLSPrimary || p.Status != StatusActive {
		t.Fatalf("unexpected primary: %+v", p)
	}
}

func TestEnsurePrimaryTracksSiteMode(t *testing.T) {
	r := newTestRegistry(t)
	ctx := context.Background()
	if err := r.EnsurePrimary(ctx, "johal.in", "blog"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Operator flips site.mode to business; the next boot re-seeds.
	if err := r.EnsurePrimary(ctx, "johal.in", "business"); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	p, _ := r.Primary(ctx)
	if p.EffectiveSiteType() != SiteBusiness {
		t.Fatalf("expected site_type to track site.mode=business, got %q", p.SiteType)
	}
	// Empty/blank mode coerces to blog.
	if err := r.EnsurePrimary(ctx, "johal.in", ""); err != nil {
		t.Fatalf("reseed empty: %v", err)
	}
	p, _ = r.Primary(ctx)
	if p.EffectiveSiteType() != SiteBlog {
		t.Fatalf("expected blank mode to coerce to blog, got %q", p.SiteType)
	}
}

func TestEnsurePrimaryMovesHostOnDomainChange(t *testing.T) {
	r := newTestRegistry(t)
	ctx := context.Background()
	if err := r.EnsurePrimary(ctx, "old.example", "blog"); err != nil {
		t.Fatalf("seed old: %v", err)
	}
	if err := r.EnsurePrimary(ctx, "new.example", "blog"); err != nil {
		t.Fatalf("seed new: %v", err)
	}
	p, ok := r.Primary(ctx)
	if !ok || p.Host != "new.example" {
		t.Fatalf("expected primary to move to new.example, got %+v", p)
	}
	// The old host must no longer be flagged primary.
	list, err := r.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, d := range list {
		if d.Host == "old.example" && d.IsPrimary {
			t.Fatal("old host is still flagged primary")
		}
	}
}

func TestResolveFallbackAndDisabled(t *testing.T) {
	r := newTestRegistry(t)
	ctx := context.Background()
	if err := r.EnsurePrimary(ctx, "johal.in", "blog"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sec, err := r.Create(ctx, "Second.Example", SiteBusiness, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Resolves case-insensitively and with a port.
	if d, err := r.Resolve(ctx, "SECOND.example:443"); err != nil || d.Host != "second.example" {
		t.Fatalf("resolve secondary: d=%+v err=%v", d, err)
	}
	// Unknown host → ErrNotFound (caller falls back to primary).
	if _, err := r.Resolve(ctx, "unknown.test"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for unknown host, got %v", err)
	}
	// Disabled host resolves as not-found.
	if err := r.SetStatus(ctx, sec.ID, StatusDisabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := r.Resolve(ctx, "second.example"); err != ErrNotFound {
		t.Fatalf("expected disabled host to resolve as ErrNotFound, got %v", err)
	}
}

func TestCreateRejectsDuplicateAndBadType(t *testing.T) {
	r := newTestRegistry(t)
	ctx := context.Background()
	if _, err := r.Create(ctx, "dup.example", SiteBlog, false); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := r.Create(ctx, "DUP.example", SiteBlog, false); err == nil {
		t.Fatal("expected duplicate host to be rejected")
	}
	if _, err := r.Create(ctx, "bad.example", "nonsense", false); err == nil {
		t.Fatal("expected invalid site type to be rejected")
	}
	if _, err := r.Create(ctx, "", SiteBlog, false); err == nil {
		t.Fatal("expected empty host to be rejected")
	}
}

func TestPrimaryIsProtected(t *testing.T) {
	r := newTestRegistry(t)
	ctx := context.Background()
	if err := r.EnsurePrimary(ctx, "johal.in", "blog"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	p, _ := r.Primary(ctx)
	if err := r.Delete(ctx, p.ID); err == nil {
		t.Fatal("expected primary delete to be refused")
	}
	if err := r.SetStatus(ctx, p.ID, StatusDisabled); err == nil {
		t.Fatal("expected primary disable to be refused")
	}
	if _, err := r.Update(ctx, p.ID, SiteBusiness, false); err == nil {
		t.Fatal("expected primary update to be refused (managed via Website)")
	}
}

// TestManualSyncGate covers the P5 gate: a new secondary starts on hold, the
// operator approves/pauses it, the primary is refused, and pre-migration rows
// (empty sync_state) read as approved so already-provisioned domains keep
// being maintained.
func TestManualSyncGate(t *testing.T) {
	r := newTestRegistry(t)
	ctx := context.Background()
	if err := r.EnsurePrimary(ctx, "johal.in", "blog"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A newly registered domain is on manual hold — adding it must never
	// auto-provision anything.
	sec, err := r.Create(ctx, "shop.example", SiteBlog, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sec.SyncState != SyncHold || sec.IsSyncApproved() {
		t.Fatalf("new secondary should start on hold, got %+v", sec)
	}

	// Approve, then pause again; both transitions round-trip through the DB.
	if err := r.SetSyncState(ctx, sec.ID, SyncApproved); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if d, _ := r.Resolve(ctx, "shop.example"); !d.IsSyncApproved() {
		t.Fatalf("expected approved after SetSyncState, got %+v", d)
	}
	if err := r.SetSyncState(ctx, sec.ID, SyncHold); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if d, _ := r.Resolve(ctx, "shop.example"); d.IsSyncApproved() {
		t.Fatalf("expected hold after SetSyncState, got %+v", d)
	}

	// Invalid states and the primary are refused.
	if err := r.SetSyncState(ctx, sec.ID, "bogus"); err == nil {
		t.Fatal("expected invalid sync state to be rejected")
	}
	p, _ := r.Primary(ctx)
	if err := r.SetSyncState(ctx, p.ID, SyncHold); err == nil {
		t.Fatal("expected primary sync-state change to be refused")
	}

	// Backfill semantics: an empty sync_state (a row written before migration
	// 064) reads as approved, exactly like the migration's DEFAULT backfill.
	if (Domain{SyncState: ""}).EffectiveSyncState() != SyncApproved {
		t.Fatal("empty sync_state must read as approved")
	}
	if (Domain{SyncState: SyncHold}).IsSyncApproved() {
		t.Fatal("hold must not read as approved")
	}
}

func TestUpdateAndDeleteSecondary(t *testing.T) {
	r := newTestRegistry(t)
	ctx := context.Background()
	sec, err := r.Create(ctx, "shop.example", SiteBlog, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.Update(ctx, sec.ID, SiteBusiness, true)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.EffectiveSiteType() != SiteBusiness || !got.MailEnabled {
		t.Fatalf("update did not apply: %+v", got)
	}
	if err := r.Delete(ctx, sec.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.Resolve(ctx, "shop.example"); err != ErrNotFound {
		t.Fatalf("expected removed host to be gone, got %v", err)
	}
}
