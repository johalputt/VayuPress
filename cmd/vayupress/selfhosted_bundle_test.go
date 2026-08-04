// SPDX-License-Identifier: Apache-2.0

package main

// selfhosted_bundle_test.go — the marketing site, checked against the policy it
// will actually be served under.
//
// WHY, and it is a specific failure rather than a principle. The build script
// rewrote index.html and then "verified" the bundle by re-reading index.html for
// surviving off-origin subresources. It passed. The bundle shipped with
// about.html, vayuweb.html, sponsors/index.html and vayumail/privacy/index.html
// still loading the CDN framework and the font host, which a hosted install
// refuses — four of six pages rendered as unstyled text, on a site the operator
// had been told was a faithful copy. A check that reads only the file you have
// already repaired can only ever tell you that you repaired it.
//
// So this test builds the bundle from the real docs/site/ with the real script,
// then reads EVERY file it produced and holds each one against the real CSP
// from internal/render. Nothing here is asserted from the script's source text.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/render"
)

func buildSelfhostedBundle(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available in this environment")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "bundle")
	cmd := exec.Command("bash", "scripts/build-selfhosted-site.sh", out)
	cmd.Dir = repo
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the bundle does not build, so nobody can produce the site at all:\n%s", b)
	}
	return out
}

func bundlePages(t *testing.T, root string) map[string]string {
	t.Helper()
	pages := map[string]string{}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, p)
		pages[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 2 {
		t.Fatalf("only %d page(s) in the bundle — the site has several, and shipping one of them "+
			"as \"the site\" is how the broken pages went unnoticed", len(pages))
	}
	return pages
}

// The policy every one of these pages will be served under. Read from the real
// builder rather than restated here, so tightening the policy fails this test
// instead of silently invalidating it.
func hostedCSP(t *testing.T) string {
	t.Helper()
	csp := render.BuildCSP("test-nonce", nil)
	for _, must := range []string{"script-src", "style-src", "font-src"} {
		if !strings.Contains(csp, must) {
			t.Fatalf("the baseline policy no longer constrains %s; this test is checking the wrong thing:\n%s", must, csp)
		}
	}
	return csp
}

var (
	reOffOrigin = regexp.MustCompile(`<(?:script|link)[^>]*(?:src|href)="https?://[^"]+"`)
	reStyleTag  = regexp.MustCompile(`(?s)<style[^>]*>`)
	// Go's regexp is RE2 and has no lookahead, so "a script tag with no src" is
	// matched broadly here and narrowed in code below.
	reAnyScript = regexp.MustCompile(`(?s)<script([^>]*)>(.*?)</script>`)
	reHasSrc    = regexp.MustCompile(`\bsrc\s*=`)
)

// Every page, not one page. This is the whole point of the file.
func TestEveryPageInTheBundleSurvivesTheHostedPolicy(t *testing.T) {
	root := buildSelfhostedBundle(t)
	csp := hostedCSP(t)
	pages := bundlePages(t, root)
	t.Logf("checking %d pages against: %s", len(pages), csp)

	for name, s := range pages {
		// font-src 'self', style-src 'self', script-src 'self' — a subresource from
		// anywhere else is refused, and the page renders as unstyled text with its
		// controls dead. Anchors and canonical links are untouched: the browser
		// does not fetch them to build the page.
		for _, m := range reOffOrigin.FindAllString(s, -1) {
			if strings.Contains(m, `rel="canonical"`) || strings.Contains(m, `rel="alternate"`) {
				continue
			}
			t.Errorf("%s loads a subresource from another origin, which this policy refuses:\n    %s\n"+
				"    The page will render unstyled and the operator will not be told why.", name, m)
		}

		// style-src 'self' does NOT include 'unsafe-inline' — only style-src-attr
		// does. A <style> element is therefore dropped in full, and every rule in
		// it is silently lost.
		if loc := reStyleTag.FindString(s); loc != "" {
			t.Errorf("%s still carries a <style> block. style-src is 'self', so the browser "+
				"discards it and the rules never apply.", name)
		}

		// script-src is 'self' plus a per-request nonce. A static bundle cannot
		// carry a nonce, so an inline block never runs.
		for _, m := range reAnyScript.FindAllStringSubmatch(s, -1) {
			if reHasSrc.MatchString(m[1]) {
				continue // an external script; 'self' covers it
			}
			if strings.Contains(m[1], "ld+json") || strings.Contains(m[1], "application/json") {
				continue // data, not script; the browser never executes it
			}
			if strings.TrimSpace(m[2]) != "" {
				t.Errorf("%s still carries an inline <script>. script-src requires a per-request "+
					"nonce that a static bundle cannot have, so this code never runs.", name)
			}
		}
	}
}

