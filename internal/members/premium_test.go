package members

import (
	"context"
	"testing"
)

func TestTierCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	tier, err := s.CreateTier(ctx, TierInput{
		Name: "Founding Member", Description: "Early supporters",
		MonthlyCents: 1500, YearlyCents: 15000, Currency: "usd",
		Benefits: []string{"All premium posts", "  ", "Founder badge"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tier.Slug != "founding-member" {
		t.Errorf("slug = %q, want founding-member", tier.Slug)
	}
	if tier.Currency != "USD" {
		t.Errorf("currency should be upper-cased, got %q", tier.Currency)
	}
	if len(tier.Benefits) != 2 {
		t.Errorf("blank benefits should be dropped, got %v", tier.Benefits)
	}
	if tier.MonthlyPrice() != "15.00" {
		t.Errorf("monthly price = %q, want 15.00", tier.MonthlyPrice())
	}

	// Duplicate name yields a unique slug.
	tier2, err := s.CreateTier(ctx, TierInput{Name: "Founding Member"})
	if err != nil {
		t.Fatal(err)
	}
	if tier2.Slug == tier.Slug {
		t.Error("duplicate tier names must get distinct slugs")
	}

	if err := s.UpdateTier(ctx, tier.ID, TierInput{Name: "Founders", MonthlyCents: 2000, Visibility: VisibilityHidden}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetTierByID(ctx, tier.ID)
	if got.Name != "Founders" || got.MonthlyCents != 2000 || got.Visibility != VisibilityHidden {
		t.Errorf("update did not persist: %+v", got)
	}

	// Public listing excludes hidden tiers; full listing includes them.
	pub, _ := s.ListTiers(ctx, false)
	for _, tr := range pub {
		if tr.Slug == tier.Slug {
			t.Error("hidden tier must not appear in public listing")
		}
	}
	all, _ := s.ListTiers(ctx, true)
	if len(all) <= len(pub) {
		t.Error("full listing should include hidden tiers")
	}
}

func TestArchiveBuiltinTierRejected(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	free, _ := s.GetTier(ctx, TierFree)
	if err := s.ArchiveTier(ctx, free.ID); err == nil {
		t.Error("archiving the built-in free tier should be rejected")
	}
}

func TestSubscriptionLifecycleAndMRR(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m, _ := s.Upsert(ctx, "reader@example.com")

	// Yearly subscription contributes 1/12 of its amount to MRR.
	if err := s.StartSubscription(ctx, SubscriptionInput{
		MemberID: m.ID, TierSlug: TierPaid, Cadence: CadenceYearly, AmountCents: 12000, Currency: "USD",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetByID(ctx, m.ID)
	if !got.IsPaid() {
		t.Error("member should be paid after subscribing")
	}
	sub, _ := s.ActiveSubscription(ctx, m.ID)
	if sub == nil || sub.TierSlug != TierPaid {
		t.Fatalf("expected active paid subscription, got %+v", sub)
	}
	stats, _ := s.Stats(ctx)
	if stats.MRRCents != 1000 {
		t.Errorf("MRR = %d, want 1000 (12000/12)", stats.MRRCents)
	}
	if stats.Paid != 1 || stats.Free != 0 {
		t.Errorf("expected 1 paid / 0 free, got %d/%d", stats.Paid, stats.Free)
	}

	// Cancelling drops them back to free and zeroes MRR.
	if err := s.CancelSubscription(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetByID(ctx, m.ID)
	if got.IsPaid() {
		t.Error("member should be free after cancellation")
	}
	stats, _ = s.Stats(ctx)
	if stats.MRRCents != 0 {
		t.Errorf("MRR after cancel = %d, want 0", stats.MRRCents)
	}
}

func TestComplimentaryTierDoesNotInflateMRR(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.SetTier(ctx, "vip@example.com", TierFree); err == nil {
		// vip not created yet — SetTier should fail on unknown member.
		t.Error("SetTier on a non-existent member should error")
	}
	m, _ := s.Upsert(ctx, "vip@example.com")
	if err := s.SetTier(ctx, m.Email, TierPaid); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, m.Email)
	if !got.IsPaid() {
		t.Error("manual paid grant should mark member paid")
	}
	stats, _ := s.Stats(ctx)
	if stats.MRRCents != 0 {
		t.Errorf("complimentary grant should not add MRR, got %d", stats.MRRCents)
	}
	if stats.Paid != 1 {
		t.Errorf("expected 1 paid member, got %d", stats.Paid)
	}
}

func TestProfileAndNewsletterOptIn(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m, _ := s.Upsert(ctx, "person@example.com")
	if !m.NewsletterOptIn {
		t.Error("new members should default to newsletter opt-in")
	}
	if m.DisplayName() != "person" {
		t.Errorf("DisplayName fallback = %q, want person", m.DisplayName())
	}
	if err := s.UpdateProfile(ctx, m.Email, "Alex Doe", "met at conference"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNewsletterOptIn(ctx, m.Email, false); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, m.Email)
	if got.Name != "Alex Doe" || got.Note != "met at conference" {
		t.Errorf("profile not saved: %+v", got)
	}
	if got.NewsletterOptIn {
		t.Error("newsletter opt-in should be off")
	}
	if got.DisplayName() != "Alex Doe" {
		t.Errorf("DisplayName = %q, want Alex Doe", got.DisplayName())
	}
}

func TestLabels(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m, _ := s.Upsert(ctx, "tagged@example.com")
	if err := s.AddLabel(ctx, m.ID, "Founding"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLabel(ctx, m.ID, "founding"); err != nil { // idempotent (case-normalised)
		t.Fatal(err)
	}
	if err := s.AddLabel(ctx, m.ID, "vip"); err != nil {
		t.Fatal(err)
	}
	labels, _ := s.LabelsForMember(ctx, m.ID)
	if len(labels) != 2 {
		t.Errorf("expected 2 labels, got %v", labels)
	}
	got, _ := s.Get(ctx, m.Email)
	if len(got.Labels) != 2 {
		t.Errorf("Get should attach labels, got %v", got.Labels)
	}
	if err := s.RemoveLabel(ctx, m.ID, "vip"); err != nil {
		t.Fatal(err)
	}
	labels, _ = s.LabelsForMember(ctx, m.ID)
	if len(labels) != 1 || labels[0] != "founding" {
		t.Errorf("after removal expected [founding], got %v", labels)
	}
}

func TestSignupsByDayContinuity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, _ = s.Upsert(ctx, "a@example.com")
	_, _ = s.Upsert(ctx, "b@example.com")
	series, err := s.SignupsByDay(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 7 {
		t.Fatalf("expected 7 days, got %d", len(series))
	}
	total := 0
	for _, d := range series {
		total += d.Count
	}
	if total != 2 {
		t.Errorf("expected 2 signups across the window, got %d", total)
	}
}

func TestUpgradeByEmailRecordsSubscription(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.UpgradeByEmail(ctx, "buyer@example.com", "cus_42"); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Get(ctx, "buyer@example.com")
	if !m.IsPaid() || m.StripeCustomer != "cus_42" {
		t.Errorf("upgrade did not persist: %+v", m)
	}
	sub, _ := s.ActiveSubscription(ctx, m.ID)
	if sub == nil || sub.AmountCents != 500 {
		t.Errorf("expected active subscription at the paid tier price, got %+v", sub)
	}
	stats, _ := s.Stats(ctx)
	if stats.MRRCents != 500 {
		t.Errorf("MRR after Stripe upgrade = %d, want 500", stats.MRRCents)
	}
}
