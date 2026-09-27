// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/ui"
)

// toolRowHTML renders one module the way the Tools page does.
func toolRowHTML(s toolState) string { return string(ui.Rows(toolRow(s))) }

// TestToolRowCSPSafe ensures a rendered module row carries no inline styles
// or external hosts and reads its state correctly.
func TestToolRowCSPSafe(t *testing.T) {
	on := toolRowHTML(toolState{
		ID: "comments", Name: "Comments", Desc: "Reader comments",
		Category: "Engagement", Icon: "talk", Toggleable: true, Enabled: true, Ready: true,
	})
	assertCSPSafe(t, "toolRow", on)
	if !strings.Contains(on, `data-tool-toggle="comments"`) {
		t.Error("a module that can be switched has no switch")
	}
	if !strings.Contains(on, `sa-dot--ok" aria-hidden="true"></span>On<`) {
		t.Error("an enabled, ready module should read On")
	}
	if !strings.Contains(on, " checked") {
		t.Error("an enabled module's switch should be checked")
	}
}

// TestToolRowOff verifies a switched-off module reads Off and its switch is
// not checked.
func TestToolRowOff(t *testing.T) {
	off := toolRowHTML(toolState{
		ID: "newsletter", Name: "Newsletter", Toggleable: true, Enabled: false, Ready: true,
	})
	if !strings.Contains(off, `sa-dot--neutral" aria-hidden="true"></span>Off<`) {
		t.Error("a switched-off module should read Off")
	}
	if strings.Contains(off, " checked") {
		t.Error("a switched-off module's switch must not be checked")
	}
}

// TestToolRowBuiltIn verifies a module that cannot be switched says so and
// offers no switch.
func TestToolRowBuiltIn(t *testing.T) {
	bi := toolRowHTML(toolState{
		ID: "diagrams", Name: "Diagrams", Toggleable: false, Ready: true,
	})
	if !strings.Contains(bi, ">Built in<") {
		t.Error("a built-in module should read Built in")
	}
	if strings.Contains(bi, "data-tool-toggle") {
		t.Error("a built-in module must not offer a switch")
	}
}

// TestToolRowEscapes ensures hostile field values cannot break out of the
// HTML context.
func TestToolRowEscapes(t *testing.T) {
	out := toolRowHTML(toolState{
		ID: `"><script>alert(1)</script>`, Name: `<img src=x onerror=alert(1)>`,
		Desc: "</div><script>", Toggleable: true,
	})
	if strings.Contains(out, "<script>alert(1)") || strings.Contains(out, "<img src=x") || strings.Contains(out, "</div><script>") {
		t.Error("toolRow did not escape hostile field values")
	}
}
