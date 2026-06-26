package mail

import (
	"strings"
	"testing"
)

// With no connecting IP, no envelope sender, and no DKIM signature, every check
// degrades to "none" and the Authentication-Results header is well-formed.
func TestVerifyInboundNoneResults(t *testing.T) {
	t.Parallel()
	raw := []byte(crlf("From: someone@nodomain.invalid\nSubject: hi\n\nbody\n"))
	v := verifyInbound("mail.test", nil, "", "", raw)
	if v.SPF != "none" {
		t.Errorf("spf = %q, want none", v.SPF)
	}
	if v.DKIM != "none" {
		t.Errorf("dkim = %q, want none", v.DKIM)
	}
	for _, want := range []string{"mail.test", "spf=none", "dkim=none", "dmarc="} {
		if !strings.Contains(v.Header, want) {
			t.Errorf("header %q missing %q", v.Header, want)
		}
	}
	if !strings.HasPrefix(v.authResultsHeader(), "Authentication-Results: ") {
		t.Errorf("bad AR header line: %q", v.authResultsHeader())
	}
}

// A message without any DKIM-Signature must report dkim=none (not fail).
func TestVerifyInboundNoDKIM(t *testing.T) {
	t.Parallel()
	raw := []byte(crlf("From: a@b.example\n\nno signature here\n"))
	if v := verifyInbound("h", nil, "", "", raw); v.DKIM != "none" {
		t.Errorf("dkim = %q, want none", v.DKIM)
	}
}

func TestDomainsAligned(t *testing.T) {
	t.Parallel()
	// relaxed: exact or subdomain; strict: exact only.
	if !domainsAligned("example.com", "example.com", "r") {
		t.Error("exact match should align (relaxed)")
	}
	if !domainsAligned("mail.example.com", "example.com", "r") {
		t.Error("subdomain should align under relaxed")
	}
	if domainsAligned("mail.example.com", "example.com", "s") {
		t.Error("subdomain must NOT align under strict")
	}
	if domainsAligned("evil.test", "example.com", "r") {
		t.Error("unrelated domains must not align")
	}
}

// The junk filter must force a DMARC-quarantine-flagged message to Junk and
// leave a clean message below the threshold.
func TestScoreSpamDMARCQuarantine(t *testing.T) {
	t.Parallel()
	quarantined := []byte(crlf("From: spoof@bank.example\n" +
		"X-VayuMail-Auth-Quarantine: yes\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 -0700\n\nhello\n"))
	if v := ScoreSpam(quarantined); !v.IsSpam {
		t.Errorf("DMARC-quarantine message should be flagged spam, score=%d", v.Score)
	}
	clean := []byte(crlf("From: friend@example.com\n" +
		"Authentication-Results: mail.test; spf=pass; dkim=pass; dmarc=pass\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 -0700\n\nhello there\n"))
	if v := ScoreSpam(clean); v.IsSpam {
		t.Errorf("clean authenticated message must not be spam, score=%d reasons=%v", v.Score, v.Reasons)
	}
}
