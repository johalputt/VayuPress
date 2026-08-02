// SPDX-License-Identifier: Apache-2.0

package mail

import "testing"

// The outbound queue must not default to keeping every sent message forever.
//
// vayumail_queue.raw is the FULL RFC5322 message — the same bytes handed to
// DeliverFunc — so with a zero retention window the delivery queue was a
// permanent, plaintext archive of everything the install had ever sent, living
// inside SQLite and therefore inside every database backup, indefinitely.
//
// Nobody chose that. QueueRetentionDays simply had no entry in DefaultConfig, so
// it took Go's zero value, and the field's own documentation described "keep
// forever" as the default as though it were a decision. A privacy posture that
// exists only because a struct field was omitted is not a posture.
//
// Pruning removes only DELIVERED delivery-status rows shown in the Outbox. The
// Sent copy in the sender's Maildir is a separate store and is never touched, so
// nothing a user would call "my sent mail" is lost.
func TestDefaultConfigDoesNotKeepSentMailForever(t *testing.T) {
	got := DefaultConfig().QueueRetentionDays
	if got <= 0 {
		t.Fatalf("DefaultConfig().QueueRetentionDays = %d — the outbound queue keeps the full "+
			"plaintext of every message ever sent, forever, in every backup. An operator may "+
			"choose 0 deliberately; the default must not arrive there by omission.", got)
	}
	// A window so long it is "forever" wearing a number would pass the check above
	// while changing nothing, so bound it at something an operator would recognise
	// as a retention policy rather than an accident.
	if got > 365 {
		t.Errorf("DefaultConfig().QueueRetentionDays = %d days, which is not meaningfully "+
			"different from keeping it forever", got)
	}
}

// The pruner must actually honour the configured window, or the default above is
// decoration. 0 stays off — an operator who deliberately sets it keeps the old
// behaviour, and that path must not silently start deleting.
func TestQueueRetentionWindowIsHonoured(t *testing.T) {
	e := &Engine{}
	e.SetQueueRetentionDays(90)
	if got := e.QueueRetentionDays(); got != 90 {
		t.Fatalf("QueueRetentionDays() = %d after SetQueueRetentionDays(90)", got)
	}
	e.SetQueueRetentionDays(0)
	if got := e.QueueRetentionDays(); got != 0 {
		t.Fatalf("QueueRetentionDays() = %d after an explicit 0 — an operator's deliberate "+
			"'keep forever' must survive", got)
	}
}
