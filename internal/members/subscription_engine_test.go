package members

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// A free trial grants paid access but contributes no MRR until it converts.
func TestTrialDoesNotCountTowardMRR(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m, _ := s.Upsert(ctx, "trialer@example.com")

	if err := s.StartSubscription(ctx, SubscriptionInput{
		MemberID: m.ID, TierSlug: TierPaid, Cadence: CadenceMonthly,
		AmountCents: 1000, Currency: "USD", TrialDays: 14,
	}); err != nil {
		t.Fatal(err)
	}
	sub, _ := s.ActiveSubscription(ctx, m.ID)
	if sub == nil || !sub.IsTrialing() {
		t.Fatalf("expected a trialing subscription, got %+v", sub)
	}
	if sub.MonthlyValueCents() != 0 {
		t.Errorf("trialing sub MRR = %d, want 0", sub.MonthlyValueCents())
	}
	got, _ := s.GetByID(ctx, m.ID)
	if !got.IsPaid() {
		t.Error("a trialing member should still have paid access")
	}
	stats, _ := s.Stats(ctx)
	if stats.Trialing != 1 {
		t.Errorf("Trialing = %d, want 1", stats.Trialing)
	}
	if stats.MRRCents != 0 {
		t.Errorf("trial must not add MRR, got %d", stats.MRRCents)
	}
}

