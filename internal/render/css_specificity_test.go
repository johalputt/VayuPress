// SPDX-License-Identifier: Apache-2.0

package render

import (
	"regexp"
	"strings"
	"testing"
)

// The defect this pins was visible on a published page and invisible to every
// other gate: the video played, but the injected iframe sat at its intrinsic
// size in the middle of a full-width black box.
//
// Nothing was broken in Go. It was a cascade collision. The article stylesheet
// carries generic rules for the elements a body can contain —
//
//	.content iframe, .content video { max-width:100%; height:auto }
//	.content img                    { height:auto; border:…; border-radius:… }
//
// — and those select a class AND an element, which is specificity (0,1,1). The
// component rules that make a facade work were written as a single class,
// (0,1,0). The generic rule wins, `height:auto` replaces `height:100%`, and the
// player never fills its frame.
//
// This is the failure mode the design guidance in this repo already names:
// classes that quietly cancel each other out. It cannot be caught by rendering
// HTML, because the markup is correct — only the computed style is wrong. So the
// assertion has to be about the stylesheet itself, and it has to be about
// SPECIFICITY rather than about the presence of a string, or it would pass
// against a rule that is present and losing.
func TestComponentRulesOutSpecifyTheGenericContentRules(t *testing.T) {
	css := articleCSSMin

	cases := []struct {
		component string // the component rule that must win
		generic   string // the generic .content rule it competes with
		property  string // the property they both set
		why       string
	}{
		{
			component: ".video-facade .video-facade__frame",
			generic:   ".content iframe",
			property:  "height",
			why: "the iframe injected on click must fill the facade; losing this leaves the " +
				"player at its intrinsic size inside a full-width black box",
		},
		{
			component: ".video-facade .video-facade__poster",
			generic:   ".content img",
			property:  "height",
			why:       "the poster must cover the facade rather than sit at its natural height",
		},
		{
			component: ".embed-card .embed-card__thumb img",
			generic:   ".content img",
			property:  "height",
			why:       "a link card's thumbnail has a fixed height; losing it collapses the card layout",
		},
	}

	for _, c := range cases {
		t.Run(c.component, func(t *testing.T) {
			if !ruleSets(css, c.component, c.property) {
				t.Fatalf("no rule for %q sets %q in the shipped stylesheet", c.component, c.property)
			}
			// Deliberately a failure, not a skip. If the generic rule stopped
			// setting this property the collision would be gone — but so would
			// the reason this test exists, and a silent skip is how the first
			// version of it passed while the bug was live.
			if !ruleSets(css, c.generic, c.property) {
				t.Fatalf("%q no longer sets %q; this test's premise has changed and it needs re-deriving, "+
					"rather than quietly passing", c.generic, c.property)
			}
			comp, gen := specificity(c.component), specificity(c.generic)
			if comp <= gen {
				t.Errorf("%s has specificity %d and loses to %s at %d, so %s wins on %q.\n\n%s",
					c.component, comp, c.generic, gen, c.generic, c.property, c.why)
			}

			// And no UNSCOPED variant may set the same property. One scoped rule
			// is not enough on its own: this component's base rule and its
			// responsive override are separate declarations, and leaving either
			// unscoped hands that breakpoint back to the generic rule. Reverting
			// only the base rule survived the first version of this test, because
			// the media-query variant was still scoped and satisfied the check —
			// so the layout was correct above 600px and collapsed below it.
			if unscoped := dropScope(c.component); ruleSets(css, unscoped, c.property) {
				t.Errorf("%q also sets %q while unscoped, so it loses to %q at that breakpoint.\n\n%s",
					unscoped, c.property, c.generic, c.why)
			}
		})
	}
}

// cssRuleRe matches innermost rules only: the selector part cannot contain a
// brace, so a media query's outer block never matches and its inner rules do.
var cssRuleRe = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)

// ruleSets reports whether ANY rule whose selector list contains selector also
// sets property.
//
// The list part is the whole reason this is not a substring search. The rule
// that broke the facade is written `.content iframe,.content video{…}`, so
// looking for ".content iframe{" finds nothing and the check silently skips —
// which is exactly what the first version of this test did, reporting three
// skips and a pass while the defect was live on a published page.
func ruleSets(css, selector, property string) bool {
	decl := regexp.MustCompile(`(^|;)\s*` + regexp.QuoteMeta(property) + `\s*:`)
	for _, m := range cssRuleRe.FindAllStringSubmatch(css, -1) {
		for _, sel := range strings.Split(m[1], ",") {
			if strings.TrimSpace(sel) != selector {
				continue
			}
			if decl.MatchString(m[2]) {
				return true
			}
		}
	}
	return false
}

// dropScope removes the leading scope class from a component selector, giving
// the unscoped form that must NOT exist: ".video-facade .video-facade__frame"
// becomes ".video-facade__frame".
func dropScope(sel string) string {
	parts := strings.Fields(sel)
	if len(parts) < 2 {
		return sel
	}
	return strings.Join(parts[1:], " ")
}

// specificity scores a simple selector as (classes*10 + elements), which is all
// this comparison needs: none of these rules use ids or inline styles, and the
// interesting comparison is exactly "two classes" against "one class plus one
// element" — the pair a hand count gets wrong.
func specificity(sel string) int {
	classes := strings.Count(sel, ".")
	elements := 0
	for _, part := range strings.Fields(sel) {
		if !strings.HasPrefix(part, ".") {
			elements++
			continue
		}
		// ".embed-card__thumb img" style compounds are already split by Fields;
		// a trailing element glued to a class (".a img") cannot occur without a
		// space, so nothing more is needed here.
		_ = part
	}
	return classes*10 + elements
}

// The generic rules are load-bearing for ordinary prose and must stay. If a
// future edit "fixes" a collision by deleting them, wide images and tables would
// start overflowing the column on small screens — so their presence is pinned
// alongside the component rules that must beat them.
func TestGenericContentRulesStillConstrainOrdinaryMedia(t *testing.T) {
	for _, want := range []string{".content iframe", ".content img", ".content pre"} {
		if !strings.Contains(articleCSSMin, want) {
			t.Errorf("the generic rule %q is gone; ordinary body media is no longer constrained "+
				"to the column, which is what these rules exist for", want)
		}
	}
}
