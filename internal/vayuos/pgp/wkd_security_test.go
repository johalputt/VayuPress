package pgp

import "testing"

// TestValidExternalWKDDomain locks in the SSRF guard: only public DNS hostnames
// are accepted; IP literals, localhost, numeric TLDs and junk are rejected.
func TestValidExternalWKDDomain(t *testing.T) {
	t.Parallel()
	ok := []string{"example.com", "mail.johal.in", "sub.domain.co.uk"}
	bad := []string{
		"", "localhost", "127.0.0.1", "169.254.169.254", "::1",
		"10.0.0.5", "example", "evil.com/path", "host:8080",
		"http://example.com", "1.2.3.4", "a..b.com", "exam ple.com",
	}
	for _, d := range ok {
		if !validExternalWKDDomain(d) {
			t.Errorf("expected %q to be accepted", d)
		}
	}
	for _, d := range bad {
		if validExternalWKDDomain(d) {
			t.Errorf("expected %q to be rejected", d)
		}
	}
}
