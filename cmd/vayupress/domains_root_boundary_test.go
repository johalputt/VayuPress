// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
)

// SECTION 6 AUDIT — the one place a panel-controlled STRING crosses into root.
//
// The provisioning request itself is a model of how to do this: an empty file,
// deliberately, guarded by a test that fails if anything ever writes a byte into
// it. Root reads existence and nothing else. That channel is sound.
//
// But triggering it starts a root worker that reads this binary's own domain
// registry — `vayupress domains hosts` — and interpolates each hostname into
// files it writes as root:
//
//	scripts/setup-vayudomain.sh:251  cat > "${AVAIL_DIR}/vayupress-dom-$1" <<NGINX
//	scripts/setup-vayudomain.sh:254      server_name $1 www.$1;
//
// In the attacker's voice, holding nothing but the ability to register a domain:
//
//	Your request file carries no payload, and I do not need one. I put my
//	payload in the registry and let your root worker fetch it. nginx directives
//	end at a semicolon, not a newline, so a host of
//	`mine.example; deny all; #` writes `deny all;` into a server block that
//	runs as root — and the filename takes my text too, so `../..` puts the file
//	wherever I like.
//
// TestAHostileHostnameWouldReachRootUnfiltered below drives the REAL function
// out of the shipped script and proves both halves of that.
//
// WHAT IS AND IS NOT TRUE HERE, because overstating it would be its own defect:
// this is NOT exploitable today. `resolves()` — `getent hosts "$1"` — refuses
// every hostile form, so the worker skips them before writing anything. That is
// the entire protection, and it is an accident: that line was written to ask
// "has DNS been pointed here yet?", and it answers "is this safe to interpolate
// into root-owned configuration?" only as a side effect. Nothing states the
// second job, no test covers it, and a reasonable refactor — checking DNS after
// writing the HTTP-only vhost, say, which is the order certbot actually wants —
// removes it without anyone noticing what was lost.
//
// So the control goes where the boundary is.

