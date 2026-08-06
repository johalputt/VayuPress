// SPDX-License-Identifier: Apache-2.0

package main

// nginx_config_health.go — reading what nginx is actually serving from.
//
// # Why this exists
//
// An install returned 502 during every certificate provisioning run for three
// days. The whole time, nginx had been logging this on every reload:
//
//	[warn] conflicting server name "mcp.example" on 0.0.0.0:443, ignored
//
// A backup file had been written into /etc/nginx/sites-enabled/, which nginx
// includes with a bare `*` glob and no extension filter, so the backup was a
// second live server block. nginx resolved the collision by keeping whichever
// the glob reached first and discarding the other — and said so once, at warn
// level, in a file nobody opens.
//
// The standing rule this violates: diagnostics belong on the page. "Check your
// nginx error log" is the same product failure as "run this command" — the
// console should already be showing it. A duplicate hostname decides which of
// two server blocks serves a site, and that is not a detail an operator should
// have to go looking for.
//
// # What it does and does not claim
//
// It reads the directory and reports what is there. It does NOT parse nginx's
// full grammar, resolve includes, or predict which block wins — that depends on
// glob ordering and listen addresses, and a panel that guessed would be worse
// than one that stays quiet. It reports two facts that are unambiguous on their
// own: a hostname declared in more than one file, and a backup file sitting
// where configuration is read from.

import (
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// htmlEscape is the local alias used throughout this file.
func htmlEscape(s string) string { return html.EscapeString(s) }

// The directory nginx includes is already declared as `nginxSitesEnabled` in
// admin_os_scoped_diagnose.go, which reads it for the per-host certificate
// diagnostic. This file is the install-wide view of the same directory, so it
// shares that variable rather than keeping a second copy that can drift.

// strayConfigSuffixes are names that are backups by universal convention.
//
// Deliberately narrow. A regular file in sites-enabled is unusual but not
// automatically wrong — some operators keep a real vhost there rather than a
// symlink into sites-available — and reporting that as a fault would train an
// operator to ignore this card.
var strayConfigSuffixes = []string{".bak", ".save", ".orig", ".dpkg-old", ".dpkg-dist", "~"}

// serverNameLine matches a server_name directive and captures its arguments.
//
// NOT anchored to the start of a line. The first version was, and it silently
// missed `server { listen 80; server_name x; }` written on one line — which is
// perfectly valid nginx and is exactly how this product's own generated vhosts
// are formatted in places. A check that only sees tidily-formatted config would
// have reported the incident that prompted it as clean.
//
// The leading class prevents matching a directive that merely ENDS in
// "server_name" (there is no such directive today, but a prefix match is the
// kind of thing that silently starts being wrong).
var serverNameLine = regexp.MustCompile(`(?:^|[;{}\s])server_name\s+([^;{}]+);`)

// NginxDuplicateHost is one hostname declared by more than one file.
type NginxDuplicateHost struct {
	Host  string
	Files []string // sorted, relative names
}

// NginxConfigHealth is what the panel renders.
type NginxConfigHealth struct {
	Dir     string
	Checked bool   // false when the directory could not be read at all
	Reason  string // why not, when Checked is false

	// Strays are backup files sitting inside the include path. Each one is a
	// live server block that nobody meant to deploy.
	Strays []string
	// Duplicates are hostnames declared in more than one FILE. Within a single
	// file a hostname legitimately appears twice — once for the :80 block and
	// once for :443 — so only a cross-file collision is reported.
	Duplicates []NginxDuplicateHost
}

// maxVhostBytes caps how much of any one file is parsed. A generated vhost in
// this product is under 4 KB and a hand-written one is rarely past 20.
const maxVhostBytes = 512 * 1024

// readCappedFile reads at most n bytes. A server_name past half a megabyte into
// a file is not a server_name anybody meant to declare.
func readCappedFile(path string, n int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- the operator's own nginx directory
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, n))
}

// OK reports whether the configuration is free of both conditions.
func (h NginxConfigHealth) OK() bool {
	return h.Checked && len(h.Strays) == 0 && len(h.Duplicates) == 0
}

// isStrayConfigName reports whether a filename is a backup by convention.
func isStrayConfigName(name string) bool {
	for _, s := range strayConfigSuffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	// `.bak` in the middle, as vayushield-agent.sh once wrote
	// (`vayupress-mcp.vayushield.bak` ends in .bak and is caught above, but
	// `foo.bak.2` would not be).
	return strings.Contains(name, ".bak.")
}

