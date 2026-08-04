// SPDX-License-Identifier: Apache-2.0

package main

// css_console_baseline_test.go — no NEW unstyled class in the VayuOS console.
//
// The per-page gate in css_contract_test.go checks the pages someone remembered
// to hand it. That is how `page-head` and `page-title` survived on seven pages:
// three of them were never passed to it, and the ones that were passed matched
// `.page-header` on a substring test.
//
// This gate reads the SOURCE of every file that renders into the VayuOS shell
// and holds it against admin-os.css — the one stylesheet that shell loads. It is
// the deadcode gate's shape: a frozen baseline of what is already wrong, and a
// failure for anything added to it. Forty-three classes render unstyled today;
// each needs a design decision rather than a mechanical fix, and pretending
// otherwise by deleting the list would be worse than carrying it.
//
// Reducing the baseline is the point. Growing it is the thing this refuses.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// consoleUnstyledBaseline is what already renders unstyled in the console.
// Shrink it by adding rules; never add to it.
// consoleUnstyledBaseline is what carries no rule of its own in the console.
//
// It was 43. Twenty-two of those were elements with NO styled class at all,
// which rendered as bare inline text; each now has a rule. What is left are
// tokens on elements a base class already styles — a `btn`, a `card`, a
// `mon-acc__sum` — so they are JavaScript hooks and identifiers rather than
// missing styling, and inventing a look for them would be worse than leaving
// them.
//
// That distinction is the whole lesson of this pass. The first version of this
// gate counted all 43 as the same defect, which would have meant writing
// twenty-one CSS rules for things that are not meant to look like anything.
//
// Shrink it by giving a class a rule; never add to it.
var consoleUnstyledBaseline = map[string]bool{
	"ak-cred-card--custom": true, "editor-hint": true, "editor-html-hint": true, "editor-md": true,
	"empty": true, "media-empty": true, "post-acc__del": true, "post-acc__sum": true,
	"vm-acct__usage": true, "vm-contact-add": true, "vm-contacts-empty": true, "vm-ed-count": true,
	"vm-encrypt-row": true, "vp-pt__tip-dot": true, "vt-bridges": true, "vt-hardening": true,
	"vt-health": true, "vt-note": true, "vt-pages": true, "vt-vanity": true,
	"world-card__copy": true,
}

// consoleUtilityClasses are spacing/typography tokens carried by the shell's own
// base rules rather than by a named component.
var consoleUtilityClasses = map[string]bool{
	"mono": true, "muted": true, "hidden": true,
	"text-sm": true, "text-xs": true, "text-lg": true,
	"mb-2": true, "mb-3": true, "mb-6": true, "mt-1": true, "mt-2": true, "mt-3": true, "mt-4": true,
}

var goClassLiteralRe = regexp.MustCompile(`class="([a-zA-Z0-9_\- ]+)"`)

// consoleSourceFiles are the files that render into the VayuOS shell, and so are
// styled by admin-os.css and nothing else. The theme store renders a PUBLIC page
// with the site's own stylesheet, so it is not one of them.
func consoleSourceFiles(t *testing.T) []string {
	t.Helper()
	all, err := filepath.Glob(filepath.Join("..", "..", "cmd", "vayupress", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var out []string
	for _, f := range all {
		b := filepath.Base(f)
		if strings.HasSuffix(b, "_test.go") || strings.Contains(b, "theme_store") {
			continue
		}
		if strings.HasPrefix(b, "admin_os_") || strings.HasPrefix(b, "vayuos") {
			out = append(out, f)
		}
	}
	if len(out) < 20 {
		t.Fatalf("only %d console files found; the glob is wrong and this gate is checking nothing", len(out))
	}
	return out
}

func TestNoNewUnstyledClassInTheVayuOSConsole(t *testing.T) {
	css := loadAdminOSCSS(t)
	seen := map[string]string{}
	for _, f := range consoleSourceFiles(t) {
		src, err := os.ReadFile(f) // #nosec G304 -- walking this repository
		if err != nil {
			continue
		}
		for _, m := range goClassLiteralRe.FindAllStringSubmatch(string(src), -1) {
			for _, cls := range strings.Fields(m[1]) {
				if consoleUtilityClasses[cls] || consoleUnstyledBaseline[cls] || cssDefines(css, cls) {
					continue
				}
				if _, dup := seen[cls]; !dup {
					seen[cls] = filepath.Base(f)
				}
			}
		}
	}
	var novel []string
	for cls, f := range seen {
		novel = append(novel, cls+" ("+f+")")
	}
	if len(novel) > 0 {
		sort.Strings(novel)
		t.Errorf("%d class(es) render unstyled in the VayuOS console and are not in the known "+
			"baseline:\n  %s\nAdd a rule to static/css/admin-os.css. An element carrying a class "+
			"with no rule renders as bare inline text, and every test about its content still passes.",
			len(novel), strings.Join(novel, "\n  "))
	}
}

// The baseline must shrink, not rot. A class that has since been given a rule is
// removed from the list, so the list keeps meaning "still unstyled" rather than
// drifting into a permanent allowlist nobody rereads.
func TestTheUnstyledBaselineCarriesNothingAlreadyFixed(t *testing.T) {
	css := loadAdminOSCSS(t)
	for cls := range consoleUnstyledBaseline {
		if cssDefines(css, cls) {
			t.Errorf("%q has a rule now — remove it from consoleUnstyledBaseline so the list keeps "+
				"meaning what it says", cls)
		}
	}
}
