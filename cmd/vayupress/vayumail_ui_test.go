package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// withoutComments strips Go, CSS and HTML comments so a check cannot match the
// prose that explains why the thing it forbids is absent — every one of these
// assertions tripped on its own rationale before this existed.
func withoutComments(src string) string {
	src = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(src, "")  // CSS + block
	src = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(src, "") // HTML
	return regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAllString(src, "") // Go line
}

// VayuMail's reading pane rendered the message in a 13px monospace <pre> capped at
// max-height: 65vh with its own overflow. In full view that put a scrollbar inside
// the pane's scrollbar and squeezed the text into two thirds of a screen it had
// all of — mail read like a log file in a box. These pin the fix, because
// "max-height on the body" is the kind of rule that looks harmless in review.

func adminOSCSS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "static", "css", "admin-os.css"))
	if err != nil {
		t.Fatalf("read admin-os.css: %v", err)
	}
	return string(b)
}

func mailJS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "static", "js", "admin-os-mail.js"))
	if err != nil {
		t.Fatalf("read admin-os-mail.js: %v", err)
	}
	return string(b)
}

// TestMessageBodyIsNotItsOwnScrollContainer is the core guard: the pane scrolls,
// the body flows. Two scrollbars for one document is the bug.
func TestMessageBodyIsNotItsOwnScrollContainer(t *testing.T) {
	css := adminOSCSS(t)
	block := cssBlock(t, css, ".vm-pre {")
	for _, banned := range []string{"max-height", "overflow"} {
		if strings.Contains(block, banned) {
			t.Errorf(".vm-pre must not declare %s — it nests a scrollbar inside the reading pane; block was:\n%s", banned, block)
		}
	}
	// The raw source dump is the one place a cap belongs.
	if !strings.Contains(cssBlock(t, css, ".vp-os .vm-raw {"), "max-height") {
		t.Error(".vm-raw should keep a max-height — it is a dump, not prose")
	}
}

// TestMessageBodyReadsAsProse checks the reading column exists: proportional type
// at a comfortable measure, not the inherited <pre> monospace.
func TestMessageBodyReadsAsProse(t *testing.T) {
	block := withoutComments(cssBlock(t, adminOSCSS(t), ".vp-os .vm-msg-body {"))
	for _, want := range []string{"font-family", "line-height", "max-width"} {
		if !strings.Contains(block, want) {
			t.Errorf(".vm-msg-body must declare %s so mail reads as prose; block was:\n%s", want, block)
		}
	}
	if strings.Contains(block, "monospace") {
		t.Error("the message body must not be monospace — nothing about plain-text mail requires fixed pitch")
	}
}

// TestFullViewKeepsActionsReachable pins the same lesson the member portal's close
// button taught: in a scrolling container, a control that scrolls away is a
// control that is not there.
func TestFullViewKeepsActionsReachable(t *testing.T) {
	css := adminOSCSS(t)
	i := strings.Index(css, "#vm-readpane.vm-readpane--full .vm-actions")
	if i < 0 {
		t.Fatal("full view must pin the action bar")
	}
	if !strings.Contains(css[i:i+400], "position: sticky") {
		t.Error("the full-view action bar must be sticky, or it scrolls out of reach on a long message")
	}
}

// TestComposerHasAFormattingToolbar guards the editor controls. The message field
// was a bare textarea with no way to structure anything.
func TestComposerHasAFormattingToolbar(t *testing.T) {
	src, err := os.ReadFile("vayuos_mail.go")
	if err != nil {
		t.Fatalf("read vayuos_mail.go: %v", err)
	}
	page := string(src)
	for _, want := range []string{
		`data-c-fmt="bold"`, `data-c-fmt="italic"`, `data-c-fmt="strike"`,
		`data-c-fmt="h2"`, `data-c-fmt="ul"`, `data-c-fmt="ol"`,
		`data-c-fmt="quote"`, `data-c-fmt="code"`, `data-c-fmt="link"`, `data-c-fmt="rule"`,
		"data-c-preview", "data-c-count",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the composer is missing the %q control", want)
		}
	}
	// A toolbar is a toolbar for assistive technology too.
	if !strings.Contains(page, `role="toolbar"`) {
		t.Error("the formatting bar must expose role=toolbar")
	}
}

// TestComposerPreviewNeverInjectsMarkup is the security property. The preview
// renders text the sender typed; building it with innerHTML would turn the
// composer into a self-XSS against the console.
func TestComposerPreviewNeverInjectsMarkup(t *testing.T) {
	js := mailJS(t)
	start := strings.Index(js, "previewBtn.addEventListener")
	if start < 0 {
		t.Fatal("the preview handler is missing")
	}
	end := strings.Index(js[start:], "// Reveal Cc/Bcc")
	if end < 0 {
		end = len(js) - start
	}
	if strings.Contains(withoutComments(js[start:start+end]), "innerHTML") {
		t.Error("the preview must build nodes with textContent — innerHTML here is self-XSS from the composer's own field")
	}
}

// TestComposerFormattingStaysPlainText pins the honesty constraint. The engine
// sends mail.ComposeMessage.Body as text, with no HTML alternative part, so the
// toolbar must write conventions INTO the text rather than pretend to style it.
// A contenteditable WYSIWYG here would promise formatting the recipient never
// receives.
func TestComposerFormattingStaysPlainText(t *testing.T) {
	src, err := os.ReadFile("vayuos_mail.go")
	if err != nil {
		t.Fatalf("read vayuos_mail.go: %v", err)
	}
	if !strings.Contains(string(src), `data-c-body placeholder="Write your message…"`) {
		t.Error("the body must remain a textarea; a contenteditable would imply an HTML part the engine does not build")
	}
	if strings.Contains(withoutComments(string(src)), "contenteditable") {
		t.Error("no contenteditable in the composer — see the comment above the toolbar")
	}
}
