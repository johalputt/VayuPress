// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
)

// THE NUMBER THE WHOLE INVESTIGATION TURNED ON COULD NOT BE READ.
//
// An operator refused a country and kept seeing it in their audience report.
// Every diagnosis of that ran off a screenshot, because the connector exposed
// totals and top pages and nothing about WHERE traffic came from. This project's
// own standing rule is to read a number from an API and never off a dashboard
// image — and that rule could not be followed, because the API had no such
// field. A week of reasoning rested on one unverifiable figure.
func TestTheAudienceBreakdownIsReachableOverTheConnector(t *testing.T) {
	perms := apikeys.NewPermissions()
	perms.Grant(apikeys.SectionAnalytics, apikeys.ActionRead)
	visible := namesVisibleTo(t, apikeys.KeyInfo{Scope: apikeys.ScopeExternal, Perms: perms})

	if !visible["analytics_audience"] {
		t.Fatal("analytics_audience is not reachable with an analytics READ grant.\n\n" +
			"Without it, 'which countries is my traffic from' can only be answered by looking " +
			"at a dashboard — which is exactly the source this project forbids for numbers.")
	}
	// It reads. It must never have been gated as anything else.
	if !visible["analytics_summary"] {
		t.Error("the read grant lost analytics_summary")
	}
}

// A read tool must not be reachable without the grant it claims to need.
func TestTheAudienceBreakdownNeedsAnalyticsRead(t *testing.T) {
	none := namesVisibleTo(t, apikeys.KeyInfo{Scope: apikeys.ScopeExternal, Perms: apikeys.NewPermissions()})
	if none["analytics_audience"] {
		t.Error("a key with no capabilities can read the audience breakdown")
	}
}

// It is a READER. If it ever gains a write path it must appear in the mutating
// census, and this pins which side of that line it is on today.
func TestTheAudienceBreakdownIsNotAMutatingTool(t *testing.T) {
	for _, n := range mutatingMCPTools {
		if n == "analytics_audience" {
			t.Fatal("analytics_audience is listed as mutating; it only reads aggregates")
		}
	}
	// And its description must say what it is FOR, because the reason it exists
	// is that nobody knew to look for it.
	src := readSourceFile(t, "mcp_server.go")
	i := strings.Index(src, `Name: "analytics_audience"`)
	if i < 0 {
		t.Fatal("analytics_audience is not registered")
	}
	if !strings.Contains(src[i:i+1200], "country rule") {
		t.Error("the description never mentions checking a country rule, which is the question " +
			"it was added to answer")
	}
}
