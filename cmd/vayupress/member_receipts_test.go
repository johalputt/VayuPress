// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/comments"
	"github.com/johalputt/vayupress/internal/payments"
)

// TestReceiptStatusChipCoversEveryOrderStatus: a status the switch does not know
// must still render, or a new payment state becomes an invisible blank where a
// member expects to see what happened to their money.
func TestReceiptStatusChipCoversEveryOrderStatus(t *testing.T) {
	all := []string{
		payments.StatusPaid, payments.StatusPending, payments.StatusRefunded,
		payments.StatusCanceled, payments.StatusFailed,
	}
	for _, s := range all {
		got := receiptStatusChip(s)
		if got == "" {
			t.Errorf("status %q renders no chip", s)
		}
		if strings.Contains(got, ">"+s+"<") && s != "" {
			t.Errorf("status %q fell through to the raw-status default; it should have a human label", s)
		}
	}
	// Only "paid" is the healthy state. A refund rendering as a green tick would
	// tell a member their money is fine when it has been sent back.
	if !strings.Contains(receiptStatusChip(payments.StatusPaid), "ma-chip--on") {
		t.Error("paid should be the affirmative chip state")
	}
	for _, s := range []string{payments.StatusRefunded, payments.StatusCanceled, payments.StatusFailed, payments.StatusPending} {
		if strings.Contains(receiptStatusChip(s), "ma-chip--on") {
			t.Errorf("status %q must not render as the affirmative state", s)
		}
	}
	// An unknown status shows itself rather than vanishing.
	if !strings.Contains(receiptStatusChip("held_for_review"), "held_for_review") {
		t.Error("an unrecognised status must still be visible")
	}
}

// TestCommentStatusChipIsHonestWithoutTippingOffSpammers pins the two-sided
// requirement: the author sees every comment they wrote and its real state, but
// "spam" is never named as such.
func TestCommentStatusChipIsHonestWithoutTippingOffSpammers(t *testing.T) {
	if !strings.Contains(commentStatusChip(comments.StatusApproved), "Live") {
		t.Error("an approved comment must say it is live")
	}
	if !strings.Contains(commentStatusChip(comments.StatusPending), "In review") {
		t.Error("a pending comment must say it is still in review, not appear live")
	}
	// Both non-approved states collapse to one wording.
	spam := commentStatusChip(comments.StatusSpam)
	rejected := commentStatusChip(comments.StatusRejected)
	if spam != rejected {
		t.Errorf("spam and rejected must be indistinguishable to the author: %q vs %q", spam, rejected)
	}
	if strings.Contains(strings.ToLower(spam), "spam") {
		t.Errorf("the author must never be told their comment was marked spam: %q", spam)
	}
	// Pending must not read as the affirmative state.
	if strings.Contains(commentStatusChip(comments.StatusPending), "ma-chip--on") {
		t.Error("a comment awaiting review must not render as the affirmative state")
	}
}

func TestTruncateComment(t *testing.T) {
	// Short bodies pass through untouched, with whitespace collapsed.
	if got := truncateComment("  hello   world \n again "); got != "hello world again" {
		t.Errorf("whitespace should collapse, got %q", got)
	}
	if strings.Contains(truncateComment("short"), "…") {
		t.Error("nothing was cut, so no ellipsis should be added")
	}
	// Long bodies are cut, marked, and not left mid-word.
	long := strings.Repeat("word ", 200)
	got := truncateComment(long)
	if !strings.HasSuffix(got, "…") {
		t.Error("a truncated body must be marked as truncated")
	}
	if len(got) > commentPreviewLen+4 {
		t.Errorf("preview is %d chars, over the %d limit", len(got), commentPreviewLen)
	}
	// A single unbroken token has no word boundary to cut on; it must still be
	// cut rather than returned whole.
	blob := strings.Repeat("x", 500)
	if g := truncateComment(blob); len(g) > commentPreviewLen+4 {
		t.Errorf("an unbroken token was not truncated: %d chars", len(g))
	}
}

