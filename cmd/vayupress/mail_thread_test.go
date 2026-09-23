// SPDX-License-Identifier: Apache-2.0

package main

// mail_thread_test.go — conversation grouping.
//
// Grouping by subject alone merged unrelated mail that happened to share a
// subject ("Invoice") and failed to join a conversation whose subject changed.
// The rule is now evidence-first: References/In-Reply-To decide, and the subject
// is only the fallback for mail that carries no ids at all.

import (
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// TestAReplyJoinsItsParentEvenWhenTheSubjectChanged is the case References exist
// for: "Re: Invoice 2026" must land with "Invoice".
func TestAReplyJoinsItsParentEvenWhenTheSubjectChanged(t *testing.T) {
	root := vmail.StoredMessage{ID: "1", MessageID: "root@x", Subject: "Invoice 2026"}
	reply := vmail.StoredMessage{ID: "2", MessageID: "r1@x", InReplyTo: "root@x", Subject: "Re: Invoice 2026"}
	key := mailThreadKeyer([]vmail.StoredMessage{root, reply})
	if key(root) != key(reply) {
		t.Errorf("a reply with In-Reply-To must join its parent: %q vs %q", key(root), key(reply))
	}
}

// TestAReplyToAReplyStillReachesTheRoot covers the chain.
func TestAReplyToAReplyStillReachesTheRoot(t *testing.T) {
	root := vmail.StoredMessage{ID: "1", MessageID: "root@x", Subject: "Trip"}
	mid := vmail.StoredMessage{ID: "2", MessageID: "mid@x", InReplyTo: "root@x", Subject: "Re: Trip"}
	deep := vmail.StoredMessage{ID: "3", MessageID: "deep@x", InReplyTo: "mid@x", Subject: "Re: Trip"}
	key := mailThreadKeyer([]vmail.StoredMessage{root, mid, deep})
	if key(root) != key(deep) {
		t.Errorf("a reply to a reply must resolve to the original thread: %q vs %q", key(root), key(deep))
	}
}

// TestUnrelatedMailSharingASubjectIsNotMerged is the bug subjects could not see.
func TestUnrelatedMailSharingASubjectIsNotMerged(t *testing.T) {
	a := vmail.StoredMessage{ID: "1", MessageID: "a@x", Subject: "Invoice"}
	b := vmail.StoredMessage{ID: "2", MessageID: "b@x", Subject: "Invoice"}
	key := mailThreadKeyer([]vmail.StoredMessage{a, b})
	if key(a) == key(b) {
		t.Errorf("two unrelated messages that merely share a subject must not be merged (both keyed %q)", key(a))
	}
}

// TestRepliesToAnAbsentParentGroupTogether — the parent has scrolled out of the
// window (or lives in another folder), so the two siblings must still group.
func TestRepliesToAnAbsentParentGroupTogether(t *testing.T) {
	r1 := vmail.StoredMessage{ID: "1", MessageID: "r1@x", InReplyTo: "gone@x", Subject: "Re: A"}
	r2 := vmail.StoredMessage{ID: "2", MessageID: "r2@x", InReplyTo: "gone@x", Subject: "Re: A"}
	key := mailThreadKeyer([]vmail.StoredMessage{r1, r2})
	if key(r1) != key(r2) {
		t.Errorf("siblings of an absent parent should group together: %q vs %q", key(r1), key(r2))
	}
}

// TestReferencesAreUsedWhenInReplyToIsMissing — some senders set only References.
func TestReferencesAreUsedWhenInReplyToIsMissing(t *testing.T) {
	root := vmail.StoredMessage{ID: "1", MessageID: "root@x", Subject: "Plan"}
	reply := vmail.StoredMessage{ID: "2", MessageID: "r@x", Subject: "Plan", References: []string{"root@x", "other@x"}}
	key := mailThreadKeyer([]vmail.StoredMessage{root, reply})
	if key(root) != key(reply) {
		t.Errorf("References must be used when In-Reply-To is absent: %q vs %q", key(root), key(reply))
	}
}

// TestAReplyWithoutItsOwnIDStillJoinsTheConversation — some clients stamp
// In-Reply-To but no Message-Id. Its subject differs here on purpose, so only the
// reference can put it in the conversation, and its parent is a mid-thread reply,
// so only walking that reference to the root puts it in the RIGHT one.
func TestAReplyWithoutItsOwnIDStillJoinsTheConversation(t *testing.T) {
	root := vmail.StoredMessage{ID: "1", MessageID: "root@x", Subject: "Budget"}
	mid := vmail.StoredMessage{ID: "2", MessageID: "mid@x", InReplyTo: "root@x", Subject: "Re: Budget"}
	idless := vmail.StoredMessage{ID: "3", InReplyTo: "mid@x", Subject: "numbers attached"}
	key := mailThreadKeyer([]vmail.StoredMessage{root, mid, idless})
	if key(idless) != key(root) {
		t.Errorf("an id-less reply must join its conversation's root: %q vs %q", key(idless), key(root))
	}
}

// TestMailWithNoIDsStillGroupsBySubject keeps the old behaviour as the fallback,
// so a sender that stamps no ids is not a regression.
func TestMailWithNoIDsStillGroupsBySubject(t *testing.T) {
	a := vmail.StoredMessage{ID: "1", Subject: "Re: Newsletter"}
	b := vmail.StoredMessage{ID: "2", Subject: "Newsletter"}
	key := mailThreadKeyer([]vmail.StoredMessage{a, b})
	if key(a) == "" || key(a) != key(b) {
		t.Errorf("id-less mail must still group by subject: %q vs %q", key(a), key(b))
	}
}

// TestAMalformedReferenceLoopTerminates guards the bounded walk.
func TestAMalformedReferenceLoopTerminates(t *testing.T) {
	a := vmail.StoredMessage{ID: "1", MessageID: "a@x", InReplyTo: "b@x", Subject: "Loop"}
	b := vmail.StoredMessage{ID: "2", MessageID: "b@x", InReplyTo: "a@x", Subject: "Loop"}
	key := mailThreadKeyer([]vmail.StoredMessage{a, b})
	if key(a) == "" || key(b) == "" {
		t.Error("a reference loop must still produce a key")
	}
}

// TestTheEngineExposesThreadingEvidence pins the engine half: without these
// fields the console can only ever guess from the subject.
func TestTheEngineExposesThreadingEvidence(t *testing.T) {
	inbound := readFileString(t, "../../internal/vayuos/mail/inbound.go")
	md := readFileString(t, "../../internal/vayuos/mail/maildir.go")
	for _, want := range []string{"MessageID", "InReplyTo", "References"} {
		if !strings.Contains(inbound, want) {
			t.Errorf("StoredMessage is missing %s", want)
		}
	}
	for _, want := range []string{`Get("Message-Id")`, `Get("In-Reply-To")`, `Get("References")`} {
		if !strings.Contains(md, want) {
			t.Errorf("the header cache does not read %s", want)
		}
	}
	if !strings.Contains(md, "func cleanMessageID(") {
		t.Error("ids must be normalised (brackets stripped, case folded) before comparison")
	}
}