// Scheduling a cancellation keeps access; the flag is recorded on the sub.
func TestScheduleCancellationKeepsAccess(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m, _ := s.Upsert(ctx, "member@example.com")
	if err := s.StartSubscription(ctx, SubscriptionInput{
		MemberID: m.ID, TierSlug: TierPaid, Cadence: CadenceMonthly, AmountCents: 800, Currency: "USD",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ScheduleCancellation(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	sub, _ := s.ActiveSubscription(ctx, m.ID)
	if sub == nil || !sub.CancelAtPeriodEnd {
		t.Fatalf("expected cancel_at_period_end set, got %+v", sub)
	}
	got, _ := s.GetByID(ctx, m.ID)
	if !got.IsPaid() {
		t.Error("member should keep paid access until the period ends")
	}
	// Scheduling on a member with no active sub fails.
	free, _ := s.Upsert(ctx, "free@example.com")
	if err := s.ScheduleCancellation(ctx, free.ID); err == nil {
		t.Error("scheduling a cancellation with no active sub should error")
	}
}

// Conversion, ARPU and churn are derived from members + subscriptions + events.
func TestDerivedMetrics(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// 3 free members, 1 paid at $10/mo.
	for _, e := range []string{"a@x.com", "b@x.com", "c@x.com"} {
		_, _ = s.Upsert(ctx, e)
	}
	paid, _ := s.Upsert(ctx, "payer@x.com")
	if err := s.StartSubscription(ctx, SubscriptionInput{
		MemberID: paid.ID, TierSlug: TierPaid, Cadence: CadenceMonthly, AmountCents: 1000, Currency: "USD",
	}); err != nil {
		t.Fatal(err)
	}
	stats, _ := s.Stats(ctx)
	if stats.Paid != 1 || stats.Total != 4 {
		t.Fatalf("paid/total = %d/%d, want 1/4", stats.Paid, stats.Total)
	}
	if stats.ARPUCents != 1000 {
		t.Errorf("ARPU = %d, want 1000", stats.ARPUCents)
	}
	if stats.ConversionRate < 0.24 || stats.ConversionRate > 0.26 {
		t.Errorf("conversion = %.3f, want ~0.25", stats.ConversionRate)
	}
	if stats.NewMRRCents != 1000 {
		t.Errorf("new MRR (30d) = %d, want 1000", stats.NewMRRCents)
	}
	if stats.LTVCents <= 0 {
		t.Errorf("LTV should be positive with no churn, got %d", stats.LTVCents)
	}

	// Now cancel the payer: churn appears and MRR movement nets negative.
	if err := s.CancelSubscription(ctx, paid.ID); err != nil {
		t.Fatal(err)
	}
	stats, _ = s.Stats(ctx)
	if stats.CanceledLast30 != 1 {
		t.Errorf("canceled(30d) = %d, want 1", stats.CanceledLast30)
	}
	if stats.ChurnedMRRCents != 1000 {
		t.Errorf("churned MRR = %d, want 1000", stats.ChurnedMRRCents)
	}
	if stats.NetMRRMovementCents != 0 {
		t.Errorf("net MRR movement = %d, want 0 (1000 new - 1000 churned)", stats.NetMRRMovementCents)
	}
	if stats.ChurnRate30 <= 0 {
		t.Errorf("churn rate should be > 0 after a cancellation, got %.3f", stats.ChurnRate30)
	}
}

// Lifecycle actions append to the member activity log.
func TestActivityLog(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m, _ := s.Upsert(ctx, "active@x.com") // signup event
	if err := s.StartSubscription(ctx, SubscriptionInput{
		MemberID: m.ID, TierSlug: TierPaid, Cadence: CadenceMonthly, AmountCents: 500, Currency: "USD",
	}); err != nil { // subscribe event
		t.Fatal(err)
	}
	if err := s.CancelSubscription(ctx, m.ID); err != nil { // cancel event
		t.Fatal(err)
	}
	events, err := s.EventsForMember(ctx, m.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events (signup, subscribe, cancel), got %d: %+v", len(events), events)
	}
	// Newest first.
	if events[0].Type != EventCancel {
		t.Errorf("newest event = %q, want %q", events[0].Type, EventCancel)
	}
	feed, _ := s.RecentEvents(ctx, 10)
	if len(feed) != 3 || feed[0].Email != "active@x.com" {
		t.Errorf("global feed should join the email, got %+v", feed)
	}
}

// A complimentary grant logs a comp event, never a paying subscribe.
func TestCompEventNotCountedAsRevenue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m, _ := s.Upsert(ctx, "vip@x.com")
	if err := s.SetTier(ctx, m.Email, TierPaid); err != nil {
		t.Fatal(err)
	}
	events, _ := s.EventsForMember(ctx, m.ID, 10)
	var comps, subs int
	for _, e := range events {
		switch e.Type {
		case EventComp:
			comps++
		case EventSubscribe:
			subs++
		}
	}
	if comps != 1 || subs != 0 {
		t.Errorf("expected 1 comp / 0 subscribe events, got %d/%d", comps, subs)
	}
	stats, _ := s.Stats(ctx)
	if stats.NewMRRCents != 0 {
		t.Errorf("comp should not add new MRR, got %d", stats.NewMRRCents)
	}
}

func TestRevenueByTier(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a, _ := s.Upsert(ctx, "a@x.com")
	b, _ := s.Upsert(ctx, "b@x.com")
	_ = s.StartSubscription(ctx, SubscriptionInput{MemberID: a.ID, TierSlug: TierPaid, Cadence: CadenceMonthly, AmountCents: 500, Currency: "USD"})
	_ = s.StartSubscription(ctx, SubscriptionInput{MemberID: b.ID, TierSlug: TierPaid, Cadence: CadenceYearly, AmountCents: 12000, Currency: "USD"})
	rev, err := s.RevenueByTier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rev) != 1 || rev[0].Slug != TierPaid {
		t.Fatalf("expected one paid tier row, got %+v", rev)
	}
	// 500 (monthly) + 1000 (12000/12) = 1500.
	if rev[0].MRRCents != 1500 || rev[0].Members != 2 {
		t.Errorf("tier revenue = %d cents / %d members, want 1500/2", rev[0].MRRCents, rev[0].Members)
	}
}

func TestTierTrialAndStripeFieldsPersist(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tier, err := s.CreateTier(ctx, TierInput{
		Name: "Pro", MonthlyCents: 900, Currency: "USD", TrialDays: 7,
		StripeMonthlyPrice: "price_month_123", StripeYearlyPrice: "price_year_456",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetTierByID(ctx, tier.ID)
	if got.TrialDays != 7 || got.StripeMonthlyPrice != "price_month_123" || got.StripeYearlyPrice != "price_year_456" {
		t.Errorf("trial/stripe fields not persisted: %+v", got)
	}
}

func TestExportCSV(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	m, _ := s.Upsert(ctx, "csv@x.com")
	_ = s.UpdateProfile(ctx, m.Email, "CSV Person", "")
	_ = s.AddLabel(ctx, m.ID, "founding")
	_ = s.StartSubscription(ctx, SubscriptionInput{MemberID: m.ID, TierSlug: TierPaid, Cadence: CadenceMonthly, AmountCents: 700, Currency: "USD"})

	var buf bytes.Buffer
	if err := s.ExportCSV(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "email,name,tier,status,newsletter_opt_in,mrr_cents,currency,labels,created_at,last_seen_at") {
		t.Errorf("unexpected CSV header: %q", strings.SplitN(out, "\n", 2)[0])
	}
	if !strings.Contains(out, "csv@x.com") || !strings.Contains(out, "CSV Person") || !strings.Contains(out, "700") || !strings.Contains(out, "founding") {
		t.Errorf("CSV missing expected member data:\n%s", out)
	}
}
