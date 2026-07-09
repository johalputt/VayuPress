package main

import (
	"net/http/httptest"
	"testing"
)

func TestSanitizeMailLocalPart(t *testing.T) {
	ok := []string{"alice", "bob.smith", "a_b+c-d", "User123", ""}
	for _, s := range ok {
		if got := sanitizeMailLocalPart(s); got != s {
			t.Fatalf("sanitizeMailLocalPart(%q) = %q, want unchanged", s, got)
		}
	}
	bad := []string{
		`<script>`,
		`a"onmouseover=alert(1)`,
		`a b`,
		`a/b`,
		`x@y`,
		`"><img src=x onerror=alert(1)>`,
	}
	for _, s := range bad {
		if got := sanitizeMailLocalPart(s); got != "" {
			t.Fatalf("sanitizeMailLocalPart(%q) = %q, want \"\" (rejected)", s, got)
		}
	}
	// Over-length is rejected.
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'a'
	}
	if got := sanitizeMailLocalPart(string(long)); got != "" {
		t.Fatal("over-length local part must be rejected")
	}
}

func TestMailUserParamRejectsInjection(t *testing.T) {
	r := httptest.NewRequest("GET", `/os/vayumail/inbox?user=%22%3E%3Cscript%3Ealert(1)%3C/script%3E`, nil)
	if got := mailUserParam(r); got != "" {
		t.Fatalf("mailUserParam must reject an XSS payload, got %q", got)
	}
	r2 := httptest.NewRequest("GET", `/os/vayumail/inbox?user=alice`, nil)
	if got := mailUserParam(r2); got != "alice" {
		t.Fatalf("mailUserParam(alice) = %q, want alice", got)
	}
}

func TestMailFolderParamDefaultsAndRejects(t *testing.T) {
	// Valid custom folder passes.
	r := httptest.NewRequest("GET", `/os/vayumail/inbox?folder=Project_X-2`, nil)
	if got := mailFolderParam(r); got != "Project_X-2" {
		t.Fatalf("folder = %q, want Project_X-2", got)
	}
	// Missing → Inbox.
	if got := mailFolderParam(httptest.NewRequest("GET", `/os/vayumail/inbox`, nil)); got != "Inbox" {
		t.Fatalf("missing folder = %q, want Inbox", got)
	}
	// Markup → Inbox (rejected to default).
	r2 := httptest.NewRequest("GET", `/os/vayumail/inbox?folder=%3Cimg%20src%3Dx%3E`, nil)
	if got := mailFolderParam(r2); got != "Inbox" {
		t.Fatalf("markup folder = %q, want Inbox", got)
	}
}
