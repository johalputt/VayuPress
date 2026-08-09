// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// FINDING (S8) — every hosted domain appeared in the operator's referrer list as
// an external site referring traffic to itself.
//
// The exclusion behind "internal navigation is not a referral" was built from
// config.Cfg.Domain — ONE value, on an install that serves many domains. So the
// primary and its subdomains were excluded and nothing else was: a visitor
// moving between two pages of client.example recorded a referral FROM
// client.example, and it stayed in the list.
//
// That is not a new bug so much as the same one, unfinished. The comment on
// TopReferrers describes the primary's version of it — "the referrer list was
// topped by the operator's own webmail and MCP hosts, each with a count larger
// than the site's entire pageview total" — and the fix stopped at the one host
// the code happened to know about.

// withPrimary pins config.Cfg.Domain for a test and restores it after.
func withPrimary(t *testing.T, host string) {
	t.Helper()
	prev := config.Cfg.Domain
	t.Cleanup(func() { config.Cfg.Domain = prev })
	config.Cfg.Domain = host
}

// referrerHostsInTop returns the hosts TopReferrers reports.
func referrerHostsInTop(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	refs, err := s.TopReferrers(context.Background(), 30, 50)
	if err != nil {
		t.Fatalf("TopReferrers: %v", err)
	}
	out := map[string]bool{}
	for _, r := range refs {
		out[r.Referrer] = true
	}
	return out
}

func TestAHostedDomainIsNotReportedAsReferringTrafficToItself(t *testing.T) {
	s := newExtStore(t)
	withPrimary(t, "operator.example")

	// Three arrivals: one genuinely external, one from the primary's own
	// subdomain (already excluded before this change), and one from a hosted
	// client's own hostname (the finding).
	for _, ref := range []string{"news.example.com", "mail.operator.example", "client.example"} {
		if err := s.Collect(context.Background(), CollectRequest{
			URL: "/x", Referrer: "https://" + ref + "/page", Hostname: "client.example", EventType: 1,
		}, "203.0.113.7", "Mozilla/5.0", "domA"); err != nil {
			t.Fatal(err)
		}
	}

	// Without the hook, the client's own host is reported — the shipped defect.
	// Asserting it here is what stops this test passing for the wrong reason: if
	// the fixture never produced the bad row, the assertion below would hold
	// against a fix that does nothing.
	if !referrerHostsInTop(t, s)["client.example"] {
		t.Fatal("the fixture did not reproduce the defect, so the assertion below proves nothing")
	}

	s.UseSelfHosts(func() []string { return []string{"client.example", "operator.example"} })

	got := referrerHostsInTop(t, s)
	if got["client.example"] {
		t.Error("a hosted domain is still listed as a referrer to itself — an operator reading " +
			"this list sees a client's own hostname as their top traffic source")
	}
	if got["mail.operator.example"] {
		t.Error("the primary's subdomain came back; widening the exclusion must not narrow it")
	}
	if !got["news.example.com"] {
		t.Fatal("a genuinely external referrer was excluded. An exclusion that eats real " +
			"referrals is worse than the over-counting it replaced — the list would read as " +
			"'no one links to us'")
	}
}

// Subdomains and port forms have to widen with the host list, or a hosted
// domain's own CDN port and its www. host stay in the list while its apex leaves.
func TestTheExclusionCoversEveryFormOfEveryHost(t *testing.T) {
	s := newExtStore(t)
	withPrimary(t, "operator.example")
	s.UseSelfHosts(func() []string { return []string{"client.example"} })

	frag, args := s.selfHostExclusion("referrer")
	if n := strings.Count(frag, "?"); n != len(args) {
		t.Fatalf("the fragment binds %d placeholders and supplies %d arguments", n, len(args))
	}
	// Two hosts × four forms. A count assertion alone would pass with the same
	// host repeated, so the values are checked too.
	want := map[string]bool{
		"operator.example": false, "%.operator.example": false,
		"operator.example:%": false, "%.operator.example:%": false,
		"client.example": false, "%.client.example": false,
		"client.example:%": false, "%.client.example:%": false,
	}
	for _, a := range args {
		s, _ := a.(string)
		if _, ok := want[s]; !ok {
			t.Errorf("unexpected exclusion argument %q", s)
			continue
		}
		want[s] = true
	}
	for pat, seen := range want {
		if !seen {
			t.Errorf("%q is not excluded, so that spelling of one of this install's own hosts "+
				"still counts as an external referral", pat)
		}
	}
}

// A nil hook must be exactly the single-domain behaviour. Most installs serve
// one domain and never call UseSelfHosts; a change that made them behave
// differently would be a regression dressed as a fix.
func TestWithNoHostListTheBehaviourIsThePrimaryOnly(t *testing.T) {
	s := newExtStore(t)
	withPrimary(t, "operator.example")

	frag, args := s.selfHostExclusion("referrer")
	if len(args) != 4 {
		t.Fatalf("a nil hook produced %d exclusion arguments, want 4 (the primary's four forms)", len(args))
	}
	if !strings.Contains(frag, "LOWER(referrer)<>?") {
		t.Errorf("the fragment does not exclude the exact host: %q", frag)
	}
}

// The registry is not always readable, and a half-written row is not a reason to
// report that nobody links to this site.
//
// An empty host would yield the pattern "%." — which matches every referrer
// containing a dot, i.e. all of them. That is a silent, total emptying of the
// list, and it is the failure mode a fix like this one invites.
func TestAnEmptyOrDuplicateHostCannotEmptyTheWholeList(t *testing.T) {
	s := newExtStore(t)
	withPrimary(t, "operator.example")
	s.UseSelfHosts(func() []string {
		return []string{"", "   ", "client.example", "CLIENT.EXAMPLE", "operator.example"}
	})

	_, args := s.selfHostExclusion("referrer")
	for _, a := range args {
		if v, _ := a.(string); v == "%." || v == "" || v == "%.:%" {
			t.Fatalf("an empty host produced the pattern %q, which matches every referrer with "+
				"a dot in it — the whole list would vanish and read as 'no referrals'", v)
		}
	}
	// Two distinct hosts after case-folding and blank-dropping, four forms each.
	if len(args) != 8 {
		t.Errorf("got %d exclusion arguments, want 8 — duplicates and blanks are not being "+
			"collapsed, which grows the WHERE clause with every registry read", len(args))
	}

	if err := s.Collect(context.Background(), CollectRequest{
		URL: "/x", Referrer: "https://news.example.com/page", Hostname: "client.example", EventType: 1,
	}, "203.0.113.7", "Mozilla/5.0", "domA"); err != nil {
		t.Fatal(err)
	}
	if !referrerHostsInTop(t, s)["news.example.com"] {
		t.Fatal("a real referrer was excluded by a blank registry entry")
	}
}
