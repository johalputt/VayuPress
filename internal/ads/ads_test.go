package ads

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE ad_slots(id TEXT PRIMARY KEY,name TEXT NOT NULL,placement TEXT NOT NULL DEFAULT 'below_post',kind TEXT NOT NULL DEFAULT 'image',image_url TEXT NOT NULL DEFAULT '',link_url TEXT NOT NULL DEFAULT '',alt_text TEXT NOT NULL DEFAULT '',html TEXT NOT NULL DEFAULT '',enabled INTEGER NOT NULL DEFAULT 1,sort INTEGER NOT NULL DEFAULT 0,owner_email TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'approved',order_ref TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return New(db)
}

func TestSlotCRUDAndPlacement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sl, err := s.Create(ctx, SlotInput{Name: "Banner", Placement: PlacementBelowPost, Kind: KindImage, ImageURL: "/media/ad.png", LinkURL: "https://sponsor.example", AltText: "Sponsor", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.EnabledByPlacement(ctx, PlacementBelowPost)
	if err != nil || len(got) != 1 {
		t.Fatalf("placement query: %v len=%d", err, len(got))
	}
	if err := s.SetEnabled(ctx, sl.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	got, _ = s.EnabledByPlacement(ctx, PlacementBelowPost)
	if len(got) != 0 {
		t.Errorf("disabled slot should not be returned, got %d", len(got))
	}
}

func TestMemberAdLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ad, err := s.CreateMemberAd(ctx, MemberAdInput{OwnerEmail: "Reader@Example.com", Placement: PlacementFooter, ImageURL: "/media/promo.png", LinkURL: "https://sponsor.example", AltText: "Promo", OrderRef: "VP-AD-1"})
	if err != nil {
		t.Fatalf("create member ad: %v", err)
	}
	if ad.Status != StatusPendingPayment || ad.Enabled || ad.OwnerEmail != "reader@example.com" {
		t.Fatalf("bad member ad: %+v", ad)
	}
	// Not rendered while pending.
	if got, _ := s.EnabledByPlacement(ctx, PlacementFooter); len(got) != 0 {
		t.Error("a pending member ad must not render")
	}
	// Payment moves it into the moderation queue.
	if err := s.MarkAdPaidByOrder(ctx, "VP-AD-1"); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	if q, _ := s.ListByStatus(ctx, StatusPendingReview); len(q) != 1 {
		t.Errorf("expected 1 ad awaiting review, got %d", len(q))
	}
	if got, _ := s.EnabledByPlacement(ctx, PlacementFooter); len(got) != 0 {
		t.Error("a paid-but-unreviewed ad must not render yet")
	}
	// Approval publishes it.
	if err := s.ApproveMemberAd(ctx, ad.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got, _ := s.EnabledByPlacement(ctx, PlacementFooter); len(got) != 1 {
		t.Errorf("an approved member ad must render, got %d", len(got))
	}
	// A member sees their own ad; an unsafe image URL is refused.
	if mine, _ := s.MemberAds(ctx, "reader@example.com"); len(mine) != 1 {
		t.Errorf("member should see their ad, got %d", len(mine))
	}
	if _, err := s.CreateMemberAd(ctx, MemberAdInput{OwnerEmail: "x@y.com", Placement: PlacementFooter, ImageURL: "javascript:alert(1)"}); err == nil {
		t.Error("unsafe image URL must be refused")
	}
}

func TestCreateValidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Create(ctx, SlotInput{Name: "x", Placement: "nowhere", Kind: KindImage}); err == nil {
		t.Error("expected invalid placement error")
	}
	if _, err := s.Create(ctx, SlotInput{Name: "x", Placement: PlacementHeader, Kind: "flash"}); err == nil {
		t.Error("expected invalid kind error")
	}
}

func TestRenderImageRejectsUnsafeURL(t *testing.T) {
	slots := []Slot{{Kind: KindImage, ImageURL: "javascript:alert(1)", AltText: "x"}}
	if out := Render(slots, RenderConfig{}); out != "" {
		t.Errorf("unsafe image URL must not render, got %q", out)
	}
	slots = []Slot{{Kind: KindImage, ImageURL: "/media/ok.png", LinkURL: "https://ok.example", AltText: "Ad"}}
	out := Render(slots, RenderConfig{})
	if !strings.Contains(out, `rel="sponsored nofollow noopener"`) || !strings.Contains(out, "/media/ok.png") {
		t.Errorf("expected safe image+link ad, got %q", out)
	}
}

func TestRenderHTMLFailsClosedWithoutSanitizer(t *testing.T) {
	slots := []Slot{{Kind: KindHTML, HTML: "<b>hi</b>"}}
	if out := Render(slots, RenderConfig{Sanitize: nil}); out != "" {
		t.Errorf("html creative must fail closed without sanitizer, got %q", out)
	}
	out := Render(slots, RenderConfig{Sanitize: func(s string) string { return s }})
	if !strings.Contains(out, "<b>hi</b>") {
		t.Errorf("expected sanitised html creative, got %q", out)
	}
}

func TestAdSenseGating(t *testing.T) {
	slots := []Slot{{Kind: KindAdSense, HTML: "1234567890"}}
	// Off: nothing renders.
	if out := Render(slots, RenderConfig{GoogleAdsEnabled: false, AdsenseClient: "ca-pub-123"}); out != "" {
		t.Errorf("adsense must not render when disabled, got %q", out)
	}
	// Bad client id: nothing renders.
	if out := Render(slots, RenderConfig{GoogleAdsEnabled: true, AdsenseClient: "pub-bad"}); out != "" {
		t.Errorf("adsense must not render with bad client, got %q", out)
	}
	cfg := RenderConfig{GoogleAdsEnabled: true, AdsenseClient: "ca-pub-123", Nonce: "abc"}
	out := Render(slots, cfg)
	if !strings.Contains(out, `data-ad-client="ca-pub-123"`) || !strings.Contains(out, `nonce="abc"`) {
		t.Errorf("expected adsense unit with client + nonce, got %q", out)
	}
	if !HasAdSense(slots, cfg) {
		t.Error("HasAdSense should be true")
	}
	if loader := AdSenseLoader("abc", "ca-pub-123"); !strings.Contains(loader, "googlesyndication.com") {
		t.Errorf("loader missing, got %q", loader)
	}
}
