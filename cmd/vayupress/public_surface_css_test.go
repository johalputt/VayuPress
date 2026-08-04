// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The public member/signup/checkout surface loads exactly two stylesheets:
// /theme.css and one static sheet (signup.css, sometimes alongside article.css).
// /theme.css is NOT a component stylesheet — render.ThemeCSSFor emits only
// custom properties (--pico-primary, --vayu-accent), the installed theme's
// tokens and operator-supplied custom CSS. It defines no class rules the product
// may rely on. So every class the product's own markup emits on this surface has
// to be defined in the static sheet that page links, or it styles nothing.
//
// This has bitten twice. The checkout page reached for the CONSOLE grammar
// (.login-form, .field, .field-label, .input) which lives in admin-os.css and is
// not loaded there, so the two inputs a customer types their name and email into
// rendered as raw browser controls, and the payment error banner rendered as
// bare text. Separately, markup asked for a `su-muted` class when only the
// --su-muted custom property existed.
//
// Two rules, both enforced below:
//
//	NAKED      an element whose classes are ALL undefined renders unstyled.
//	DEAD TOKEN a class on an otherwise-styled element that matches nothing.
//	           Harmless on screen and worse over time: it reads as intent, so
//	           the next person styles around something that was never there.
//
// Scoping is by FUNCTION, not by file. A single file serves two surfaces —
// handlers_team.go renders both handleOSProfile (console, admin-os.css) and
// handlePublicAuthor (public, signup.css) — and mapping stylesheets per-file
// reports the console's grammar as broken on the public page. An earlier pass
// did exactly that and produced 38 findings, every one of them false.
func TestPublicSurfaceClassesAreStyled(t *testing.T) {
	const surfaceSheet = "signup.css"

	var (
		funcStart = regexp.MustCompile(`(?m)^func\b`)
		sheetRef  = regexp.MustCompile(`/static/css/([A-Za-z0-9_.-]+\.css)`)
		classAttr = regexp.MustCompile(`class="([^"]*)"`)
		// Whole identifiers, so membership is an exact string compare. A prefix
		// test would let ".btn--primary" satisfy a lookup for ".btn".
		cssClass    = regexp.MustCompile(`\.(-?[A-Za-z_][A-Za-z0-9_-]*)`)
		cssComment  = regexp.MustCompile(`(?s)/\*.*?\*/`)
		funcNameRe  = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Za-z0-9_]+)`)
		definedIn   = map[string]map[string]bool{}
		loadDefined = func(sheet string) map[string]bool {
			if got, ok := definedIn[sheet]; ok {
				return got
			}
			b, err := os.ReadFile(filepath.Join("..", "..", "static", "css", sheet))
			if err != nil {
				t.Fatalf("read %s: %v", sheet, err)
			}
			names := map[string]bool{}
			for _, m := range cssClass.FindAllStringSubmatch(cssComment.ReplaceAllString(string(b), ""), -1) {
				names[m[1]] = true
			}
			definedIn[sheet] = names
			return names
		}
	)

	goFiles, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(goFiles)

	naked, dead, checked := 0, 0, 0
	for _, path := range goFiles {
		if strings.HasSuffix(path, "_test.go") {
			continue // a gate must not lint itself
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)

		// Chunk on top-level func boundaries. Chunk 0 is everything above the
		// first func — package-level template vars live there and emit markup
		// too (checkoutInstructionsTmpl is one).
		cuts := []int{0}
		for _, loc := range funcStart.FindAllStringIndex(src, -1) {
			cuts = append(cuts, loc[0])
		}
		cuts = append(cuts, len(src))

		for i := 0; i < len(cuts)-1; i++ {
			chunk := src[cuts[i]:cuts[i+1]]
			sheets := map[string]bool{}
			for _, m := range sheetRef.FindAllStringSubmatch(chunk, -1) {
				sheets[m[1]] = true
			}
			if !sheets[surfaceSheet] {
				continue
			}
			known := map[string]bool{}
			for sheet := range sheets {
				for name := range loadDefined(sheet) {
					known[name] = true
				}
			}
			where := path
			if m := funcNameRe.FindStringSubmatch(chunk); m != nil {
				where = path + " " + m[1]
			}

			for _, m := range classAttr.FindAllStringSubmatch(chunk, -1) {
				raw := m[1]
				// Concatenated or templated values cannot be judged statically.
				if strings.ContainsAny(raw, "`+") || strings.Contains(raw, "{{") {
					continue
				}
				var styled, missing []string
				for _, tok := range strings.Fields(raw) {
					if known[tok] {
						styled = append(styled, tok)
					} else {
						missing = append(missing, tok)
					}
				}
				if len(styled) == 0 && len(missing) == 0 {
					continue
				}
				checked++
				switch {
				case len(styled) == 0:
					naked++
					t.Errorf("%s: class=%q renders UNSTYLED — none of it is defined in %s",
						where, raw, strings.Join(sheetNames(sheets), "+"))
				case len(missing) > 0:
					dead++
					t.Errorf("%s: class=%q carries names defined nowhere: %s (styled by %s)",
						where, raw, strings.Join(missing, " "), strings.Join(styled, " "))
				}
			}
		}
	}

	if checked == 0 {
		t.Fatal("checked no elements at all — the scan matched nothing, so it proves nothing")
	}
	t.Logf("checked %d class attributes on the %s surface: %d naked, %d dead-token",
		checked, surfaceSheet, naked, dead)
}

func sheetNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