// Every first-party file the pages ask for has to exist, or the page is broken in
// a way no CSP check would notice — a 404 stylesheet looks exactly like a
// refused one.
func TestEverySubresourceTheBundleAsksForIsActuallyPresent(t *testing.T) {
	root := buildSelfhostedBundle(t)
	repo, _ := filepath.Abs("../..")
	pages := bundlePages(t, root)

	ref := regexp.MustCompile(`<(?:script|link)[^>]*(?:src|href)="(/?[^":?#][^":?#]*)"`)
	checked := 0
	for name, s := range pages {
		for _, m := range ref.FindAllStringSubmatch(s, -1) {
			p := m[1]
			if strings.HasPrefix(p, "http") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "data:") {
				continue
			}
			var full string
			switch {
			case strings.HasPrefix(p, "/static/"):
				// Served by the binary out of its own static tree, not the bundle.
				full = filepath.Join(repo, p)
			case strings.HasPrefix(p, "/"):
				full = filepath.Join(root, p)
			default:
				full = filepath.Join(root, filepath.Dir(name), p)
			}
			checked++
			if _, err := os.Stat(full); err != nil {
				t.Errorf("%s references %q, which does not exist (%v). A missing stylesheet looks "+
					"identical to a blocked one — the page is just wrong, with no error anywhere.",
					name, p, err)
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d subresources checked; the extraction is not finding them", checked)
	}
	t.Logf("verified %d subresource references across %d pages", checked, len(pages))
}

// The typefaces the pages ask for must be ones the binary will actually serve.
// font-src is 'self', so a face missing from the allowlist is not a fallback —
// it is a refused request and a different typeface on screen.
func TestEveryFontTheBundleAsksForIsOneTheBinaryServes(t *testing.T) {
	root := buildSelfhostedBundle(t)
	css, err := os.ReadFile(filepath.Join(root, "assets", "fonts.css"))
	if err != nil {
		t.Fatal(err)
	}
	want := regexp.MustCompile(`/static/fonts/([a-z0-9-]+\.woff2)`).FindAllStringSubmatch(string(css), -1)
	if len(want) == 0 {
		t.Fatal("fonts.css names no faces, so the page falls back to a system font everywhere")
	}
	for _, m := range want {
		if !fontAllowlist[m[1]] {
			t.Errorf("the bundle asks for %q, which is not in the binary's font allowlist — the "+
				"request is refused and the text renders in a different typeface", m[1])
		}
	}
	t.Logf("verified %d faces against the allowlist", len(want))
}

// The two files the GitHub Pages deploy bakes and the bundle did not.
//
// The page reads assets/stars.json and assets/version.json first — same-origin,
// instant — and only then refreshes from the GitHub API. Without them the bundle
// produced two 404s and a fallback fetch to api.github.com that `connect-src
// 'self'` refuses outright, so the star count and the version simply never
// appeared. No error on screen, no missing layout: two numbers that are part of
// the design were blank, which is not something anyone reports as a bug.
func TestTheBundleCarriesTheFilesThePageReadsBeforeItAsksTheNetwork(t *testing.T) {
	root := buildSelfhostedBundle(t)
	for _, f := range []string{"assets/stars.json", "assets/version.json"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Errorf("%s is missing: the page requests it, gets a 404, falls back to an external "+
				"API this policy refuses, and shows nothing where a number belongs (%v)", f, err)
			continue
		}
		var v map[string]any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Errorf("%s is not valid JSON, so the page's parse throws and the fallback runs anyway: %v", f, err)
			continue
		}
		// The null form is legitimate — a build with no network still has to produce
		// a working bundle — but the KEY must be present, because that is what the
		// page reads.
		key := "stargazers_count"
		if strings.Contains(f, "version") {
			key = "tag_name"
		}
		if _, ok := v[key]; !ok {
			t.Errorf("%s does not carry %q, which is the field the page reads", f, key)
		}
	}
}
