// SPDX-License-Identifier: Apache-2.0

package main

// nginx_config_health_test.go — the panel must name a config collision.
//
// The condition these tests describe was live on a real install for three days,
// visible only as a warn-level line in nginx's error log:
//
//	[warn] conflicting server name "mcp.example" on 0.0.0.0:443, ignored
//
// A hostname declared by two files means nginx keeps one server block and
// discards the other, chosen by glob order. Which site an operator is actually
// serving is not a detail that belongs in a log nobody opens.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureSites builds a sites-enabled directory. Entries whose value is "" are
// symlinks to a generated target; others are regular files with that content.
func fixtureSites(t *testing.T, files map[string]string, links map[string]string) string {
	t.Helper()
	base := t.TempDir()
	dir := filepath.Join(base, "sites-enabled")
	avail := filepath.Join(base, "sites-available")
	for _, d := range []string{dir, avail} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range links {
		target := filepath.Join(avail, name)
		if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const vhostMCP = `
server { listen 80; server_name mcp.example; return 301 https://$host$request_uri; }
server { listen 443 ssl; server_name mcp.example; location / { return 404; } }
`

// THE test, and it is the live incident reproduced exactly: a real vhost as a
// symlink, plus a backup of it as a regular file in the same directory.
func TestTheLiveIncidentIsReportedAsBothAStrayAndACollision(t *testing.T) {
	dir := fixtureSites(t,
		map[string]string{"vayupress-mcp.vayushield.bak": vhostMCP},
		map[string]string{"vayupress-mcp": vhostMCP})

	h := inspectNginxSitesEnabled(dir)
	if !h.Checked {
		t.Fatalf("directory was not read: %s", h.Reason)
	}
	if h.OK() {
		t.Fatal("a backup file being served as live configuration was reported as healthy")
	}

	if len(h.Strays) != 1 || h.Strays[0] != "vayupress-mcp.vayushield.bak" {
		t.Errorf("strays = %v, want exactly the backup file. nginx includes this directory with a "+
			"bare * glob, so a backup here is a second live server block.", h.Strays)
	}
	if len(h.Duplicates) != 1 {
		t.Fatalf("duplicates = %v, want one (mcp.example, declared by two files)", h.Duplicates)
	}
	d := h.Duplicates[0]
	if d.Host != "mcp.example" {
		t.Errorf("duplicate host = %q, want mcp.example", d.Host)
	}
	// Naming BOTH files is the whole value: an operator has to know which two
	// are colliding to know which one they lost.
	if len(d.Files) != 2 {
		t.Errorf("the collision names %v; it must name both files or an operator cannot act on it",
			d.Files)
	}
}

// THE false-positive test, and the one most likely to break this feature.
//
// A normal vhost declares its hostname TWICE — once in the :80 block, once in
// :443. Counting occurrences rather than distinct files would flag every
// correctly-configured site on every install, and a card that cries wolf is one
// nobody reads.
func TestANormalVhostIsNotACollision(t *testing.T) {
	dir := fixtureSites(t, nil, map[string]string{
		"johal": vhostMCP,
		"other": "\nserver { listen 80; server_name other.example; }\nserver { listen 443 ssl; server_name other.example; }\n",
	})
	h := inspectNginxSitesEnabled(dir)
	if !h.OK() {
		t.Errorf("a healthy install was reported as faulty: strays=%v duplicates=%v. A hostname "+
			"appears twice in a normal vhost (:80 and :443) and only a CROSS-FILE collision matters.",
			h.Strays, h.Duplicates)
	}
}

// Multiple names on one directive, and www aliases, must not confuse it.
func TestMultipleNamesOnOneDirectiveAreHandled(t *testing.T) {
	dir := fixtureSites(t, nil, map[string]string{
		"a": "server { server_name a.example www.a.example; }",
		"b": "server { server_name b.example www.a.example; }",
	})
	h := inspectNginxSitesEnabled(dir)
	if len(h.Duplicates) != 1 || h.Duplicates[0].Host != "www.a.example" {
		t.Errorf("duplicates = %v, want www.a.example — it is declared by both files and only one "+
			"of the two will be served", h.Duplicates)
	}
}

// The catch-all is not a hostname. Every install has one and it is meant to be
// shared.
func TestTheCatchAllIsNotReportedAsACollision(t *testing.T) {
	dir := fixtureSites(t, nil, map[string]string{
		"catchall": "server { listen 80 default_server; server_name _; }",
		"other":    "server { listen 443 default_server; server_name _; }",
	})
	h := inspectNginxSitesEnabled(dir)
	if len(h.Duplicates) != 0 {
		t.Errorf("the catch-all `server_name _` was reported as a collision: %v", h.Duplicates)
	}
}

// A symlink is how a vhost is enabled. It is never a stray, whatever it is named.
func TestASymlinkIsNeverAStray(t *testing.T) {
	dir := fixtureSites(t, nil, map[string]string{
		"deliberate.bak": "server { server_name kept.example; }",
	})
	h := inspectNginxSitesEnabled(dir)
	if len(h.Strays) != 0 {
		t.Errorf("a symlink named .bak was reported as a stray: %v. Enabling a vhost IS symlinking, "+
			"so this is an operator's choice however oddly named.", h.Strays)
	}
}

// A regular file with an ordinary name is not a stray either — some operators
// keep a real vhost in sites-enabled rather than symlinking one.
func TestAHandWrittenVhostIsNotAStray(t *testing.T) {
	dir := fixtureSites(t, map[string]string{
		"handwritten": "server { server_name hand.example; }",
	}, nil)
	h := inspectNginxSitesEnabled(dir)
	if len(h.Strays) != 0 {
		t.Errorf("a hand-written vhost was reported as a stray: %v", h.Strays)
	}
	if !h.OK() {
		t.Error("a hand-written vhost made the install read as faulty")
	}
}

// THE honesty test. An unreadable directory must not read as a clean bill of
// health — that is a zero meaning "no data" rendered as a zero meaning "no
// problems", which is the defect class this whole card exists to close.
func TestAnUnreadableDirectoryIsNotReportedAsHealthy(t *testing.T) {
	h := inspectNginxSitesEnabled(filepath.Join(t.TempDir(), "does-not-exist"))
	if h.Checked {
		t.Error("a missing directory was reported as successfully checked")
	}
	if h.OK() {
		t.Error("a directory that could not be read reported OK. A panel that cannot see the " +
			"configuration must say so, not imply it is clean.")
	}
	if h.Reason == "" {
		t.Error("no reason given for the failed check, so an operator cannot tell a container " +
			"layout from a permissions problem")
	}
}

// ── The card ─────────────────────────────────────────────────────────────────

// A healthy install renders nothing. A permanent green panel for a fault almost
// nobody has is noise on every page view, and noise is what made the original
// warning invisible.
func TestTheCardIsSilentOnAHealthyInstall(t *testing.T) {
	dir := fixtureSites(t, nil, map[string]string{"a": "server { server_name a.example; }"})
	if got := nginxConfigHealthCard(inspectNginxSitesEnabled(dir)); got != "" {
		t.Errorf("a healthy install rendered a card:\n%s", got)
	}
}

// THE card test. Mid-fault it must name the hostname, both files, and what the
// consequence is — "one of these is not serving anything".
func TestTheCardNamesTheHostnameAndBothFiles(t *testing.T) {
	dir := fixtureSites(t,
		map[string]string{"vayupress-mcp.vayushield.bak": vhostMCP},
		map[string]string{"vayupress-mcp": vhostMCP})
	card := nginxConfigHealthCard(inspectNginxSitesEnabled(dir))
	if card == "" {
		t.Fatal("the live incident rendered no card at all")
	}
	for _, want := range []string{
		"mcp.example",                  // the hostname
		"vayupress-mcp",                // the real vhost
		"vayupress-mcp.vayushield.bak", // the stray
		"discards the other",           // the consequence, stated
	} {
		if !strings.Contains(card, want) {
			t.Errorf("the card omits %q:\n%s", want, card)
		}
	}
}

// An unreadable directory must say so rather than rendering nothing — silence
// would be indistinguishable from a clean install, which is the exact defect
// class this card exists to close.
func TestTheCardSaysWhenItCouldNotLook(t *testing.T) {
	card := nginxConfigHealthCard(inspectNginxSitesEnabled(filepath.Join(t.TempDir(), "nope")))
	if card == "" {
		t.Fatal("a directory that could not be read rendered nothing, which reads as healthy")
	}
	if !strings.Contains(card, "Not checked") {
		t.Errorf("the card does not say the check did not run:\n%s", card)
	}
	if !strings.Contains(card, "not that the configuration is clean") {
		t.Error("the card does not distinguish 'not checked' from 'nothing wrong'")
	}
}

// A hostile filename or hostname must render as text.
func TestTheCardEscapesHostileNames(t *testing.T) {
	dir := fixtureSites(t, map[string]string{
		`evil<img onerror=alert(1)>.bak`: `server { server_name "x<script>y"; }`,
	}, nil)
	card := nginxConfigHealthCard(inspectNginxSitesEnabled(dir))
	if strings.Contains(card, "<img onerror") {
		t.Errorf("a filename reached the page as markup:\n%s", card)
	}
	if strings.Contains(card, "<scr"+"ipt>") {
		t.Errorf("a server_name reached the page as markup:\n%s", card)
	}
}

// House style and CSP.
func TestTheNginxConfigCardIsCSPSafe(t *testing.T) {
	dir := fixtureSites(t,
		map[string]string{"vayupress-mcp.vayushield.bak": vhostMCP},
		map[string]string{"vayupress-mcp": vhostMCP})
	assertCSPSafe(t, "nginx config health card", nginxConfigHealthCard(inspectNginxSitesEnabled(dir)))
}

// The card must be reachable, not merely defined. This is the wiring, which is
// what regresses.
func TestTheDomainsPageRendersTheConfigCard(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("admin_os_domains.go"))
	if err != nil {
		t.Skipf("not readable: %v", err)
	}
	if !strings.Contains(string(b), "nginxConfigHealthCard(") {
		t.Error("the Sites page does not render the nginx configuration card. A check nobody can " +
			"see is the same as no check — the original fault was already being reported, in a log.")
	}
}

// AUDIT FINDING. This runs on every Sites page view and reads whatever is in
// the directory — so it must not read an unbounded amount into memory.
func TestAnEnormousFileIsReadOnlyUpToTheCap(t *testing.T) {
	dir := fixtureSites(t, nil, nil)
	huge := make([]byte, maxVhostBytes+(2<<20))
	for i := range huge {
		huge[i] = ' '
	}
	// A real directive at the very start, so a correct capped read still finds
	// it and the test cannot pass merely by reading nothing.
	copy(huge, []byte("server { server_name big.example; }"))
	if err := os.WriteFile(filepath.Join(dir, "huge"), huge, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other"), []byte("server { server_name big.example; }"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := inspectNginxSitesEnabled(dir)
	if len(h.Duplicates) != 1 {
		t.Errorf("the capped read lost a real directive: duplicates=%v", h.Duplicates)
	}
	got, err := readCappedFile(filepath.Join(dir, "huge"), maxVhostBytes)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(got)) > maxVhostBytes {
		t.Errorf("read %d bytes with a %d cap; a page view must not pull an arbitrary file into "+
			"memory", len(got), maxVhostBytes)
	}
}
