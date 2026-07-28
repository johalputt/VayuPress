// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"strings"
	"testing"
)

// The nginx templates in deploy/ are copied verbatim onto operator machines, and
// a broken one is expensive in a way a broken Go file is not: `nginx -t` fails,
// so the next `systemctl reload nginx` does nothing, and the operator is left on
// a stale config with no obvious signal. Certbot renewals reload too, which is
// how a bad template turns into an expired certificate weeks later.
//
// These tests cannot run `nginx -t` (nginx is not a build dependency), so they
// pin the properties that actually break installs.

func readDeployConf(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../deploy/" + name)
	if err != nil {
		t.Fatalf("read deploy/%s: %v", name, err)
	}
	return string(b)
}

// activeLines returns only the directives nginx will actually parse — comments
// stripped — which is the whole point: the optional blocks are documentation
// until an operator deliberately enables them.
func activeLines(conf string) []string {
	var out []string
	for _, ln := range strings.Split(conf, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		out = append(out, t)
	}
	return out
}

func TestNginxTemplateBracesBalance(t *testing.T) {
	for _, name := range []string{"nginx-vayupress.conf", "nginx-vayushield.conf", "nginx-vayumail.conf"} {
		conf := readDeployConf(t, name)
		depth := 0
		for i, ln := range activeLines(conf) {
			depth += strings.Count(ln, "{") - strings.Count(ln, "}")
			if depth < 0 {
				t.Fatalf("%s: closing brace with nothing open, at active line %d: %q", name, i+1, ln)
			}
		}
		if depth != 0 {
			t.Errorf("%s: %d unclosed block(s) — nginx -t would reject this", name, depth)
		}
	}
}

// TestOptionalDirectivesStayCommented is the important one. Brotli needs a module
// that is not installed by default and HTTP/3 needs nginx >= 1.25 built with the
// v3 module. An unknown directive is a HARD failure of `nginx -t`, not a warning,
// so shipping either one active would break every stock install that copies this
// file — the opposite of what a template is for.
func TestOptionalDirectivesStayCommented(t *testing.T) {
	conf := readDeployConf(t, "nginx-vayupress.conf")
	// Directives whose NAME leads the line. `http2 on` is separate from the
	// `listen … http2` parameter: the standalone directive only exists on
	// nginx >= 1.25.1, where the parameter form still works everywhere.
	gatedNames := []string{"brotli", "http2 on", "http3"}
	for _, ln := range activeLines(conf) {
		low := strings.ToLower(ln)
		for _, g := range gatedNames {
			if strings.HasPrefix(low, g) {
				t.Errorf("%q is active but needs a module or nginx version the template cannot assume; "+
					"keep it commented with enable instructions", ln)
			}
		}
		// QUIC is a listen PARAMETER, so it never leads the line — an earlier
		// version of this test checked only prefixes and let `listen 443 quic`
		// through, which is exactly the line that breaks a pre-1.25 nginx.
		if strings.HasPrefix(low, "listen") && strings.Contains(low, "quic") {
			t.Errorf("%q is active but needs nginx >= 1.25 with the v3 module; keep it commented", ln)
		}
		// Advertising h3 without a QUIC listener is worse than not advertising
		// it: clients attempt HTTP/3, fail, and pay a round trip falling back.
		if strings.Contains(low, "alt-svc") && strings.Contains(low, "h3") {
			t.Errorf("%q advertises HTTP/3 while the QUIC listener is commented out — "+
				"clients would try h3 and have to fall back", ln)
		}
	}
	// ...and they must still be PRESENT as documentation, or the operator who
	// goes gray has no path to the compression and protocol they just lost.
	for _, want := range []string{"brotli_comp_level", "listen 443 quic", "Alt-Svc"} {
		if !strings.Contains(conf, want) {
			t.Errorf("template no longer documents %q — a self-hosted install has no way to recover it", want)
		}
	}
}

// TestNoOCSPStaplingDirective — Let's Encrypt retired its OCSP responders, so
// their certificates carry no responder URL and nginx warns on every config test
// and every reload. The warning is harmless; training operators to scroll past
// nginx warnings is not.
func TestNoOCSPStaplingDirective(t *testing.T) {
	for _, ln := range activeLines(readDeployConf(t, "nginx-vayupress.conf")) {
		if strings.HasPrefix(ln, "ssl_stapling") {
			t.Errorf("%q is active, but Let's Encrypt certificates have no OCSP responder URL — "+
				"this warns on every nginx -t", ln)
		}
	}
}

// TestGzipCoversTextTypes guards the floor. gzip needs no module, so unlike
// Brotli it must always be on and must cover the types VayuPress actually
// serves — an uncompressed JSON feed or SVG is pure wasted bandwidth.
func TestGzipCoversTextTypes(t *testing.T) {
	conf := readDeployConf(t, "nginx-vayupress.conf")
	active := strings.Join(activeLines(conf), "\n")
	if !strings.Contains(active, "gzip on;") {
		t.Fatal("gzip must be active — it is the only compression a stock nginx guarantees")
	}
	for _, ct := range []string{
		"text/css", "application/javascript", "application/json",
		"image/svg+xml", "application/manifest+json",
	} {
		if !strings.Contains(active, ct) {
			t.Errorf("gzip_types does not cover %s", ct)
		}
	}
}
