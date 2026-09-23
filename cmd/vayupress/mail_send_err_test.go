// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"strings"
	"testing"
)

// TestASendFailureNamesWhoseProblemItIs holds the send-error sentences to the
// person they are about. One seed per rule, each carrying only its own trigger
// word, so a rule can be broken without another one catching the seed instead.
func TestASendFailureNamesWhoseProblemItIs(t *testing.T) {
	for _, c := range []struct {
		name, err, want, mustNot string
	}{
		// The case that was wrong: a colleague's full mailbox, reported to the
		// sender as their own.
		{"a local recipient is full",
			"vayumail: local delivery to bob@example.com: vayumail: mailbox bob@example.com is over its storage quota",
			"bob@example.com is full", "Your mailbox"},
		{"the sender is full", "vayumail: quota exceeded", "Your mailbox is full", ""},
		{"no mail server", "dial tcp: lookup mx.example: no such host", "recipient’s mail server", ""},
		{"no recipients", "vayumail: no recipients", "Add at least one recipient", ""},
		{"unrecognised", "vayumail: something new", "has not been delivered", "something new"},
	} {
		got := mailSendErrText(errors.New(c.err))
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: %q does not say %q", c.name, got, c.want)
		}
		if c.mustNot != "" && strings.Contains(got, c.mustNot) {
			t.Errorf("%s: %q says %q", c.name, got, c.mustNot)
		}
	}
}
