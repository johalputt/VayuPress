// SPDX-License-Identifier: Apache-2.0

package main

// native_dialog_test.go — the console's dialogs are the console's own.
//
// window.alert/confirm/prompt are unstyled browser chrome that block the whole
// tab, cannot be themed, ignore the shell's Escape and focus conventions, and read
// as a different product from the rest of VayuOS. Every first-party console script
// now routes through vpToast / vpConfirm / vpPrompt.
//
// This is a repo-wide rule rather than a per-file one because the migration was
// per-file: without a test, the next app added would quietly reintroduce them.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vendorJS is third-party code bundled into the console; it is not ours to police.
var vendorJS = map[string]bool{
	"htmx.min.js":            true,
	"alpine-csp.min.js":      true,
	"purify.min.js":          true,
	"alpine-csp.LICENSE":     true,
	"purify.LICENSE":         true,
	"vayu-islands.js":        true, // references Alpine by name only, but keep the allowlist explicit
	"theme-preview-frame.js": true,
}

func TestConsoleScriptsUseTheConsolesOwnDialogs(t *testing.T) {
	dir := filepath.Join("..", "..", "static", "js")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read static/js: %v", err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".js") || vendorJS[name] {
			continue
		}
		src := withoutComments(repoFile(t, filepath.Join("static", "js", name)))
		checked++

		// alert/confirm/prompt that RETURN a value cannot be shimmed, so these are
		// never acceptable.
		for _, banned := range []string{"window.confirm(", "window.prompt("} {
			if strings.Contains(src, banned) {
				t.Errorf("%s still calls %s — use vpConfirm/vpPrompt", name, banned)
			}
		}
		// A bare call is the native global too, unless the file defines its own.
		for _, fn := range []string{"alert", "confirm", "prompt"} {
			if strings.Contains(src, fn+"(") && definesLocal(src, fn) {
				continue // a deliberate local shim (see admin-os-members.js)
			}
			if containsCall(src, fn) {
				t.Errorf("%s calls the native %s() — route it through vpToast/vpConfirm/vpPrompt", name, fn)
			}
		}
		// window.alert is allowed exactly once, and only as the toast fallback.
		if n := strings.Count(src, "window.alert("); n > 0 {
			if name != "admin-os-mail.js" || n > 1 {
				t.Errorf("%s calls window.alert %d time(s); only admin-os-mail.js's documented toast fallback may", name, n)
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d first-party scripts were checked — the scan is not looking in the right place", checked)
	}
}

// definesLocal reports whether src declares its own function of that name (the
// shadowing shim is deliberate and documented where it is used).
func definesLocal(src, fn string) bool {
	return strings.Contains(src, "function "+fn+"(") || strings.Contains(src, "var "+fn+" = function")
}

// containsCall reports whether fn is called as a bare identifier — not as a
// property (obj.fn()), not as part of a longer word, and not in prose.
func containsCall(src, fn string) bool {
	for i := 0; ; {
		idx := strings.Index(src[i:], fn+"(")
		if idx < 0 {
			return false
		}
		at := i + idx
		if at == 0 || !isIdentByte(src[at-1]) {
			// Guard against `window.alert(` / `.prompt(` being read as bare calls:
			// a preceding '.' or an identifier character means it is a property.
			if at == 0 || (src[at-1] != '.') {
				return true
			}
		}
		i = at + len(fn) + 1
		if i >= len(src) {
			return false
		}
	}
}

func isIdentByte(b byte) bool {
	return b == '_' || b == '$' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b >= 0x80
}

// TestTheShellProvidesBothDialogs — the migration depends on these existing on
// every console page, so their absence would turn a click into a dead button.
func TestTheShellProvidesBothDialogs(t *testing.T) {
	ui := readFileString(t, "admin_os_ui.go")
	for _, want := range []string{"window.vpConfirm=function", "window.vpPrompt=function"} {
		if !strings.Contains(ui, want) {
			t.Errorf("the shell bootstrap is missing %s", want)
		}
	}
	// vpPrompt's styling hook must exist (its field is wrapped in this class).
	if !strings.Contains(adminOSCSS(t), ".vp-os .vp-confirm__label") {
		t.Error("vpPrompt renders .vp-confirm__label with no rule in admin-os.css")
	}
}

// TestConsoleInlineScriptsUseTheConsolesOwnDialogs — the scan above reads
// static/js only, and three native dialogs lived in scripts the Go code serves
// inline: the Tor-world Rotate confirm and the backup Restore prompt among them.
// Every non-test Go file is read here, comments stripped.
func TestConsoleInlineScriptsUseTheConsolesOwnDialogs(t *testing.T) {
	// The members' own account page is the public site, not the console: it has
	// no vpConfirm/vpPrompt to call.
	outsideConsole := map[string]bool{"handlers_member_account_script.go": true}
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) < 50 {
		t.Fatalf("read the package: %v (%d files)", err, len(files))
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || outsideConsole[f] {
			continue
		}
		src := withoutComments(readFileString(t, f))
		for _, banned := range []string{"window.confirm(", "window.prompt(", "window.alert("} {
			if strings.Contains(src, banned) {
				t.Errorf("%s serves a script that calls %s — use vpConfirm/vpPrompt/vpToast", f, banned)
			}
		}
	}
}