// inspectNginxSitesEnabled reads dir and reports what nginx would load from it.
//
// It never returns an error: a panel that disappears when it cannot read
// something is less useful than one that says it could not read it.
func inspectNginxSitesEnabled(dir string) NginxConfigHealth {
	h := NginxConfigHealth{Dir: dir}
	ents, err := os.ReadDir(dir)
	if err != nil {
		// Common and not alarming: a container image, a non-Debian layout, or a
		// service user without read access. Say which rather than implying the
		// configuration is clean.
		h.Reason = "could not read " + dir + ": " + err.Error()
		return h
	}
	h.Checked = true

	byHost := map[string]map[string]bool{}
	for _, e := range ents {
		name := e.Name()
		info, err := e.Info()
		if err != nil {
			continue
		}
		// A symlink is how a vhost is enabled — an operator's deliberate act,
		// whatever it is called. Only regular files can be accidental.
		isSymlink := info.Mode()&os.ModeSymlink != 0
		if !isSymlink && !info.IsDir() && isStrayConfigName(name) {
			h.Strays = append(h.Strays, name)
		}
		if info.IsDir() {
			continue
		}
		// BOUNDED. This runs on every Sites page view and reads whatever is in
		// the directory — including through a symlink, exactly as nginx does. A
		// vhost is a few kilobytes; anything past the cap is not one, and
		// reading it whole into memory on a page view would be the second
		// finding of this release's audit rather than a fix from it.
		b, err := readCappedFile(filepath.Join(dir, name), maxVhostBytes)
		if err != nil {
			continue
		}
		for _, m := range serverNameLine.FindAllStringSubmatch(string(b), -1) {
			for _, host := range strings.Fields(m[1]) {
				host = strings.TrimSpace(host)
				if host == "" || host == "_" {
					continue // the catch-all is not a hostname
				}
				if byHost[host] == nil {
					byHost[host] = map[string]bool{}
				}
				byHost[host][name] = true
			}
		}
	}

	for host, files := range byHost {
		if len(files) < 2 {
			continue
		}
		var fs []string
		for f := range files {
			fs = append(fs, f)
		}
		sort.Strings(fs)
		h.Duplicates = append(h.Duplicates, NginxDuplicateHost{Host: host, Files: fs})
	}
	sort.Slice(h.Duplicates, func(i, j int) bool { return h.Duplicates[i].Host < h.Duplicates[j].Host })
	sort.Strings(h.Strays)
	return h
}

// nginxConfigHealthCard renders the install-wide nginx configuration state.
//
// It renders NOTHING on a healthy install. This card exists to surface a fault
// that otherwise lives only in a warn-level log line, and a permanent green
// panel for a condition almost nobody has would be noise on every other page
// view. Silence here means the directory was read and both checks passed —
// except when it could not be read, which is stated rather than implied.
func nginxConfigHealthCard(h NginxConfigHealth) string {
	if h.OK() {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="section-head"><div class="section-head__title">Web server configuration</div>` +
		`<div class="section-head__hint">nginx loads every file in its sites directory, so what is in there ` +
		`decides which server block answers for a hostname.</div></div>`)

	if !h.Checked {
		b.WriteString(`<div class="card mb-6"><div class="settings-block-title">Not checked</div>` +
			`<p class="text-sm muted">` + htmlEscape(h.Reason) + `. This is normal on a container or ` +
			`non-Debian layout. It means these checks did not run — not that the configuration is clean.</p></div>`)
		return b.String()
	}

	if len(h.Strays) > 0 {
		b.WriteString(`<div class="card mb-6"><div class="settings-block-title">A backup file is being served as configuration</div>`)
		b.WriteString(`<p class="text-sm muted">nginx includes this directory with a plain <code>*</code> ` +
			`glob and does not skip backup extensions, so each file below is a live server block that ` +
			`nobody deployed on purpose. VayuShield moves these out automatically on its next pass; ` +
			`they are listed here so the change is visible rather than silent.</p><ul class="text-sm muted">`)
		for _, s := range h.Strays {
			b.WriteString(`<li><code>` + htmlEscape(filepath.Join(h.Dir, s)) + `</code></li>`)
		}
		b.WriteString(`</ul></div>`)
	}

	if len(h.Duplicates) > 0 {
		b.WriteString(`<div class="card mb-6"><div class="settings-block-title">A hostname is declared more than once</div>`)
		b.WriteString(`<p class="text-sm muted">Two files claim the same hostname. nginx keeps whichever ` +
			`it loads first and discards the other, choosing by filename order — so one of these two ` +
			`server blocks is not serving anything, and which one is not obvious from either file.</p>`)
		b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>Hostname</th>` +
			`<th>Declared by</th></tr></thead><tbody>`)
		for _, d := range h.Duplicates {
			b.WriteString(`<tr><td class="row-title">` + htmlEscape(d.Host) + `</td><td class="muted text-sm">`)
			for i, f := range d.Files {
				if i > 0 {
					b.WriteString(` &middot; `)
				}
				b.WriteString(`<code>` + htmlEscape(f) + `</code>`)
			}
			b.WriteString(`</td></tr>`)
		}
		b.WriteString(`</tbody></table></div></div>`)
	}
	return b.String()
}
