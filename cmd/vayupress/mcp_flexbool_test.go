// SPDX-License-Identifier: Apache-2.0

package main

// mcp_flexbool_test.go — boolean tool arguments that survive a coercing client.
//
// From a live call. The schema declares {"type": "boolean"}, the assistant sent
// true, and the string "true" arrived:
//
//	invalid arguments: json: cannot unmarshal string into Go struct field
//	.allow_eval of type bool
//
// The schema was right and the client was wrong, which is precisely why the
// server cannot depend on it: an MCP server talks to whatever client is pointed
// at it, and a strict decode turns somebody else's type coercion into a setting
// the operator simply cannot use, reported as an error about Go structs.

import (
	"encoding/json"
	"testing"
)

func TestABooleanArgumentSurvivesAClientThatSendsItAsAString(t *testing.T) {
	cases := map[string]bool{
		`true`:    true,
		`false`:   false,
		`"true"`:  true,
		`"false"`: false,
		`"True"`:  true,
		`"1"`:     true,
		`"0"`:     false,
		`" true"`: true,
	}
	for raw, want := range cases {
		var v flexBool
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Errorf("%s failed to decode: %v — the operator cannot set this at all", raw, err)
			continue
		}
		if bool(v) != want {
			t.Errorf("%s decoded to %v, want %v", raw, bool(v), want)
		}
	}
}

// It must NOT be generous about what counts as true. Reading "no" or "off" as
// true would silently enable something the operator was trying to disable, which
// is a far worse failure than refusing the value.
func TestAnythingThatIsNotABooleanIsRefused(t *testing.T) {
	for _, raw := range []string{`"yes"`, `"no"`, `"on"`, `"off"`, `"maybe"`, `""`, `2`, `null`, `[]`, `{}`} {
		var v flexBool
		if err := json.Unmarshal([]byte(raw), &v); err == nil {
			t.Errorf("%s was accepted as %v — a value nobody clearly meant should be refused, "+
				"not guessed at", raw, bool(v))
		}
	}
}

// Omitted must stay distinguishable from false, or a call that changes one field
// silently rewrites every boolean on the site.
func TestOmittedStaysDistinctFromFalse(t *testing.T) {
	var in struct {
		A *flexBool `json:"a"`
		B *flexBool `json:"b"`
	}
	if err := json.Unmarshal([]byte(`{"a":"false"}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.A == nil {
		t.Fatal("an explicitly-sent false decoded as omitted, so it can never be turned off")
	}
	if in.A.Bool() {
		t.Error(`"false" decoded as true`)
	}
	if in.B != nil {
		t.Error("an omitted field decoded as present, so an unrelated edit would rewrite it")
	}
	// And the nil receiver must not panic — the handler calls Bool() on a pointer
	// it only checked for nil in one of the two branches.
	if in.B.Bool() {
		t.Error("a nil flexBool reported true")
	}
}
