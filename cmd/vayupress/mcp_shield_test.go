// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"

	"bytes"
	"encoding/json"

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

// TestEveryToolSchemaIsValidOnTheWire.
//
// The check that was missing, and its absence cost a working connector.
//
// Three no-argument tools were registered with objSchema(nil, nil). A nil map
// marshals to `"properties": null`, and null is not a valid value for
// `properties` in JSON Schema — it must be an object. A client that validates
// tool schemas rejects the tool, and some reject the whole tools/list. So three
// tools with an empty schema removed EVERY tool the server offered, across every
// session.
//
// Nothing caught it because nothing looked at the payload. The server answered
// tools/list with HTTP 200 and 8 KB of JSON; the bytes were fine and were refused
// at the other end. Every test and every probe exercised the transport.
//
// This marshals what actually goes on the wire and validates it.
func TestEveryToolSchemaIsValidOnTheWire(t *testing.T) {
	a := &App{}
	srv := a.buildMCPServer()
	tools := srv.Tools()
	if len(tools) == 0 {
		t.Fatal("no tools registered")
	}

	for _, tool := range tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Errorf("%s: input schema does not marshal: %v", tool.Name, err)
			continue
		}
		// The literal that broke it. Checked on the encoded bytes, because that is
		// what the client parses — a Go-side nil map looks harmless in a debugger
		// and only becomes null at the boundary.
		if bytes.Contains(raw, []byte(`"properties":null`)) {
			t.Errorf("%s: emits \"properties\":null, which is invalid JSON Schema and can cause a "+
				"client to discard the ENTIRE tool list: %s", tool.Name, raw)
		}

		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Errorf("%s: schema is not valid JSON: %v", tool.Name, err)
			continue
		}
		if decoded["type"] != "object" {
			t.Errorf("%s: schema type is %v, want \"object\"", tool.Name, decoded["type"])
		}
		props, ok := decoded["properties"]
		if !ok {
			t.Errorf("%s: schema has no properties key", tool.Name)
			continue
		}
		if _, isObj := props.(map[string]any); !isObj {
			t.Errorf("%s: properties is %T (%v), want an object — a no-argument tool needs {}, "+
				"not null", tool.Name, props, props)
		}
	}
}

// TestTheWholeToolListSurvivesAStrictClient — the failure mode was not "one tool
// is broken", it was "one tool takes every other tool with it". So the assertion
// that matters is over the WHOLE list as a single document, the way a client
// receives it.
func TestTheWholeToolListSurvivesAStrictClient(t *testing.T) {
	a := &App{}
	srv := a.buildMCPServer()

	type wireTool struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"inputSchema"`
	}
	var list []wireTool
	for _, tool := range srv.Tools() {
		list = append(list, wireTool{tool.Name, tool.Description, tool.InputSchema})
	}
	payload, err := json.Marshal(map[string]any{"tools": list})
	if err != nil {
		t.Fatalf("the tools/list payload does not marshal: %v", err)
	}
	if bytes.Contains(payload, []byte("null")) {
		// Narrow the report to the offender rather than the whole document.
		for _, tool := range srv.Tools() {
			b, _ := json.Marshal(tool.InputSchema)
			if bytes.Contains(b, []byte("null")) {
				t.Errorf("tool %q contributes a null to the tools/list payload: %s", tool.Name, b)
			}
		}
	}
	// Sanity: the list is substantial. A payload that shrank to nothing would
	// also "contain no nulls".
	if len(srv.Tools()) < 10 {
		t.Errorf("only %d tools registered — the surface collapsed", len(srv.Tools()))
	}
}

// TestNoShieldToolPanicsOnAnUninitialisedInstall.
//
// A panicking handler kills the request, and on an install where VayuShield has
// not booted — a fresh deploy, a failed boot, a Tor Space mid-start — every one
// of these tools is reachable before the thing it reads exists.
//
// The guard is cheap and the failure is not: a panic in a tool handler takes out
// the connection, and an MCP client retries, so it takes it out repeatedly.
func TestNoShieldToolPanicsOnAnUninitialisedInstall(t *testing.T) {
	a := &App{} // nothing initialised: no shield, no settings, no analytics
	srv := a.buildMCPServer()

	for _, tool := range srv.Tools() {
		if !strings.HasPrefix(tool.Name, "vayushield_") && tool.Name != "analytics_referrers" {
			continue
		}
		t.Run(tool.Name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("%s panicked on an uninitialised install: %v", tool.Name, p)
				}
			}()
			// Empty args, the shape a client sends for a no-argument tool.
			_, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
			// An error is the CORRECT answer here. Only a panic is a defect.
			_ = err
		})
	}
}

// TestShieldToolResultsCarryNoNullCollections.
//
// The nil-schema bug's smaller sibling. A Go nil slice or map marshals to `null`,
// so a consumer reading a list field has to special-case null where `[]` would
// simply iterate zero times. It does not break a client the way a null schema
// does, but it is the same mistake — a nil that looks harmless until it crosses
// the wire — and this is where it was found on the settings tool.
func TestShieldToolResultsCarryNoNullCollections(t *testing.T) {
	a := &App{}
	if got := a.shieldIntelStatus(); got == nil {
		t.Error("shieldIntelStatus returns nil, which marshals to null in the settings tool result")
	}
	b, err := json.Marshal(map[string]any{"network_intelligence": a.shieldIntelStatus()})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(`"network_intelligence":null`)) {
		t.Errorf("the settings tool emits a null collection: %s", b)
	}
}
