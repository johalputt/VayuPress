// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// TestSanitizeMailUser pins the widened mailbox-key sanitizer that fixes opening a
// secondary domain's mailbox: a bare local-part OR a full local@domain is
// accepted, and everything unsafe (extra "@", empty side, "+"/"_" in the domain,
// or any HTML/JS metacharacter) is rejected to "".
func TestSanitizeMailUser(t *testing.T) {
	good := []string{"admin", "john.doe+tag-1", "hello@vayupress.com", "a_b.c@sub.example.co", "A.B@Ex-ample.COM"}
	for _, s := range good {
		if got := sanitizeMailUser(s); got != s {
			t.Errorf("sanitizeMailUser(%q) = %q, want unchanged", s, got)
		}
	}
	bad := []string{
		"", "@x.com", "x@", "a@b@c", "hi there@x.com", "<script>", "a@b/c",
		"user@dom+ain", "user@dom_ain", strings.Repeat("a", 65),
		// malformed domains (defense-in-depth beyond the Maildir's safeSegment)
		"x@..", "x@.a", "x@a.", "x@a..b", "x@-a.com",
	}
	for _, s := range bad {
		if got := sanitizeMailUser(s); got != "" {
			t.Errorf("sanitizeMailUser(%q) = %q, want empty (rejected)", s, got)
		}
	}
}

// TestEmailDomain covers the Outbox From-domain extraction used by the per-domain
// filter: real domain lower-cased, and a bare/relative From falling back.
func TestEmailDomain(t *testing.T) {
	cases := map[string]string{
		"hello@vayupress.com": "vayupress.com",
		"admin@EXAMPLE.TEST":  "example.test",
		"postmaster":          "example.test", // no domain → fallback
		"weird@":              "example.test", // trailing @ → fallback
	}
	for in, want := range cases {
		if got := emailDomain(in, "example.test"); got != want {
			t.Errorf("emailDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestVayuMailboxDomainSection pins the per-domain mailbox directory: a
// secondary domain's rows link the FULL address (so the read path opens its
// own Maildir) and its section says it is the secondary; the primary keeps a
// bare local-part link. Neither carries a filled badge (rule 4).
func TestVayuMailboxDomainSection(t *testing.T) {
	a := &App{}
	sec := string(a.vayuMailboxDomainSection("vayupress.com", "example.test",
		[]vmail.MailboxSummary{{Username: "hello", Domain: "vayupress.com", Total: 3, Unseen: 1}}, false))
	for _, want := range []string{"vayupress.com", "Secondary · 1 mailbox · 1 unseen", "hello@vayupress.com", "user=hello%40vayupress.com"} {
		if !strings.Contains(sec, want) {
			t.Errorf("secondary section missing %q\n%s", want, sec)
		}
	}
	prim := string(a.vayuMailboxDomainSection("example.test", "example.test",
		[]vmail.MailboxSummary{{Username: "admin", Domain: "example.test", Total: 2}}, true))
	if !strings.Contains(prim, `user=admin"`) {
		t.Errorf("primary section should link the bare local part:\n%s", prim)
	}
	if !strings.Contains(prim, "Primary · 1 mailbox<") {
		t.Errorf("primary section does not say it is the primary, or counts a zero:\n%s", prim)
	}
	for _, html := range []string{sec, prim} {
		if strings.Contains(html, `class="badge`) || strings.Contains(html, "vm-tab-badge") {
			t.Errorf("a filled badge in the directory:\n%s", html)
		}
	}
}

// TestOutboxChip pins the per-domain Outbox filter chip: active state + the
// server-side HTMX domain param.
func TestOutboxChip(t *testing.T) {
	on := outboxChip("vayupress.com", "vayupress.com", true)
	if !strings.Contains(on, "vm-fchip--on") || !strings.Contains(on, "domain=vayupress.com") {
		t.Errorf("active chip wrong: %s", on)
	}
	if off := outboxChip("All domains", "all", false); strings.Contains(off, "vm-fchip--on") {
		t.Errorf("inactive chip should not be --on: %s", off)
	}
}
