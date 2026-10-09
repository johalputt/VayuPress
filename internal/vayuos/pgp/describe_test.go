// SPDX-License-Identifier: Apache-2.0

package pgp

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

// armoredPublic is one armor block holding the public halves of ents.
func armoredPublic(t *testing.T, ents ...*openpgp.Entity) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if err := e.Serialize(w); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	return buf.Bytes()
}

func entity(t *testing.T, name, email string) *openpgp.Entity {
	t.Helper()
	e, err := openpgp.NewEntity(name, "", email, nil)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// A pasted key is read for its fingerprint and addresses; one seed per
// refusal.
func TestDescribePublicKey(t *testing.T) {
	ana := entity(t, "Ana", "Ana@Outside.test")
	fp, emails, err := DescribePublicKey(armoredPublic(t, ana))
	if err != nil || fp != fingerprintOf(ana) || len(emails) != 1 || emails[0] != "ana@outside.test" {
		t.Fatalf("ana: %q %v %v", fp, emails, err)
	}
	for name, c := range map[string]struct {
		armor []byte
		want  string
	}{
		"two keys in one block": {armoredPublic(t, ana, entity(t, "Ben", "ben@outside.test")), "one key at a time"},
		"two blocks":            {append(armoredPublic(t, ana), armoredPublic(t, entity(t, "Ben", "ben@outside.test"))...), "one key at a time"},
		"no address":            {armoredPublic(t, entity(t, "Nobody", "")), "names no address"},
		"not a key":             {[]byte("hello"), "not an OpenPGP public key"},
	} {
		if _, _, err := DescribePublicKey(c.armor); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}
