package graphqlapi

import (
	"fmt"
	"strings"
	"testing"
)

// TestCheckQueryCost guards audit M12: a normal query passes, an alias-flooded
// query (the amplification attack) is rejected before execution, and a deeply
// nested query is rejected — while a syntactically invalid query is left for the
// executor to report.
func TestCheckQueryCost(t *testing.T) {
	if err := checkQueryCost(`{ articles { title wordCount } }`); err != nil {
		t.Fatalf("a normal query must pass, got: %v", err)
	}

	// Alias-flood: 300 aliased root fields (~600 selections) exceeds the cap.
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, " a%d: articles { title }", i)
	}
	b.WriteString(" }")
	if err := checkQueryCost(b.String()); err == nil {
		t.Fatal("an alias-flooded query must be rejected (M12)")
	}

	// Excessive nesting depth is rejected.
	deep := strings.Repeat("a{", maxQueryDepth+3) + "x" + strings.Repeat("}", maxQueryDepth+3)
	if err := checkQueryCost("{" + deep + "}"); err == nil {
		t.Fatal("an over-deep query must be rejected (M12)")
	}

	// A syntactically invalid query is left for graphql.Do to report cleanly.
	if err := checkQueryCost("{ this is not valid gql"); err != nil {
		t.Fatalf("a parse error must be ignored by the cost check, got: %v", err)
	}
}