// THE BOUNDARY. Every hostname root ever sees comes through this one printer:
//
//	cmd/vayupress/domains_cli.go:104  fmt.Fprintln(out, d.Host)
//
// Five helper scripts consume it. Filtering here covers all of them, covers rows
// written by an older version that had no validation, and covers rows written
// straight into SQLite — none of which a check on the HTTP handler would reach.
func TestDomainsHostsNeverEmitsAHostRootCannotSafelyInterpolate(t *testing.T) {
	setupSEOTestDB(t)
	ctx := context.Background()
	reg := domain.New(dbpkg.DB, dbpkg.RDB)
	if err := reg.EnsurePrimary(ctx, "example.test", domain.SiteBlog); err != nil {
		t.Fatalf("ensure primary: %v", err)
	}

	// Written straight to the table, bypassing every Go-side guard — because
	// that is the state this check exists for. A row can predate validation, or
	// arrive from a restored backup, and the worker would read it just the same.
	// Each entry is rejected by exactly one rule wherever possible. A seed that
	// trips several at once cannot kill a mutation of any single one — the first
	// version of this list had that flaw, and two mutations survived it: the
	// semicolon case also carried spaces, and the over-long case was one
	// over-long LABEL, so neither the character rule nor the total-length rule
	// was ever the deciding check.
	hostile := []string{
		"mine.example;deny",                  // ONLY a semicolon: ends the directive
		"mine.example withspace",             // ONLY a space
		"../../../etc/nginx/conf.d/own.conf", // path separators: escapes sites-available
		"a.example\nserver_name b.example",   // newline into the config body
		"me.example{deny}",                   // braces: a block, not a directive
		"me.example`id`",                     // backticks: inert here, not everywhere
		// Every label legal and short; only the TOTAL length is out of bounds.
		strings.TrimSuffix(strings.Repeat(strings.Repeat("x", 60)+".", 5), ".") + ".example",
		strings.Repeat("y", 64) + ".example", // ONLY the per-label ceiling
	}
	for i, h := range hostile {
		if _, err := dbpkg.DB.ExecContext(ctx,
			`INSERT INTO domains(id,host,site_type,mail_enabled,tls_state,sync_state,config_json,is_primary,status)
			 VALUES(?,?,?,0,?,?,'',0,?)`,
			"hostile-"+string(rune('a'+i)), h, domain.SiteBlog, domain.TLSPending, domain.SyncApproved, domain.StatusActive,
		); err != nil {
			t.Fatalf("seed %q: %v", h, err)
		}
	}

	// One row that MUST survive. A filter that emitted nothing would satisfy
	// every assertion below while taking certificates away from every domain on
	// the install — a control that rations real people is its own outage.
	if _, err := reg.Create(ctx, "shop.example", domain.SiteBlog, false); err != nil {
		t.Fatalf("create the legitimate secondary: %v", err)
	}
	if err := reg.SetSyncState(ctx, mustDomainID(t, ctx, reg, "shop.example"), domain.SyncApproved); err != nil {
		t.Fatalf("approve: %v", err)
	}

	var b strings.Builder
	if err := runDomainsCLI([]string{"hosts", "--all"}, &b); err != nil {
		t.Fatalf("hosts --all: %v", err)
	}
	got := b.String()

	// The assertions are deliberately INDEPENDENT of ValidateHost.
	//
	// The first version of this test called ValidateHost on each emitted line,
	// and two mutations survived because of it: loosening the validator loosened
	// the assertion in the same edit, so the test was comparing the code with
	// itself. What is unsafe has to be stated here, separately, or this proves
	// nothing at all.
	for _, h := range hostile {
		if strings.Contains(got, strings.SplitN(h, "\n", 2)[0]) {
			t.Errorf("`domains hosts` emitted the hostile row %q.\n\n"+
				"Root interpolates this into an nginx directive and into the name of a file it\n"+
				"writes as root. The registry is not a trusted source — a row can predate\n"+
				"validation, come from a restored backup, or be written straight into SQLite —\n"+
				"so this printer is the boundary.\n\noutput:\n%s", h, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 253 {
			t.Errorf("`domains hosts` emitted a %d-byte name; nothing downstream can hold it", len(line))
		}
		for _, r := range line {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			default:
				t.Errorf("`domains hosts` emitted %q, containing %q — root writes this into an\n"+
					"nginx directive and a filename, unquoted.", line, r)
			}
		}
	}
	if !strings.Contains(got, "shop.example") {
		t.Errorf("the legitimate secondary was withheld too.\n\nRefusing every host would pass "+
			"the checks above and silently stop certificate renewal for the whole install.\n\noutput:\n%s", got)
	}
}

func mustDomainID(t *testing.T, ctx context.Context, reg *domain.Registry, host string) string {
	t.Helper()
	list, err := reg.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, d := range list {
		if d.Host == host {
			return d.ID
		}
	}
	t.Fatalf("no registry row for %s", host)
	return ""
}

// THE CONSEQUENCE, driven through the real code rather than described.
//
// This runs the actual write_http_only out of scripts/setup-vayudomain.sh. It
// exists so the check above is anchored to something true about the shipped
// worker: if that function is ever changed to quote its heredoc or to write the
// hostname through a variable that cannot escape, this test says so and the
// justification above can be revisited.
func TestAHostileHostnameWouldReachRootUnfiltered(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "scripts", "setup-vayudomain.sh"))
	if err != nil {
		t.Fatalf("read the provisioning helper: %v", err)
	}

	// Reuses the heredoc-aware extractor already in this package; a second copy
	// would be a future divergence, and the first naive version of that helper had
	// exactly this bug — it stopped at the `}` inside the template.
	fn := extractShellFunc(string(src), "write_http_only")
	if fn == "" {
		t.Fatal("scripts/setup-vayudomain.sh no longer defines write_http_only()")
	}
	dir := t.TempDir()
	script := fn + "\nwrite_http_only 'mine.example; deny all; #'\n"

	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "AVAIL_DIR="+dir, "CACHE_DIR=/var/cache/vayupress")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run write_http_only: %v\n%s", err, out)
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one config file, got %v (%v)", entries, err)
	}
	name := entries[0].Name()
	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if !strings.Contains(string(body), "deny all;") {
		t.Errorf("the shipped worker no longer carries an unquoted hostname into its nginx\n"+
			"config — good, and the reasoning in this file should be revisited.\n\nconfig:\n%s", body)
	}
	if !strings.Contains(name, ";") {
		t.Errorf("the shipped worker no longer carries an unquoted hostname into the config\n"+
			"FILENAME — good, and the reasoning in this file should be revisited.\n\nname: %s", name)
	}
}
