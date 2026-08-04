// SPDX-License-Identifier: Apache-2.0

package main

// site_csp_test.go — the per-domain opt-out of the no-eval rule.
//
// It exists because the strict baseline made a whole class of site impossible
// rather than merely awkward: mainstream front-end libraries compile the
// expression strings written in markup into functions at runtime, the policy
// refuses that, and the page renders inert with nothing explaining why.
//
// The opt-in is therefore a real product need. It is also a real loosening, so
// the value of this file is entirely in what it proves the relaxation CANNOT
// reach. A setting that quietly widened the policy for the panel would be worse
// than never having offered it.

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/render"
)

// The relaxed policy must differ from the baseline in exactly one directive.
// Anything else that drifted in would be a second, unrequested loosening riding
// along with the one the operator agreed to.
func TestTheRelaxedPolicyChangesScriptSrcAndNothingElse(t *testing.T) {
	const nonce = "testnonce"
	base := render.BuildCSP(nonce, nil)
	eased := render.BuildCSPAllowingEval(nonce)

	if base == eased {
		t.Fatal("the relaxed policy is identical to the baseline, so the opt-in does nothing")
	}
	if !strings.Contains(eased, "'unsafe-eval'") {
		t.Fatal("the relaxed policy does not actually admit eval, so the library it exists for " +
			"still will not run")
	}
	if strings.Contains(base, "'unsafe-eval'") {
		t.Fatal("the BASELINE admits eval — the opt-in is meaningless because every site " +
			"already has it")
	}

	// Compare directive by directive. Only script-src may differ.
	split := func(s string) map[string]string {
		out := map[string]string{}
		for _, d := range strings.Split(s, ";") {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			if i := strings.Index(d, " "); i > 0 {
				out[d[:i]] = d[i+1:]
			} else {
				out[d] = ""
			}
		}
		return out
	}
	b, e := split(base), split(eased)
	if len(b) != len(e) {
		t.Fatalf("the relaxed policy has a different set of directives (%d vs %d)", len(b), len(e))
	}
	for k, bv := range b {
		ev, ok := e[k]
		if !ok {
			t.Errorf("directive %q disappeared from the relaxed policy", k)
			continue
		}
		if k == "script-src" {
			continue
		}
		if bv != ev {
			t.Errorf("directive %q changed and should not have:\n  baseline: %s\n  relaxed:  %s", k, bv, ev)
		}
	}

	// The sources themselves must stay 'self' — eval is the concession, not a
	// licence to pull code from anywhere.
	//
	// Asserted as a WHITELIST of permitted tokens rather than a search for
	// suspicious ones. The first version looked for "https://" and a mutation
	// that added the bare scheme source `https:` sailed straight past it: one
	// character short of the pattern, and a policy that admits every host on the
	// web would have been reported as unchanged.
	for _, tok := range strings.Fields(e["script-src"]) {
		switch {
		case tok == "'self'", tok == "'unsafe-eval'":
		case strings.HasPrefix(tok, "'nonce-"), strings.HasPrefix(tok, "'sha256-"),
			strings.HasPrefix(tok, "'sha384-"), strings.HasPrefix(tok, "'sha512-"):
		default:
			t.Errorf("script-src carries an unexpected source %q. The opt-in concedes eval and "+
				"nothing else — a host or scheme source here would let the page load code from "+
				"somewhere the operator never agreed to.\n  full directive: %s", tok, e["script-src"])
		}
	}
}

// THE POINT OF THE WHOLE FILE. The opt-in belongs to one static site on one
// hosted domain. It must never follow a visitor into a surface that carries a
// session, where eval turns a small injection into account takeover.
func TestTheRelaxationNeverReachesAnAuthenticatedSurface(t *testing.T) {
	mustRefuse := []string{
		"/os", "/os/", "/os/settings", "/os/api/shield/rescue",
		"/api", "/api/v1/members/me",
		"/admin", "/admin/theme",
		"/oauth", "/oauth/authorize",
		"/mcp", "/mcp/messages",
		"/__vayushield/pow", "/__vayuanalytics/enter",
	}
	for _, p := range mustRefuse {
		refused := false
		for _, pre := range evalRefusedPrefixes {
			if p == pre || strings.HasPrefix(p, pre+"/") {
				refused = true
				break
			}
		}
		if !refused {
			t.Errorf("%q would receive the relaxed policy. A session lives behind that path, and "+
				"'unsafe-eval' there converts an injected string into full control of the "+
				"operator's account", p)
		}
	}
}

// And it must still apply to the thing it was built for, or the feature is a
// refusal wearing a setting's clothes.
func TestAnOrdinarySitePathIsNotRefused(t *testing.T) {
	for _, p := range []string{"/", "/index.html", "/assets/app.js", "/assets/site.css", "/about"} {
		for _, pre := range evalRefusedPrefixes {
			if p == pre || strings.HasPrefix(p, pre+"/") {
				t.Errorf("%q is refused by prefix %q, so the opted-in site still cannot run", p, pre)
			}
		}
	}
}

// A prefix match must not catch a path that merely STARTS with those letters.
// "/oscar" is not "/os", and refusing it would break an innocent page for a
// reason nobody could find.
func TestThePrefixMatchDoesNotOverreach(t *testing.T) {
	for _, p := range []string{"/oscar", "/apiary", "/administration", "/mcpherson"} {
		for _, pre := range evalRefusedPrefixes {
			if p == pre || strings.HasPrefix(p, pre+"/") {
				t.Errorf("%q was refused by prefix %q — a legitimate page broken by an "+
					"over-eager match", p, pre)
			}
		}
	}
}
