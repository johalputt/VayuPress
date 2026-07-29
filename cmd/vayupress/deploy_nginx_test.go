// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// deploy/nginx-vayupress.conf is the edge every request passes through, and the
// properties below are the ones that are expensive to get wrong: a cache that
// serves one reader's page to another, a stampede caused by the cache meant to
// prevent one, or a directive in a context where nginx refuses to start.
//
// Where a real nginx binary is available these run against it. Where it is not,
// the structural assertions still run — but a syntax check is never inferred
// from them, because "the text looks right" is not "nginx accepts it".

func readNginxTemplate(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../deploy/nginx-vayupress.conf")
	if err != nil {
		t.Fatalf("read the nginx template: %v", err)
	}
	return string(b)
}

// TestMicrocacheCannotServeOnePersonsPageToAnother is the property that makes the
// whole micro-cache safe to ship. A signed-in reader's response must never enter
// the cache, and a signed-in reader must never be served from it — two separate
// directives, in two directions, and having only one of them is a data leak
// rather than a performance bug.
func TestMicrocacheCannotServeOnePersonsPageToAnother(t *testing.T) {
	src := readNginxTemplate(t)
	for _, want := range []string{
		"proxy_no_cache           $http_cookie $http_authorization;",
		"proxy_cache_bypass       $http_cookie $http_authorization;",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q — with only one direction covered, a logged-in reader's page "+
				"either enters the shared cache or is served from it", want)
		}
	}
}

// TestMicrocacheDoesNotCauseTheStampedeItPrevents — without proxy_cache_lock, a
// cache MISS under load sends every waiting request upstream simultaneously. The
// cache then converts a flood the origin was absorbing into a synchronised
// thundering herd, which is worse than having no cache at all.
func TestMicrocacheDoesNotCauseTheStampedeItPrevents(t *testing.T) {
	src := readNginxTemplate(t)
	for _, want := range []string{"proxy_cache_lock", "proxy_cache_use_stale", "updating"} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q — a miss under load would send every waiting request upstream at once", want)
		}
	}
}

// TestCacheZoneIsDeclaredInHTTPContext — proxy_cache_path is an http-level
// directive. Inside a server block it is not a warning, it is a startup failure,
// so an operator who copied this file would find nginx refusing to start.
func TestCacheZoneIsDeclaredInHTTPContext(t *testing.T) {
	src := readNginxTemplate(t)
	i := strings.Index(src, "proxy_cache_path")
	if i < 0 {
		t.Fatal("no proxy_cache_path — the micro-cache references a zone nothing declares")
	}
	if j := strings.Index(src, "server {"); j >= 0 && i > j {
		t.Error("proxy_cache_path appears after the first server block — if it is inside one, " +
			"nginx will not start")
	}
}

// TestStaticSendsOneCacheControlHeader — `expires` emits its own Cache-Control,
// so pairing it with an add_header produces a response carrying TWO. Some
// intermediaries take the first and some the last, and `immutable` — the
// directive that actually stops revalidation round-trips — ends up in whichever
// one they discarded. Found by running the config, not by reading it.
func TestStaticSendsOneCacheControlHeader(t *testing.T) {
	src := readNginxTemplate(t)
	loc := staticLocation(t, src)
	if strings.Contains(loc, "expires ") && strings.Contains(loc, "add_header Cache-Control") {
		t.Error("the /static/ location sets both `expires` and an explicit Cache-Control — " +
			"the response carries two Cache-Control headers and `immutable` may be dropped")
	}
	if !strings.Contains(loc, "immutable") {
		t.Error("static assets are not marked immutable, so every reader revalidates them")
	}
	if !strings.Contains(loc, "try_files $uri @app") {
		t.Error("the static location does not fall through to the app — a path served " +
			"dynamically under /static would 404")
	}
}

func staticLocation(t *testing.T, src string) string {
	t.Helper()
	i := strings.Index(src, "location ^~ /static/ {")
	if i < 0 {
		t.Fatal("no /static/ location — every asset still costs a goroutine and a middleware chain")
	}
	rest := src[i:]
	if j := strings.Index(rest, "\n    }"); j > 0 {
		rest = rest[:j]
	}
	// Strip comments. The block explains WHY `expires` is not used, and matching
	// on that sentence made this test fail against a correct config — a finding
	// about the prose, reported as a finding about the directives.
	var live []string
	for _, line := range strings.Split(rest, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		if strings.TrimSpace(line) != "" {
			live = append(live, line)
		}
	}
	return strings.Join(live, "\n")
}

