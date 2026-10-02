// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Motion is on the tokens (Mail plan §8, "Tokens only"). In the Still Air
// console a transition or a one-off animation takes its duration from
// --duration-micro, -standard or -major, never a literal, and moves only
// what the compositor draws without laying the page out again: transform,
// opacity and colours. Keyframes are held to the same properties.
//
// An infinite animation is a busy indicator: it runs until the work ends and
// moves between no two states, so it is not a transition and keeps its own
// pace. A visibility switch with a 0s duration is a timed toggle that lets a
// slide finish before the element leaves the tab order, not a movement.
func TestMotionIsOnTheTokens(t *testing.T) {
	css := readFileString(t, filepath.Join("..", "..", "static", "css", "vayuos.css"))
	for _, f := range motionFaults(css) {
		t.Error(f)
	}
}

var (
	cssComment     = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssRule        = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
	cssKeyframes   = regexp.MustCompile(`@keyframes\s+([\w-]+)\s*\{((?:[^{}]*\{[^{}]*\})*)\s*\}`)
	cssTimeLiteral = regexp.MustCompile(`(?:^|[\s,(])(\d*\.?\d+m?s)\b`)
)

// motionAllowed is what may move: what the compositor draws on its own.
var motionAllowed = map[string]bool{
	"transform": true, "opacity": true, "color": true,
	"background-color": true, "border-color": true, "box-shadow": true,
}

func motionFaults(css string) []string {
	css = cssComment.ReplaceAllString(css, "")
	var faults []string
	// Keyframes first, and out of the way of the rule scan below. Only the
	// ones a Still Air rule runs as a one-off movement are held to it.
	moves := map[string][]string{}
	css = cssKeyframes.ReplaceAllStringFunc(css, func(block string) string {
		m := cssKeyframes.FindStringSubmatch(block)
		for _, step := range regexp.MustCompile(`\{([^{}]*)\}`).FindAllStringSubmatch(m[2], -1) {
			for _, decl := range strings.Split(step[1], ";") {
				if k, _, ok := strings.Cut(decl, ":"); ok {
					moves[m[1]] = append(moves[m[1]], strings.TrimSpace(k))
				}
			}
		}
		return ""
	})
	for _, r := range cssRule.FindAllStringSubmatch(css, -1) {
		sel, body := strings.TrimSpace(r[1]), r[2]
		if !strings.Contains(sel, `data-ui="still-air"`) {
			continue
		}
		for _, decl := range strings.Split(body, ";") {
			k, v, ok := strings.Cut(decl, ":")
			if !ok {
				continue
			}
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "!important"))
			if v == "none" {
				continue
			}
			where := firstLine(sel) + " { " + k + ": " + v + " }"
			switch k {
			case "transition", "transition-property", "transition-duration":
				for _, part := range strings.Split(v, ",") {
					words := strings.Fields(part)
					if len(words) == 0 {
						continue
					}
					prop := words[0]
					if k == "transition-duration" {
						prop = ""
					}
					timed := cssTimeLiteral.FindAllStringSubmatch(part, -1)
					if prop == "visibility" && len(timed) > 0 && strings.HasPrefix(timed[0][1], "0") {
						continue // a timed switch, not a movement
					}
					if prop != "" && !motionAllowed[prop] {
						faults = append(faults, "transitions "+prop+", which is not transform, opacity or a colour: "+where)
					}
					for _, lit := range timed {
						if lit[1] != "0s" {
							faults = append(faults, "a literal duration "+lit[1]+" instead of a --duration token: "+where)
						}
					}
				}
			case "animation", "animation-duration":
				if strings.Contains(v, "infinite") {
					continue // a busy indicator
				}
				for _, lit := range cssTimeLiteral.FindAllStringSubmatch(v, -1) {
					faults = append(faults, "a literal duration "+lit[1]+" instead of a --duration token: "+where)
				}
				if k == "animation" {
					name := strings.Fields(v)[0]
					for _, p := range moves[name] {
						if !motionAllowed[p] {
							faults = append(faults, "@keyframes "+name+" moves "+p+", which is not transform, opacity or a colour: "+where)
						}
					}
				}
			}
		}
	}
	return faults
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i]) + " …"
	}
	return s
}

// The gate is seen to fail on the thing it forbids, each rule by itself.
func TestTheMotionGateRefusesEachFault(t *testing.T) {
	for name, c := range map[string]struct{ css, want string }{
		"a literal transition": {`.vp-os[data-ui="still-air"] .x { transition: opacity 300ms; }`, "a literal duration 300ms"},
		"a layout property":    {`.vp-os[data-ui="still-air"] .x { transition: width var(--duration-micro); }`, "transitions width"},
		"a literal animation":  {`.vp-os[data-ui="still-air"] .x { animation: f 0.2s; } @keyframes f { to { opacity: 0; } }`, "a literal duration 0.2s"},
		"keyframes on layout":  {`.vp-os[data-ui="still-air"] .x { animation: g var(--duration-micro); } @keyframes g { to { height: 0; } }`, "moves height"},
	} {
		got := strings.Join(motionFaults(c.css), "\n")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: the gate said %q, want %q", name, got, c.want)
		}
	}
	for name, css := range map[string]string{
		"tokens":          `.vp-os[data-ui="still-air"] .x { transition: opacity var(--duration-micro) var(--ease-move); animation: f var(--duration-major); } @keyframes f { to { transform: none; } }`,
		"an indicator":    `.vp-os[data-ui="still-air"] .x { animation: spin 0.7s linear infinite; }`,
		"a timed switch":  `.vp-os[data-ui="still-air"] .x { transition: transform var(--duration-standard), visibility 0s linear var(--duration-standard); }`,
		"the classic css": `.vp-os .x { transition: width 300ms; }`,
	} {
		if f := motionFaults(css); len(f) > 0 {
			t.Errorf("%s: refused %v", name, f)
		}
	}
}
