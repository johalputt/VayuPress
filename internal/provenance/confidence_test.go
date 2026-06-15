package provenance

import "testing"

func TestConfidenceOrdering(t *testing.T) {
	if !Canonical.AtLeast(Derived) || !Derived.AtLeast(Inferred) {
		t.Error("expected canonical > derived > inferred")
	}
	if Inferred.AtLeast(Derived) {
		t.Error("inferred must not be AtLeast derived")
	}
	// The unset zero value fails safe: it is not a valid level, ranks below
	// inferred, and is never treated as trustworthy.
	var zero Confidence
	if zero.Known() {
		t.Error("the zero Confidence must not be Known")
	}
	if zero.AtLeast(Inferred) {
		t.Error("the zero Confidence must rank below inferred")
	}
}

func TestParseAndKnown(t *testing.T) {
	for _, c := range All() {
		got, ok := Parse(c.String())
		if !ok || got != c {
			t.Errorf("Parse(%q) = %v,%v", c, got, ok)
		}
		if !c.Known() {
			t.Errorf("%q should be Known", c)
		}
	}
	if got, ok := Parse("CANONICAL"); !ok || got != Canonical {
		t.Error("Parse must be case-insensitive")
	}
	// Unknown provenance is untrusted provenance: parses to Inferred, not ok.
	if got, ok := Parse("nonsense"); ok || got != Inferred {
		t.Errorf("unknown name should be (Inferred,false), got (%v,%v)", got, ok)
	}
	if Confidence("bogus").Known() {
		t.Error("a bogus value must not be Known")
	}
}

// TestCombinePropagationRules pins the propagation contract the rest of the
// platform relies on: trust cannot be manufactured by derivation, and any
// weaker (or unknown) input dominates the conclusion.
func TestCombinePropagationRules(t *testing.T) {
	cases := []struct {
		in   []Confidence
		want Confidence
	}{
		{nil, Inferred},                               // no basis at all
		{[]Confidence{Canonical}, Derived},            // a conclusion from one observation is derived
		{[]Confidence{Canonical, Canonical}, Derived}, // canonical + canonical → derived
		{[]Confidence{Canonical, Inferred}, Inferred}, // inferred input poisons the conclusion
		{[]Confidence{Derived, Canonical}, Derived},   // weakest non-inferred dominates
		{[]Confidence{Derived, Inferred}, Inferred},
		{[]Confidence{Confidence("garbage"), Canonical}, Inferred}, // unknown poisons
	}
	for _, c := range cases {
		if got := Combine(c.in...); got != c.want {
			t.Errorf("Combine(%v) = %q; want %q", c.in, got, c.want)
		}
	}
}

// TestCombineNeverManufacturesTrust is an ontology-drift guard: for every pair of
// inputs, the conclusion's trust must not exceed the weakest input, and can never
// be canonical (only direct observation is canonical).
func TestCombineNeverManufacturesTrust(t *testing.T) {
	levels := All()
	for _, a := range levels {
		for _, b := range levels {
			got := Combine(a, b)
			if got == Canonical {
				t.Errorf("Combine(%q,%q) = canonical — derivation must never manufacture direct observation", a, b)
			}
			weakest := a
			if b.rank() < a.rank() {
				weakest = b
			}
			if got.AtLeast(weakest) && got != weakest {
				t.Errorf("Combine(%q,%q) = %q exceeds weakest input %q", a, b, got, weakest)
			}
		}
	}
}
