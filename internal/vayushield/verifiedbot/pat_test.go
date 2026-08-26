// SPDX-License-Identifier: Apache-2.0

package verifiedbot

import (
	"net/http"
	"testing"
	"time"
)

func TestAttestByPAT(t *testing.T) {
	now := time.Now()
	v := New(Config{
		Now: func() time.Time { return now },
		PATAttest: func(token string) (string, Class, bool) {
			if token == "good-token" {
				return "Bytespider", "", true
			}
			return "", "", false
		},
	})

	r, _ := http.NewRequest("GET", "https://x.test/", nil)
	r.Header.Set("Authorization", "Bearer good-token")
	got, vendor, _ := v.AttestByPAT(r)
	if got != Verified || vendor != "Bytespider" {
		t.Fatalf("bearer attestation wrong: %v %q", got, vendor)
	}

	r2, _ := http.NewRequest("GET", "https://x.test/", nil)
	r2.Header.Set("X-Bot-PAT", "good-token")
	if got2, _, _ := v.AttestByPAT(r2); got2 != Verified {
		t.Fatalf("X-Bot-PAT channel failed: %v", got2)
	}

	r3, _ := http.NewRequest("GET", "https://x.test/", nil) // no credential
	if got3, _, _ := v.AttestByPAT(r3); got3 != Unknown {
		t.Fatalf("credentialless request attested: %v", got3)
	}

	r4, _ := http.NewRequest("GET", "https://x.test/", nil)
	r4.Header.Set("X-Bot-PAT", "wrong-token")
	if got4, _, _ := v.AttestByPAT(r4); got4 != Unknown {
		t.Fatalf("bad token attested: %v", got4)
	}
}

func TestAttestByPATDisabledWithoutResolver(t *testing.T) {
	v := New(Config{})
	r, _ := http.NewRequest("GET", "https://x.test/", nil)
	r.Header.Set("Authorization", "Bearer anything")
	if got, _, _ := v.AttestByPAT(r); got != Unknown {
		t.Fatalf("channel off but attested: %v", got)
	}
}
