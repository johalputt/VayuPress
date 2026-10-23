// SPDX-License-Identifier: Apache-2.0

package mail

import "testing"

// The host hears of each message filed, whichever way it is filed, so the
// console's bell can show new mail the moment it lands.
func TestTheHostIsToldOfEveryFiledMessage(t *testing.T) {
	md := NewMaildir(t.TempDir())
	n := 0
	md.onDeliver = func() { n++ }
	if _, err := md.Deliver("example.com", "priya", []byte("Subject: hi\r\n\r\nhello")); err != nil {
		t.Fatal(err)
	}
	if _, err := md.DeliverTo("example.com", "priya", "Junk", []byte("Subject: junk\r\n\r\nbuy")); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("the host was told of %d deliveries, want 2", n)
	}
	// No host listening is not a failure.
	md.onDeliver = nil
	if _, err := md.Deliver("example.com", "priya", []byte("Subject: again\r\n\r\nhello")); err != nil {
		t.Fatal(err)
	}
}
