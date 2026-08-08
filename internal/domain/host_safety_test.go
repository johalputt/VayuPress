// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"strings"
	"testing"
)

// Everything the live install actually hosts must keep working. A validator that
// closes a hole and takes someone's domain with it is its own outage, and this
// list is the twelve hosts on the operator's own server plus the forms the
// product creates for itself.
func TestValidateHostAcceptsEveryHostTheProductLegitimatelyCreates(t *testing.T) {
	for _, h := range []string{
		"johal.in", "haru.johal.in", "vayupress.com", "vayucell.vayupress.com",
		"vayuweb.vayupress.com", "test2.johal.in",
		"xn--mnchen-3ya.de",   // an internationalised domain, encoded
		"EXAMPLE.COM",         // casing is NormalizeHost's business, not a refusal
		"a.b.c.d.e.f.example", // deep but ordinary
		// A v3 onion address: 56 base32 characters. SetHost rewrites a domain to
		// one of these when a Tor Space is minted, so refusing it would break the
		// feature that function exists for.
		strings.Repeat("a", 56) + ".onion",
	} {
		if err := ValidateHost(h); err != nil {
			t.Errorf("ValidateHost(%q) = %v; this host is legitimate and would stop being "+
				"provisioned", h, err)
		}
	}
}

// The non-ASCII case says what to do, not only what is wrong. Such a name never
// worked — DNS, nginx and certbot all want the encoded form — so the refusal
// replaces a domain that would have failed later with nothing to read.
func TestAnInternationalisedDomainIsToldWhatToRegisterInstead(t *testing.T) {
	err := ValidateHost("münchen.de")
	if err == nil {
		t.Fatal("a display-form internationalised domain was accepted; it reaches an nginx " +
			"server_name that cannot match it")
	}
	if !strings.Contains(err.Error(), "punycode") {
		t.Errorf("the refusal does not name punycode, so the operator is told their domain "+
			"is invalid and not that there is a spelling of it that works: %v", err)
	}
}
