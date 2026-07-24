package anonaudit

import (
	"strings"
	"testing"
)

func find(checks []Check, title string) (Check, bool) {
	for _, c := range checks {
		if c.Title == title {
			return c, true
		}
	}
	return Check{}, false
}

func TestClearnetInstallIsInfoOnly(t *testing.T) {
	checks := Run(Inputs{OnionMode: false})
	if len(checks) != 1 || checks[0].Status != Info {
		t.Fatalf("clearnet install should be a single Info line, got %+v", checks)
	}
}

func TestHealthyTorSpacePasses(t *testing.T) {
	checks := Run(Inputs{
		OnionMode:             true,
		ClearnetEgressBlocked: true,
		LoopbackBind:          true,
	})
	pass, warn, fail := Summary(checks)
	if fail != 0 {
		t.Fatalf("a healthy Tor Space must have no failures, got %d (checks %+v)", fail, checks)
	}
	if pass < 3 {
		t.Fatalf("expected the core controls to pass, got pass=%d", pass)
	}
	_ = warn
	if c, ok := find(checks, "Clearnet egress disabled"); !ok || c.Status != Pass {
		t.Fatal("clearnet egress control should pass")
	}
}

func TestMisconfiguredTorSpaceFails(t *testing.T) {
	// In Tor mode but the guard is off and the bind is public: two hard failures.
	checks := Run(Inputs{OnionMode: true, ClearnetEgressBlocked: false, LoopbackBind: false})
	_, _, fail := Summary(checks)
	if fail < 2 {
		t.Fatalf("egress-off + public-bind must produce >=2 failures, got %d", fail)
	}
	if c, ok := find(checks, "Clearnet egress NOT disabled"); !ok || c.Status != Fail {
		t.Fatal("egress-off must be a Fail")
	}
}

func TestResidualWarnings(t *testing.T) {
	checks := Run(Inputs{
		OnionMode: true, ClearnetEgressBlocked: true, LoopbackBind: true,
		ExternalSMTPConfigured: true, ClearnetDomainSet: true,
	})
	if c, ok := find(checks, "External SMTP relay is configured"); !ok || c.Status != Warn {
		t.Fatal("external SMTP should warn")
	}
	if c, ok := find(checks, "A clearnet domain is still configured"); !ok || c.Status != Warn {
		t.Fatal("clearnet domain should warn")
	}
	// Never a false absolute-anonymity promise.
	if _, ok := find(checks, "No tool makes you '100% anonymous'"); !ok {
		t.Fatal("report must include the honest no-100%-anonymity note")
	}
}

func TestTripwireCountSurfaced(t *testing.T) {
	checks := Run(Inputs{OnionMode: true, ClearnetEgressBlocked: true, LoopbackBind: true, BlockedClearnetAttempts: 3})
	c, ok := find(checks, "Egress guard has actively blocked leaks")
	if !ok {
		t.Fatal("a non-zero tripwire count should surface a check")
	}
	if !strings.Contains(c.Detail, "3 clearnet") {
		t.Fatalf("tripwire detail should include the count, got %q", c.Detail)
	}
	// Zero attempts → no such line.
	checks = Run(Inputs{OnionMode: true, ClearnetEgressBlocked: true, LoopbackBind: true})
	if _, ok := find(checks, "Egress guard has actively blocked leaks"); ok {
		t.Fatal("zero attempts should not surface the tripwire line")
	}
}
