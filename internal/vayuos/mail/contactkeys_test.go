// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"strings"
	"testing"
)

// keyBridge reads a "key" as the comma-separated addresses it names.
type keyBridge struct{ loopbackBridge }

func (keyBridge) DescribePublicKey(armored []byte) (string, []string, error) {
	return "FP-" + string(armored), strings.Split(string(armored), ","), nil
}

// A mailbox keeps keys up to its bound, and a key replaces the one held for
// its address rather than counting twice.
func TestTheKeyListIsBounded(t *testing.T) {
	e := newLoopbackEngine(t, keyBridge{})
	old := maxContactKeysPerMailbox
	maxContactKeysPerMailbox = 2
	t.Cleanup(func() { maxContactKeysPerMailbox = old })
	rd := ReadAsOwner("alice")
	for _, k := range []string{"a@x.test", "b@x.test"} {
		if _, err := e.AddContactKey(rd, []byte(k)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.AddContactKey(rd, []byte("c@x.test")); err == nil || !strings.Contains(err.Error(), "remove one") {
		t.Fatalf("past the bound: %v", err)
	}
	if _, err := e.AddContactKey(rd, []byte("a@x.test")); err != nil {
		t.Fatalf("replacing a held key at the bound: %v", err)
	}
}
