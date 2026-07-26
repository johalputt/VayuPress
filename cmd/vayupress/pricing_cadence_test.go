// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/members"
)

// TestYearlySavingNeverOverstatesTheDiscount is the one with money attached. A
// page that advertises "save 20%" and charges a 19% discount is a false price
// claim, so the rounding must go down, and a yearly price that is not actually
// cheaper must advertise nothing at all.
func TestYearlySavingNeverOverstatesTheDiscount(t *testing.T) {
	cases := []struct {
		name    string
		monthly int
		yearly  int
		want    int
	}{
		{"two months free", 1000, 10000, 16},   // 120.00 -> 100.00 is 16.6%, floored
		{"exactly 25pc", 1000, 9000, 25},       // clean number, no rounding involved
		{"no discount at all", 1000, 12000, 0}, // same price billed yearly
		{"yearly costs more", 1000, 13000, 0},  // never render a negative "saving"
		{"one cent cheaper", 1000, 11999, 0},   // rounds down to nothing, correctly
		{"monthly only", 1000, 0, 0},
		{"yearly only", 0, 9000, 0},
		{"free tier", 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := yearlySavingPct(members.Tier{MonthlyCents: c.monthly, YearlyCents: c.yearly})
			if got != c.want {
				t.Errorf("yearlySavingPct(%d/%d) = %d, want %d", c.monthly, c.yearly, got, c.want)
			}
			// The floor is the property that matters, so assert it directly rather
			// than trusting the table: the advertised discount must never exceed the
			// real one.
			if c.monthly > 0 && c.yearly > 0 && c.yearly < c.monthly*12 {
				full := c.monthly * 12
				realPct := float64(full-c.yearly) / float64(full) * 100
				if float64(got) > realPct {
					t.Errorf("advertised %d%% but the real discount is only %.2f%%", got, realPct)
				}
			}
		})
	}
}

// TestEffectiveMonthlyComparesAcrossCadences: the featured card is chosen by
// price, so a yearly-only tier has to be comparable with a monthly-only one.
func TestEffectiveMonthlyComparesAcrossCadences(t *testing.T) {
	yearlyOnly := members.Tier{YearlyCents: 6000}  // 5.00/mo
	monthlyOnly := members.Tier{MonthlyCents: 800} // 8.00/mo
	if effectiveMonthlyCents(yearlyOnly) >= effectiveMonthlyCents(monthlyOnly) {
		t.Error("a 60.00/yr tier is cheaper per month than an 8.00/mo tier and must compare as such")
	}
	if got := effectiveMonthlyCents(members.Tier{}); got != 0 {
		t.Errorf("a free tier is 0, got %d", got)
	}
	// Monthly wins when both are set: it is the price the card shows by default.
	both := members.Tier{MonthlyCents: 500, YearlyCents: 60000}
	if got := effectiveMonthlyCents(both); got != 500 {
		t.Errorf("with both cadences the monthly price rules, got %d", got)
	}
}

func TestFormatQuotaMB(t *testing.T) {
	cases := []struct {
		mb   int
		want string
	}{
		{500, "500 MB"},
		{1024, "1 GB"},
		{5120, "5 GB"},
		{1536, "1.5 GB"},
		{0, "0 MB"},
	}
	for _, c := range cases {
		if got := formatQuotaMB(c.mb); got != c.want {
			t.Errorf("formatQuotaMB(%d) = %q, want %q", c.mb, got, c.want)
		}
	}
}

// TestPricingCadenceToggleCannotCauseABillingMismatch is the invariant behind the
// whole toggle: showing a yearly price while the button still points at a monthly
// checkout would bill the reader differently from what they read. The toggle must
// therefore move the destination with the price, and it must only do so for cards
// that genuinely have both.
func TestPricingCadenceToggleCannotCauseABillingMismatch(t *testing.T) {
	src, err := os.ReadFile("handlers_member_portal.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(src)
	i := strings.Index(s, "func (a *App) handlePricingPage(")
	if i < 0 {
		t.Fatal("handlePricingPage not found")
	}
	body := s[i:]
	if j := strings.Index(body, "\nfunc effectiveMonthlyCents"); j > 0 {
		body = body[:j]
	}

	// Both destinations must travel with the button…
	for _, want := range []string{"data-href-monthly=", "data-href-yearly="} {
		if !strings.Contains(body, want) {
			t.Errorf("the CTA must carry %s so the toggle can move the destination with the price", want)
		}
	}
	// …and they must be emitted only when the tier really is priced both ways.
	if !strings.Contains(body, "if bothCadences {") {
		t.Error("cadence hrefs must be gated on the tier having both prices")
	}
	// The script must rewrite the href, not just the visible price.
	if !strings.Contains(body, "setAttribute('href'") {
		t.Error("the toggle script must rewrite the CTA href")
	}
	// A toggle that ships visible but inert is worse than no toggle.
	if !strings.Contains(body, `id="pr-toggle"`) || !strings.Contains(body, "hidden>") {
		t.Error("the toggle must ship hidden and be revealed by its own script")
	}
	if !strings.Contains(body, "tog.hidden=false") {
		t.Error("the script must be what reveals the toggle")
	}

	// Exactly one card is featured. The bug this replaced highlighted every paid
	// tier, which is the same as highlighting none.
	if strings.Contains(body, `if !t.IsFree() {
			featured = " pr-card--featured"`) {
		t.Error("every paid tier is featured again; exactly one card should be")
	}
	if !strings.Contains(body, "i == featuredIdx") {
		t.Error("the featured card must be chosen once, not per-tier")
	}
	// Popularity is not something this page knows. Checked against emitted copy
	// only: a comment explaining why the badge is absent must not read as the
	// badge being present, which is exactly what a whole-body substring match
	// does.
	if strings.Contains(strings.ToLower(stripGoComments(body)), "most popular") {
		t.Error(`the page must not claim "most popular" — it has no popularity data`)
	}

	// Tier data that used to be dropped on the floor.
	if !strings.Contains(body, "t.TrialDays > 0") {
		t.Error("a tier's free trial must be shown; it is the strongest thing a plan offers")
	}
	if !strings.Contains(body, "t.MailEnabled") {
		t.Error("a tier that includes a mailbox must say so — it is the distinctive benefit")
	}
}

// stripGoComments drops whole-line // comments so a copy assertion tests what
// the page says rather than what the source explains about itself. It is
// deliberately line-oriented: Go string literals in this file legitimately
// contain "//" (in URLs), and a smarter parser would be a liability here.
func stripGoComments(src string) string {
	lines := strings.Split(src, "\n")
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		kept = append(kept, ln)
	}
	return strings.Join(kept, "\n")
}

// TestPricingToggleStylesExist: the handler renders these classes, so missing
// rules mean a control that looks broken rather than one that looks deliberate.
func TestPricingToggleStylesExist(t *testing.T) {
	b, err := os.ReadFile("../../static/css/signup.css")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	css := string(b)
	for _, want := range []string{
		".pr-toggle", ".pr-toggle-btn", ".pr-save", ".pr-trial",
		".pr-price--yearly", ".pr-benefit--mail", ".pr-shell--js",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %s — the plans page renders it, so it must be styled", want)
		}
	}
	// Without JS both prices would stack; the yearly one has to start hidden.
	if !strings.Contains(css, ".pr-price--yearly { display: none; }") {
		t.Error("the yearly price must be hidden until the toggle can control it")
	}
	if strings.Count(css, "{") != strings.Count(css, "}") {
		t.Error("unbalanced braces in signup.css")
	}
}
