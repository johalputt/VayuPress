// SPDX-License-Identifier: Apache-2.0

package main

import (
	"regexp"
	"strings"
	"testing"
)

// FINDING (S8-F2) — the operator's console answered on every client's domain.
//
// setup-vayudomain.sh writes one reverse-proxy vhost per hosted domain, and it
// proxied "/" to the shared origin with nothing carved out. The origin serves
// ONE app on every host it answers for, so https://theirdomain/os rendered the
// install's sign-in page, /oauth/authorize its consent screen, /mcp its
// connector endpoint and /api/v1/admin its admin API.
//
// Authentication held throughout — none of it was reachable without a session.
// It is still the finding ADR-0152 rules out in as many words ("no client can
// reach the operator's controls"): a login form for the whole install, served
// on a hostname whose DNS a client controls, is where credential-stuffing and a
// convincing phishing page aim. The sibling setup-mcp-subdomain.sh had already
// narrowed exactly this on its own vhost.

// clientVhostRefusals is what a client's server block must refuse. Each entry
// needs BOTH forms — nginx matches locations by prefix, so "= /os" alone leaves
// /os/login answering and "^~ /os/" alone leaves the bare /os answering.
var clientVhostRefusals = []string{"/os", "/admin", "/oauth", "/mcp", "/api/v1/admin"}

// nginxLocationRe captures the modifier and the path of every location in a
// server block: `location ^~ /os/ {` → ("^~", "/os/").
var nginxLocationRe = regexp.MustCompile(`(?m)^\s*location\s+(=|\^~)?\s*(\S+)\s*\{`)

// clientVhostTemplate returns the nginx config write_full emits — the heredoc
// body itself, not the shell around it.
//
// shellFuncBody cannot be reused here: it ends at the first "\n}\n", and the
// heredoc's own first `server { … }` block closes exactly that way. It returned
// the HTTP-redirect half and nothing else, so every assertion below passed on a
// fragment that never contained the locations they name.
func clientVhostTemplate(t *testing.T) string {
	t.Helper()
	src := readSourceFile(t, "../../scripts/setup-vayudomain.sh")
	fn := strings.Index(src, "write_full() {")
	if fn < 0 {
		t.Fatal("write_full is gone from setup-vayudomain.sh — this test asserts against the " +
			"HTTPS vhost every hosted client domain is served from, and it no longer exists")
	}
	rest := src[fn:]
	open := strings.Index(rest, "<<NGINX\n")
	if open < 0 {
		t.Fatal("write_full no longer writes its vhost from a heredoc, so this test is reading " +
			"something other than the config that ships")
	}
	rest = rest[open+len("<<NGINX\n"):]
	end := strings.Index(rest, "\nNGINX\n")
	if end < 0 {
		t.Fatal("write_full's heredoc is unterminated")
	}
	tmpl := rest[:end]
	// A guard against reading a fragment again: the HTTPS server block is the
	// one every assertion here is about.
	if !strings.Contains(tmpl, "listen 443") || !strings.Contains(tmpl, "proxy_pass") {
		t.Fatalf("the extracted template is not the HTTPS reverse-proxy vhost:\n%s", tmpl)
	}
	return tmpl
}

// refusingLocations returns the set of prefixes the template answers with a 404.
func refusingLocations(tmpl string) map[string]string { // path → modifier
	out := map[string]string{}
	for _, m := range nginxLocationRe.FindAllStringSubmatchIndex(tmpl, -1) {
		var mod string
		if m[2] >= 0 {
			mod = tmpl[m[2]:m[3]]
		}
		path := tmpl[m[4]:m[5]]
		// The directive's body runs to the end of its line, which is where the
		// one-line `{ return 404; }` form this template uses lives.
		line := tmpl[m[1]:]
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		if strings.Contains(line, "return 404") {
			out[path] = mod
		}
	}
	return out
}

