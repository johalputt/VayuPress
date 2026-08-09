// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
)

// SECTION 7 AUDIT — the sweep of every MCP tool's capability against its effect.
//
// The result is worth recording as a result, because a clean audit is evidence
// and silence is not. Thirty-three registered tools: read gates on the readers,
// write gates on the writers, delete on the deleter, and all twelve mutating
// tools write an audit record. The two whose names read like controls —
// vayushield_settings and vayuveil_unit_controls — were read in the body rather
// than judged by their names, and genuinely only report, so settings:read is the
// right bar for both. site_info is ungated on purpose and already pinned by
// TestOnlySiteInfoIsUngated; it returns the platform, domain and version that
// every public page already carries in its generator meta tag.
//
// What the sweep DID find was a coverage gap rather than a hole. The read-only
// refusal list in mcp_call_gating_test.go named ten tools; the registry has
// twelve that mutate. create_page and embed_url were absent — one writes
// content, the other resolves a URL and stores the result — so nothing checked
// that a read-only connector is refused them. They are gated correctly; simply
// nothing said so.
//
// A hand-kept list is still the right shape for that test: it names tools by
// what they DO, and deriving it from the gates would ask the gates to grade
// themselves. What it needed was something to notice when it falls behind.

// mutatingMCPTools is the set a read-only grant must never reach.
var mutatingMCPTools = []string{
	"create_post", "update_post", "delete_post", "create_page",
	"update_site_settings", "apply_theme", "upload_media", "embed_url",
	"update_site", "build_site", "restore_previous_site", "provision_certificates",
}

// THE LIST MUST NOT FALL BEHIND THE REGISTRY.
//
// It stops covering a tool the day somebody adds one, and nothing fails. This
// finds that day by asking the server rather than a person: a key granted READ on
// every section can see every reading tool, so a tool it CANNOT see needs more
// than read and belongs on the list.
//
// Not circular. The refusal test asks whether the gates hold. This asks whether
// the list covers everything the gates already treat as more than a read — a
// different question, answered by a different oracle.
func TestTheMutatingToolListCoversEveryToolNeedingMoreThanRead(t *testing.T) {
	perms := apikeys.NewPermissions()
	for _, sec := range apikeys.AllSections {
		perms.Grant(sec, apikeys.ActionRead)
	}

	readable := namesVisibleTo(t, apikeys.KeyInfo{Scope: apikeys.ScopeExternal, Perms: perms})
	ungated := namesVisibleTo(t, apikeys.KeyInfo{Scope: apikeys.ScopeExternal, Perms: apikeys.NewPermissions()})
	all := namesVisibleTo(t, apikeys.KeyInfo{Scope: apikeys.ScopeExternal, Perms: apikeys.Superuser()})

	if len(all) < 30 {
		t.Fatalf("the census sees only %d tools; something has stopped it enumerating the "+
			"registry, and every check built on it is now vacuous", len(all))
	}

	listed := map[string]bool{}
	for _, n := range mutatingMCPTools {
		listed[n] = true
	}

	for name := range all {
		if readable[name] || ungated[name] || listed[name] {
			continue
		}
		t.Errorf("tool %q needs more than a read capability but is absent from "+
			"mutatingMCPTools, so nothing checks that a read-only connector is refused it.\n\n"+
			"Add it to that list.", name)
	}
	for _, name := range mutatingMCPTools {
		if !all[name] {
			t.Errorf("mutatingMCPTools names %q, which is not registered — a stale entry "+
				"makes the refusal test pass for a tool that no longer exists", name)
		}
		if readable[name] {
			t.Errorf("mutatingMCPTools names %q, but a key holding only READ capabilities can "+
				"see it. Either its gate is too weak, or it does not mutate.", name)
		}
	}
}

func namesVisibleTo(t *testing.T, ki apikeys.KeyInfo) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, n := range mcpToolNames(t, ki) {
		out[n] = true
	}
	return out
}
