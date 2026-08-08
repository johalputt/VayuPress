// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
)

// SECTION 3 AUDIT — the MCP capability gate, attacked at the point that matters.
//
// The consent screen makes a promise in plain words: Read-only "Cannot change
// anything." That promise is kept by one predicate, Visible, and the question an
// attacker asks is not "what am I shown" but "what happens if I ask anyway".
//
// A connector holding a read-only grant knows every tool name — they are in the
// product's own documentation. Nothing stops it sending
// tools/call{"name":"delete_post"} directly. If Visible only filtered the
// listing, the promise would be decoration.
//
// It does not: internal/mcp checks Visible on the call path as well, and reports
// a gated tool as "unknown or unavailable" rather than "forbidden", so a key
// cannot even map the capabilities it lacks. Both halves are attacked here
// against the REAL VayuPress tool registry rather than a synthetic one — the
// package-level test covers the mechanism, this covers the composition, and a
// gate can be correct in one and missing in the other.
//
// This is a clean result recorded as a test rather than as a note.

// mcpCall sends one tools/call as the given key and returns the raw response.
func mcpCall(t *testing.T, ki apikeys.KeyInfo, tool string) string {
	t.Helper()
	srv := (&App{}).buildMCPServer()
	body := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"` + tool + `","arguments":{}}}`
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, keyCtxRequest(body, ki))
	return rec.Body.String()
}

// mcpToolNames lists the tools the given key can see.
func mcpToolNames(t *testing.T, ki apikeys.KeyInfo) []string {
	t.Helper()
	srv := (&App{}).buildMCPServer()
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, keyCtxRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, ki))
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("tools/list: %v (%s)", err, rec.Body.String())
	}
	out := make([]string, 0, len(resp.Result.Tools))
	for _, tl := range resp.Result.Tools {
		out = append(out, tl.Name)
	}
	return out
}

func TestAReadOnlyGrantCannotCallAWritingTool(t *testing.T) {
	// Exactly the grant behind the consent screen's "Read-only" preset.
	readonly := scopedKey([2]string{"posts", "read"}, [2]string{"analytics", "read"})

	for _, tool := range []string{
		"create_post", "update_post", "delete_post",
		"update_site_settings", "apply_theme", "upload_media",
		"update_site", "build_site", "restore_previous_site", "provision_certificates",
	} {
		got := mcpCall(t, readonly, tool)
		if !strings.Contains(got, "unknown or unavailable") {
			t.Errorf("a read-only connector called %q and the server did not refuse it.\n\n"+
				"The consent screen told the operator this grant \"cannot change anything\". "+
				"Tool names are public; hiding one from the listing is not a control.\n\n%s",
				tool, got)
		}
	}
	// And the grant is not over-refused in the other direction: the tools it WAS
	// approved for stay reachable. Asserted through the listing rather than a
	// call, because calling a GRANTED tool runs the real handler and needs the
	// stores an empty App does not have — the refusals above return before the
	// handler, which is why they can be exercised directly.
	visible := map[string]bool{}
	for _, n := range mcpToolNames(t, readonly) {
		visible[n] = true
	}
	for _, want := range []string{"list_posts", "get_post", "analytics_summary"} {
		if !visible[want] {
			t.Errorf("the read-only grant cannot reach %q, which is exactly what it was "+
				"approved for. A grant that refuses its own tools is a broken feature.", want)
		}
	}
}

// A refused tool must be indistinguishable from a tool that does not exist, or
// the error message becomes a map of everything this key is missing.
func TestARefusedToolIsReportedAsUnknownNotForbidden(t *testing.T) {
	none := apikeys.KeyInfo{Scope: apikeys.ScopeExternal, Perms: apikeys.NewPermissions()}
	real := mcpCall(t, none, "delete_post")
	fake := mcpCall(t, none, "no_such_tool_at_all")

	for _, leak := range []string{"forbidden", "permission", "capability", "not allowed", "posts:"} {
		if strings.Contains(strings.ToLower(real), leak) {
			t.Errorf("refusing delete_post leaked %q to a key that cannot use it: %s", leak, real)
		}
	}
	if !strings.Contains(real, "unknown or unavailable") || !strings.Contains(fake, "unknown or unavailable") {
		t.Errorf("a gated tool and a nonexistent one answer differently.\ngated: %s\nfake:  %s", real, fake)
	}
}

// THE GATE THAT OUTLIVES THIS AUDIT.
//
// Every tool in the registry carries a Visible predicate today except site_info,
// which is deliberate — a cheap connectivity probe returning platform, version
// and domain. A tool registered tomorrow WITHOUT one is callable by any valid
// key, including a read-only connector, and nothing would say so: the existing
// scope test names create_post and get_post specifically, so a new ungated tool
// passes it.
//
// Asserting the exact set a zero-grant key sees turns that from a review
// question into a build failure. When this test fails because a tool was added,
// the fix is to give that tool a Visible predicate — not to extend this list.
func TestOnlySiteInfoIsUngated(t *testing.T) {
	none := apikeys.KeyInfo{Scope: apikeys.ScopeExternal, Perms: apikeys.NewPermissions()}
	got := mcpToolNames(t, none)

	if len(got) != 1 || got[0] != "site_info" {
		t.Errorf("a key with NO grants can see %v.\n\n"+
			"Only site_info is meant to be ungated. Any other name here is a tool "+
			"registered without a Visible predicate, which makes it callable by every "+
			"connector regardless of what the operator approved — give that tool a "+
			"predicate rather than adding it to this list.", got)
	}
}