func TestAClientDomainDoesNotServeTheOperatorsControls(t *testing.T) {
	refused := refusingLocations(clientVhostTemplate(t))
	for _, p := range clientVhostRefusals {
		if mod, ok := refused[p]; !ok {
			t.Errorf("%s is not refused on a client's vhost, so the operator's own %s answers on "+
				"every hosted client domain", p, p)
		} else if mod != "=" {
			t.Errorf("%s is refused with modifier %q, not the exact match `=` — a bare prefix "+
				"here would take a client's own page down with it", p, mod)
		}
		if mod, ok := refused[p+"/"]; !ok {
			t.Errorf("%s/ is not refused, so only the bare %s is blocked and %s/anything still "+
				"answers — which is where the console actually lives", p, p, p)
		} else if mod != "^~" {
			t.Errorf("%s/ is refused with modifier %q; it must be `^~` so a later regex location "+
				"cannot take the request back", p+"/", mod)
		}
	}
}

// THE REGRESSION THIS EXISTS TO STOP. A bare `location /os` (no `=`, no trailing
// slash) is a prefix match, so it refuses /oscar, /ostrich and every client page
// that merely begins with those letters. Debugging must not break what already
// works, and a client's own site is the thing this must never cost them.
func TestTheRefusalNeverReachesAClientsOwnPages(t *testing.T) {
	refused := refusingLocations(clientVhostTemplate(t))
	for path, mod := range refused {
		if mod == "=" {
			continue // an exact match can only ever hit that one path
		}
		if mod != "^~" {
			t.Errorf("location %q uses modifier %q; only `=` and `^~` are used here and a regex "+
				"location refusing traffic would be invisible to the rest of this test", path, mod)
			continue
		}
		if !strings.HasSuffix(path, "/") {
			t.Errorf("location ^~ %q is a bare prefix, so it also refuses %qsomething — a client "+
				"page named like an operator path (/oscar for /os) 404s on their own domain",
				path, path)
		}
	}

	// Named cases, because the rule above is about shape and these are about the
	// paths a real bundle actually uses.
	for _, ok := range []string{"/oscar", "/administration", "/mcphersons-bakery", "/oauthorize"} {
		for path, mod := range refused {
			if mod == "^~" && strings.HasPrefix(ok, path) {
				t.Errorf("a client page at %s is refused by `location ^~ %s`", ok, path)
			}
			if mod == "=" && ok == path {
				t.Errorf("a client page at %s is refused by `location = %s`", ok, path)
			}
		}
	}
}

// /api MUST NOT be refused wholesale, and this is the trap: it reads as the
// obvious tightening and it would silently end analytics on every client site.
//
// The beacon posts to /api/v1/analytics/collect on the CURRENT origin — the
// client's own hostname — so a blanket /api refusal takes the site's own
// analytics with it while leaving every page loading normally. The rest of /api
// is public, member- or credential-gated and belongs to the site being served;
// only /api/v1/admin is an operator control.
func TestTheApiRefusalIsNarrowedToTheAdminHalf(t *testing.T) {
	refused := refusingLocations(clientVhostTemplate(t))
	for _, mustAnswer := range []string{
		"/api/v1/analytics/collect",
		"/api/v1/comments",
		"/api/v1/members/login",
	} {
		for path, mod := range refused {
			if (mod == "^~" && strings.HasPrefix(mustAnswer, path)) || (mod == "=" && mustAnswer == path) {
				t.Errorf("%s is refused on a client's vhost by `location %s %s`. That path serves "+
					"the client's OWN site — refusing /api wholesale is the tightening that looks "+
					"right and switches off analytics everywhere", mustAnswer, mod, path)
			}
		}
	}
}

// The two layers must agree. The nginx block is the outer refusal; the CSP
// middleware's evalRefusedPrefixes is the inner one, and it is the list that
// survives if the vhost is ever regenerated by hand. Anything the edge treats as
// an operator control must also be a path the relaxed policy never reaches —
// otherwise the two disagree about what an operator surface is, and the weaker
// answer is the one that ships.
func TestTheEdgeAndTheMiddlewareAgreeOnWhatIsAnOperatorPath(t *testing.T) {
	for _, p := range clientVhostRefusals {
		if !evalRefusedPath(p) {
			t.Errorf("nginx refuses %s on a client domain but evalRefusedPath does not, so the "+
				"same path would still be granted 'unsafe-eval' if it were ever reachable", p)
		}
	}
}
