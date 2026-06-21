package main

import (
	"strings"
	"testing"
)

// TestToolCardHTMLCSPSafe ensures a rendered module card carries no inline
// styles or external hosts and reflects its status correctly.
func TestToolCardHTMLCSPSafe(t *testing.T) {
	on := toolCardHTML(toolState{
		ID: "comments", Name: "Comments", Desc: "Reader comments",
		Category: "Engagement", Icon: "💬", Toggleable: true, Enabled: true, Ready: true,
	})
	assertCSPSafe(t, "toolCardHTML", on)
	if !strings.Contains(on, `data-tool-toggle="comments"`) {
		t.Error("toggleable card missing switch input")
	}
	if !strings.Contains(on, "Active") {
		t.Error("enabled+ready card should read Active")
	}
	if !strings.Contains(on, "checked") {
		t.Error("enabled card switch should be checked")
	}
}

// TestToolCardHTMLDisabled verifies a disabled toggleable module reads Disabled
// and its switch is not checked.
func TestToolCardHTMLDisabled(t *testing.T) {
	off := toolCardHTML(toolState{
		ID: "newsletter", Name: "Newsletter", Toggleable: true, Enabled: false, Ready: true,
	})
	if !strings.Contains(off, "Disabled") {
		t.Error("disabled card should read Disabled")
	}
	if strings.Contains(off, " checked") {
		t.Error("disabled card switch must not be checked")
	}
}

// TestToolCardHTMLBuiltin verifies a non-toggleable module shows the Built-in
// badge and no switch.
func TestToolCardHTMLBuiltin(t *testing.T) {
	bi := toolCardHTML(toolState{
		ID: "diagrams", Name: "Diagrams", Toggleable: false, Ready: true,
	})
	if !strings.Contains(bi, "Built-in") {
		t.Error("built-in card should show the Built-in badge")
	}
	if strings.Contains(bi, "data-tool-toggle") {
		t.Error("built-in card must not render a toggle switch")
	}
}

// TestToolCardHTMLEscapes ensures hostile field values cannot break out of the
// HTML context.
func TestToolCardHTMLEscapes(t *testing.T) {
	out := toolCardHTML(toolState{
		ID: `"><script>alert(1)</script>`, Name: `<img src=x onerror=alert(1)>`,
		Desc: "</div><script>", Toggleable: true,
	})
	if strings.Contains(out, "<script>alert(1)") || strings.Contains(out, "<img src=x") {
		t.Error("toolCardHTML did not escape hostile field values")
	}
}