func TestCountOf(t *testing.T) {
	cases := []struct {
		n    int
		noun string
		want string
	}{
		{1, "payment", "1 payment"},
		{2, "payment", "2 payments"},
		{0, "comment", "0 comments"},
	}
	for _, c := range cases {
		if got := countOf(c.n, c.noun); got != c.want {
			t.Errorf("countOf(%d, %q) = %q, want %q", c.n, c.noun, got, c.want)
		}
	}
}

// TestAccountSectionsAppendSoTheyDoNotStealTheOpenRow guards a documented
// invariant of the account page: the open accordion is chosen positionally
// (i == 0), so a new row inserted at the front would silently collapse the
// VayuMail row that members actually come to the page for.
func TestAccountSectionsAppendSoTheyDoNotStealTheOpenRow(t *testing.T) {
	src, err := os.ReadFile("handlers_member_portal.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(src)
	firstRow := strings.Index(s, `rows = append(rows, accRow{`)
	mail := strings.Index(s, `accRow{"&#128236;", "VayuMail address"`)
	receipts := strings.Index(s, `"Receipts"`)
	cmts := strings.Index(s, `accRow{"&#128172;", "Your comments"`)
	if mail < 0 || receipts < 0 || cmts < 0 || firstRow < 0 {
		t.Fatal("expected the mail, receipts and comments rows to all be present")
	}
	if receipts < mail || cmts < mail {
		t.Error("receipts/comments must be appended AFTER the VayuMail row, or they steal the open state")
	}
	// And the open row must still be selected positionally, which is what makes
	// the ordering above matter.
	if !strings.Contains(s, "i == 0") {
		t.Error("the open row is no longer positional; re-check the ordering assumption above")
	}
}

// TestReceiptProductLabelIsEscaped: orderProductLabel interpolates the raw tier
// slug, and the receipts renderer writes its result into the page as markup. The
// admin ledger escapes it at the call site; this pins that the member page does
// too, since a slug is operator-controlled input reaching a member's browser.
func TestReceiptProductLabelIsEscaped(t *testing.T) {
	src := repoFile(t, "cmd/vayupress/member_receipts.go")
	if !strings.Contains(src, "html.EscapeString(orderProductLabel(") {
		t.Error("orderProductLabel output is written as markup and must be escaped")
	}
	// The tier NAME is likewise operator-controlled.
	if !strings.Contains(src, `"Membership: " + html.EscapeString(name)`) {
		t.Error("a resolved tier name must be escaped")
	}
	// And the receipt should prefer the name a member actually bought over the
	// internal slug the admin ledger shows.
	if !strings.Contains(src, "tierNames[o.TierSlug]") {
		t.Error("a member-facing receipt should name the tier, not print its slug")
	}
}

// TestMemberReceiptAndCommentStylesExist: these sections render classes that must
// be styled, since signup.css is the only stylesheet the account page loads —
// console classes silently render as plain text there.
func TestMemberReceiptAndCommentStylesExist(t *testing.T) {
	css := repoFile(t, "static/css/signup.css")
	for _, want := range []string{
		".ma-rcpts", ".ma-rcpt__main", ".ma-rcpt__amt", ".ma-rcpt__ref", ".ma-rcpt__cad",
		".ma-cmts", ".ma-cmt__body", ".ma-cmt__where", ".ma-cmt__where--gone", ".ma-cmt__when",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("missing %s — the account page renders it, so it must be styled", want)
		}
	}
	if strings.Count(css, "{") != strings.Count(css, "}") {
		t.Error("unbalanced braces in signup.css")
	}
	// The receipts section must not reach for console pill classes.
	rec := repoFile(t, "cmd/vayupress/member_receipts.go")
	if strings.Contains(rec, "orderStatusPill(") {
		t.Error("orderStatusPill emits console .status-pill classes that signup.css does not define")
	}
}
