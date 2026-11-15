// SPDX-License-Identifier: Apache-2.0

package trace

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestTheSpanContractIsTheShapeServed holds the published span contract to
// what the admin trace endpoints send. The contract once named fields
// (parent_id, name, attrs) that no endpoint ever sent, and a database table
// nothing wrote; both drifted because nothing read the document.
func TestTheSpanContractIsTheShapeServed(t *testing.T) {
	doc, err := os.ReadFile("../../docs/compatibility/api-contracts.md")
	if err != nil {
		t.Fatal(err)
	}
	section := string(doc)
	i := strings.Index(section, "## Trace Span Contract")
	if i < 0 {
		t.Fatal("api-contracts.md has no Trace Span Contract section")
	}
	section = section[i:]
	open := strings.Index(section, "```json\n")
	if open < 0 {
		t.Fatal("the Trace Span Contract section has no JSON example")
	}
	body := section[open+len("```json\n"):]
	body = body[:strings.Index(body, "```")]
	var documented map[string]any
	if err := json.Unmarshal([]byte(body), &documented); err != nil {
		t.Fatalf("the contract's example is not JSON: %v", err)
	}

	// Every field set, so omitempty hides none of them: the record is
	// marshalled exactly as handleTraceSpans marshals it.
	rec := NewRecorder(1)
	sp := &Span{
		TraceID: "t", SpanID: "s", ParentSpanID: "p", Operation: "op",
		StartTime: time.Unix(0, 0), Status: StatusError, ErrorMsg: "e",
		Attributes: map[string]string{"k": "v"}, recorder: rec,
	}
	sp.End()
	raw, err := json.Marshal(rec.Recent(1)[0])
	if err != nil {
		t.Fatal(err)
	}
	var served map[string]any
	if err := json.Unmarshal(raw, &served); err != nil {
		t.Fatal(err)
	}

	var missingFromDoc, notServed []string
	for k := range served {
		if _, ok := documented[k]; !ok {
			missingFromDoc = append(missingFromDoc, k)
		}
	}
	for k := range documented {
		if _, ok := served[k]; !ok {
			notServed = append(notServed, k)
		}
	}
	sort.Strings(missingFromDoc)
	sort.Strings(notServed)
	if len(missingFromDoc) > 0 {
		t.Errorf("served but not in the contract: %v", missingFromDoc)
	}
	if len(notServed) > 0 {
		t.Errorf("in the contract but never served: %v", notServed)
	}
}
