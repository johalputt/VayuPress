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

// consoleUnstyledBaseline is EMPTY, and the point is to keep it that way.
//
// It held 43 entries. Splitting them turned out to be the whole job, and the
// split was not the one first assumed:
//
//   - 22 were elements carrying NO styled class at all, rendering as bare inline
//     text. Each was given a rule.
//   - The other 21 were assumed to be JavaScript hooks and left alone — a claim
//     made without checking. Checking found no CSS rule, no JS selector, no Go
//     selector and no test reference for any of them: they were class names
//     emitted into markup that matched nothing anywhere. They are removed.
//
// A class that matches nothing is worse than a missing rule. A missing rule
// looks wrong on screen and gets fixed; a dead class name reads as intent, and
// the next person to touch that markup styles around something that was never
// there.
//
// Shrink it by giving a class a rule or by deleting the class. Never add to it.
var consoleUnstyledBaseline = map[string]bool{}

// consoleUtilityClasses are spacing/typography tokens carried by the shell's own
// base rules rather than by a named component.
var consoleUtilityClasses = map[string]bool{
	"mono": true, "muted": true, "hidden": true,
	"text-sm": true, "text-xs": true, "text-lg": true,
	"mb-2": true, "mb-3": true, "mb-6": true, "mt-1": true, "mt-2": true, "mt-3": true, "mt-4": true,
}

var goClassLiteralRe = regexp.MustCompile(`class="([a-zA-Z0-9_\- ]+)"`)

// consoleShellCall marks a file as rendering into the VayuOS shell. Selecting
// console files by NAME alone missed four that render into the shell without the
// admin_os_/vayuos prefix — handlers_bizsite.go among them, which is how a bare
// `biz-deploy` div survived on the .zip deploy control while every sibling
// biz-* class had a rule. Membership is now decided by what a file calls, not by
// what it is called.
var consoleShellCall = regexp.MustCompile(`adminOSShellHead|writeOSHTML`)

// ownStylesheetRe marks a chunk that links its OWN stylesheet, so it belongs to
// some other surface and admin-os.css says nothing about it. handlers_team.go
// renders BOTH a console page and a public author page; judging the whole file
// against the console's sheet would report the public page's entire grammar as
// unstyled.
var ownStylesheetRe = regexp.MustCompile(`/static/css/`)

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
			continue
		}
		src, err := os.ReadFile(f) // #nosec G304 -- walking this repository
		if err == nil && consoleShellCall.Match(src) {
			out = append(out, f)
		}
	}
	if len(out) < 20 {
		t.Fatalf("only %d console files found; the glob is wrong and this gate is checking nothing", len(out))
	}
	return out
}

// consoleChunks splits a source file at top-level func boundaries and drops the
// chunks that link their own stylesheet. What remains is markup the VayuOS shell
// styles.
func consoleChunks(src string) []string {
	cuts := []int{0}
	for _, loc := range regexp.MustCompile(`(?m)^func\b`).FindAllStringIndex(src, -1) {
		cuts = append(cuts, loc[0])
	}
	cuts = append(cuts, len(src))
	var out []string
	for i := 0; i < len(cuts)-1; i++ {
		if chunk := src[cuts[i]:cuts[i+1]]; !ownStylesheetRe.MatchString(chunk) {
			out = append(out, chunk)
		}
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
		for _, chunk := range consoleChunks(string(src)) {
			for _, m := range goClassLiteralRe.FindAllStringSubmatch(chunk, -1) {
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
