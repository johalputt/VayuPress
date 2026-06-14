package severity

import (
	"encoding/json"
	"testing"
)

func TestLevelsAreTotallyOrdered(t *testing.T) {
	order := []Level{Observe, Notice, Warn, Violation, Escalation, Containment, Critical}
	for i := 1; i < len(order); i++ {
		if !(order[i] > order[i-1]) {
			t.Fatalf("levels must be strictly increasing: %s !> %s", order[i], order[i-1])
		}
		if !order[i].AtLeast(order[i-1]) {
			t.Errorf("%s should be AtLeast %s", order[i], order[i-1])
		}
	}
	if Observe.AtLeast(Warn) {
		t.Error("Observe must not be AtLeast Warn")
	}
}

func TestRegistryComplete(t *testing.T) {
	all := All()
	if len(all) != 7 {
		t.Fatalf("expected 7 taxonomy levels, got %d", len(all))
	}
	for i, m := range all {
		if m.Rank != i {
			t.Errorf("rank mismatch at %d: %s has rank %d", i, m.Name, m.Rank)
		}
		if m.Name == "" || m.Meaning == "" || m.OperatorExpect == "" ||
			m.Escalation == "" || m.TimelineClass == "" || m.TopologyColor == "" || m.PolicyInteraction == "" {
			t.Errorf("level %s has an empty semantic field: %+v", m.Name, m)
		}
	}
}

func TestParseRoundTrip(t *testing.T) {
	for _, m := range All() {
		got, ok := Parse(m.Name)
		if !ok || got != m.Level {
			t.Errorf("Parse(%q) = %v,%v; want %v", m.Name, got, ok, m.Level)
		}
	}
	if _, ok := Parse("violation"); !ok {
		t.Error("Parse must be case-insensitive")
	}
	if _, ok := Parse("nonsense"); ok {
		t.Error("Parse must reject unknown names")
	}
}

func TestJSONMarshalsAsName(t *testing.T) {
	b, err := json.Marshal(Violation)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"VIOLATION"` {
		t.Errorf("expected \"VIOLATION\", got %s", b)
	}
	var l Level
	if err := json.Unmarshal([]byte(`"critical"`), &l); err != nil || l != Critical {
		t.Errorf("unmarshal critical: %v %v", l, err)
	}
	if json.Unmarshal([]byte(`"bogus"`), &l) == nil {
		t.Error("unmarshal must reject unknown level")
	}
}