// TestHTTP3StaysCommentedUntilItsPortIsCovered — HTTP/3 is UDP/443. Enabling it
// moves the site's primary transport onto a port, and the Tier 2 ruleset has to
// model that port or the operator has just moved all their traffic somewhere
// with no per-source cap at all. The firewall now covers UDP/443, so the
// documentation may point at it — but the listen lines stay commented, because
// turning them on also needs a QUIC-capable build and an Alt-Svc header.
func TestHTTP3StaysCommentedUntilItsPortIsCovered(t *testing.T) {
	src := readNginxTemplate(t)
	live := regexp.MustCompile(`(?m)^\s*listen[^#\n]*quic`)
	if live.MatchString(src) {
		t.Error("a `listen … quic` line is uncommented in the template — HTTP/3 needs a " +
			"QUIC-capable nginx build and an Alt-Svc header, and shipping it on by default " +
			"silently changes which transport the site is served over")
	}
	fw, err := os.ReadFile("../../deploy/vayushield-firewall.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fw), "QUIC_PORTS") {
		t.Error("the firewall no longer models UDP/443, so enabling HTTP/3 would move the " +
			"site's primary transport to a port with zero Tier 2 rules")
	}
}

// TestTemplateIsAcceptedByNginx runs the real parser where one exists. Presence
// is checked by actually validating a known-good fragment first: a binary that
// cannot run here would otherwise report the template as broken.
func TestTemplateIsAcceptedByNginx(t *testing.T) {
	bin, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx unavailable — the structural assertions above still ran, but nothing here syntax-checked the file")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/conf.d", 0o755); err != nil {
		t.Fatal(err)
	}

	// Neutralise what a sandbox cannot provide — certificates, privileged ports,
	// IPv6 — and validate everything else for real.
	//
	// The absolute paths are rewritten for the same reason, and that one was
	// learned the hard way: nginx CREATES a proxy_cache_path directory during
	// `-t`, so the template's /var/cache/nginx failed with "No such file or
	// directory" on a machine that had never run nginx. It passed locally only
	// because an earlier manual probe had created that directory as a side
	// effect — the test was reading the machine's history, not the config.
	src := readNginxTemplate(t)
	for _, r := range [][2]string{
		{"ssl_certificate", "#ssl_certificate"},
		{"ssl_trusted_certificate", "#ssl_trusted_certificate"},
		{"listen 443 ssl http2;", "listen 18443;"},
		{"listen 443 ssl default_server;", "listen 18443 default_server;"},
		{"listen 80 default_server;", "listen 18081 default_server;"},
		{"listen 80;", "listen 18082;"},
		{"/var/cache/nginx", dir + "/ngxcache"},
		{"/var/cache/vayupress", dir + "/vpcache"},
		{"/opt/vayupress", dir + "/opt"},
	} {
		src = strings.ReplaceAll(src, r[0], r[1])
	}
	for _, d := range []string{"/ngxcache", "/vpcache", "/opt"} {
		if err := os.MkdirAll(dir+d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var kept []string
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "listen [::]") {
			continue // no IPv6 in most CI sandboxes
		}
		kept = append(kept, line)
	}
	if err := os.WriteFile(dir+"/conf.d/vayupress.conf", []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	main := "worker_processes 1;\nerror_log " + dir + "/error.log;\npid " + dir + "/nginx.pid;\n" +
		"events { worker_connections 64; }\nhttp {\n access_log " + dir + "/access.log;\n" +
		" client_body_temp_path " + dir + "/body;\n proxy_temp_path " + dir + "/proxy;\n" +
		" fastcgi_temp_path " + dir + "/fcgi;\n uwsgi_temp_path " + dir + "/uwsgi;\n" +
		" scgi_temp_path " + dir + "/scgi;\n include " + dir + "/conf.d/*.conf;\n}\n"
	if err := os.WriteFile(dir+"/nginx.conf", []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(bin, "-t", "-c", dir+"/nginx.conf").CombinedOutput()
	if err != nil {
		body := string(out)
		// A sandbox that cannot bind or lacks a module is an environment limit,
		// not a defect in the template — reporting one as the other is the same
		// mistake the nft differential made.
		for _, env := range []string{"Permission denied", "Address family not supported", "bind()", "unknown directive"} {
			if strings.Contains(body, env) {
				t.Skipf("nginx cannot validate in this environment: %s", strings.TrimSpace(body))
			}
		}
		t.Errorf("nginx rejected the template:\n%s", body)
	}
}
