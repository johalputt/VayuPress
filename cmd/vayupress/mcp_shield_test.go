// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/mcp"
)

// TestTheShieldMCPSurfaceIsReadOnly.
//
// "Read-only" written in a doc comment is a promise. This makes it a property.
//
// The reason it matters more here than on any other MCP surface: a tool is
// invoked by a model, and a model's context carries text from wherever it has
// been reading — a blog comment, a fetched page, a mail body. A writable
// operator ALLOW list would therefore let a stranger who can get text in front
// of a model add themselves to the one list that skips every gate including the
// jail. Observe-only is the same shape from the other end: one write turns every
// defence into a counter.
//
// So if someone later adds a shield write tool, they have to delete this test
// and say why in the diff.
func TestTheShieldMCPSurfaceIsReadOnly(t *testing.T) {
	srv := mcp.NewServer("test", "0")
	(&App{}).registerShieldTools(srv)

	names := map[string]bool{}
	for _, tool := range srv.Tools() {
		names[tool.Name] = true

		// Matched on whole underscore-separated SEGMENTS. A substring match reads
		// "set" inside "settings" and fails the read tool it is meant to protect —
		// a check that cries wolf on its own subject gets deleted rather than
		// heeded.
		mutating := map[string]bool{
			"set": true, "write": true, "update": true, "enable": true, "disable": true,
			"apply": true, "delete": true, "add": true, "remove": true, "block": true,
			"unblock": true, "release": true, "reset": true, "clear": true, "jail": true,
		}
		for _, seg := range strings.Split(strings.ToLower(tool.Name), "_") {
			if mutating[seg] {
				t.Errorf("tool %q has the mutating segment %q. Nothing on this surface may change "+
					"shield state: a writable allow list is a total bypass reachable by anyone who "+
					"can get text in front of a model", tool.Name, seg)
			}
		}
		if !strings.Contains(tool.Description, "Read-only") {
			t.Errorf("tool %q does not declare itself read-only in its description, which is what "+
				"the calling model actually reads", tool.Name)
		}
	}

	for _, want := range []string{
		"vayushield_status", "vayushield_posture", "vayushield_settings", "vayushield_history",
		"analytics_referrers",
	} {
		if !names[want] {
			t.Errorf("the %q tool is missing", want)
		}
	}
}

// TestTheAllowListIsNeverDisclosedOverMCP.
//
// Knowing WHICH networks bypass the shield is the one piece of shield state with
// direct offensive value — it names the addresses worth being. Least disclosure
// costs nothing here, because an operator reading their own panel sees the values
// anyway; only the remote reader is narrowed.
//
// Counts are fine and useful: "3 allow entries" answers "is this configured?"
// without answering "what should I pretend to be?".
func TestTheAllowListIsNeverDisclosedOverMCP(t *testing.T) {
	srv := mcp.NewServer("test", "0")
	(&App{}).registerShieldTools(srv)

	for _, tool := range srv.Tools() {
		if tool.Name != "vayushield_settings" {
			continue
		}
		d := strings.ToLower(tool.Description)
		if !strings.Contains(d, "count") {
			t.Error("the settings tool does not tell a caller that the network lists are counts " +
				"rather than values, so a model may report an absence of entries as an absence of " +
				"protection")
		}
		return
	}
	t.Fatal("vayushield_settings is not registered")
}
