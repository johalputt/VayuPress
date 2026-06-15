package governance_test

// This is the executable half of docs/governance/operational-ontology.md. It is a
// cross-package contract test: it pins the couplings *between* the ontology's axes
// (severity, confidence, budgets) so the build fails if the code drifts from the
// written doctrine. Per-package internals are tested in their own packages; this
// guards the seams where they meet.

import (
	"os"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/budget"
	"github.com/johalputt/vayupress/internal/provenance"
	"github.com/johalputt/vayupress/internal/severity"
)

// TestSeverityTaxonomyIsStable pins the size and total ordering the doc and the
// budget engine both depend on. The taxonomy is append-only at the severe end.
func TestSeverityTaxonomyIsStable(t *testing.T) {
	all := severity.All()
	if len(all) != 7 {
		t.Fatalf("ontology pins a 7-level severity taxonomy; got %d — update the doc deliberately", len(all))
	}
	for i := 1; i < len(all); i++ {
		if !all[i].Level.AtLeast(all[i-1].Level) || all[i].Level == all[i-1].Level {
			t.Errorf("severity must be strictly increasing: %s !> %s", all[i].Name, all[i-1].Name)
		}
	}
}

// TestBudgetsBindToSeverity enforces the budget↔severity coupling: every default
// budget tracks and escalates to a real, parseable severity level.
func TestBudgetsBindToSeverity(t *testing.T) {
	for _, r := range budget.DefaultRules() {
		if _, ok := severity.Parse(r.Tracks.String()); !ok {
			t.Errorf("budget %q tracks a non-taxonomy severity %v", r.Name, r.Tracks)
		}
		if _, ok := severity.Parse(r.OnExhaust.String()); !ok {
			t.Errorf("budget %q escalates to a non-taxonomy severity %v", r.Name, r.OnExhaust)
		}
	}
}

// TestConfidencePropagationContract enforces the rule the doc states verbatim:
// trust cannot be manufactured by derivation.
func TestConfidencePropagationContract(t *testing.T) {
	if got := provenance.Combine(provenance.Canonical, provenance.Canonical); got != provenance.Derived {
		t.Errorf("canonical+canonical must derive (not stay canonical), got %s", got)
	}
	if got := provenance.Combine(provenance.Canonical, provenance.Inferred); got != provenance.Inferred {
		t.Errorf("any inferred input must poison the conclusion, got %s", got)
	}
	// No axis may invent a canonical conclusion from a derivation.
	for _, a := range provenance.All() {
		for _, b := range provenance.All() {
			if provenance.Combine(a, b) == provenance.Canonical {
				t.Errorf("Combine(%s,%s) manufactured canonical trust", a, b)
			}
		}
	}
}

// TestOntologyDocNamesItsAxes is a cheap doc-presence guard: the doctrine file must
// exist and reference each axis package, so the doc and the code stay co-located.
func TestOntologyDocNamesItsAxes(t *testing.T) {
	b, err := os.ReadFile("../../docs/governance/operational-ontology.md")
	if err != nil {
		t.Fatalf("operational ontology doc must exist: %v", err)
	}
	doc := string(b)
	for _, axis := range []string{"internal/severity", "internal/provenance", "internal/budget", "event-retention.md"} {
		if !strings.Contains(doc, axis) {
			t.Errorf("ontology doc must reference %q", axis)
		}
	}
}
