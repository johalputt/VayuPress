// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A throwaway address delivers into its mailbox and counts what it takes;
// turned off it takes nothing, not even through the domain's catch-all; it
// is its own mailbox's alone to turn off or remove; and there is a ceiling.
func TestThrowawayAddresses(t *testing.T) {
	s := aliasTestStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, "erin@example.com", "hash", "", "mailbox"); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	e := &Engine{cfg: cfg, maildir: NewMaildir(t.TempDir()), accounts: s}
	me, erin := ReadAsSystem("ankush", "test"), ReadAsSystem("erin", "test")

	tw, err := e.NewThrowaway(me, "Shop Example!")
	if err != nil {
		t.Fatal(err)
	}
	local, dom, _ := strings.Cut(tw.Address, "@")
	if dom != "example.com" || !strings.HasPrefix(local, "shopexample-") || len(local) != len("shopexample-")+6 {
		t.Fatalf("the address is %q", tw.Address)
	}
	if !e.isMailboxOrAlias(tw.Address) || e.mailboxFor(tw.Address) != "ankush@example.com" {
		t.Fatal("a throwaway address does not deliver into its mailbox")
	}
	if _, err := e.DeliverInbound("news@shop.example", tw.Address, []byte("From: news@shop.example\r\nSubject: Hi\r\n\r\nhi\r\n")); err != nil {
		t.Fatal(err)
	}
	list, _ := e.Throwaways(me)
	if len(list) != 1 || list[0].Received != 1 || list[0].LastReceived.IsZero() || list[0].Label != "Shop Example!" {
		t.Fatalf("listed as %+v", list)
	}

	// Another mailbox can neither see nor touch it.
	if l, _ := e.Throwaways(erin); len(l) != 0 {
		t.Error("another mailbox lists this one's throwaway addresses")
	}
	if err := e.SetThrowawayOff(erin, tw.Address, true); !errors.Is(err, ErrNotYourThrowaway) {
		t.Errorf("another mailbox turning it off: %v", err)
	}
	if err := e.RemoveThrowaway(erin, tw.Address); !errors.Is(err, ErrNotYourThrowaway) {
		t.Errorf("another mailbox removing it: %v", err)
	}
	// Nor is an ordinary alias of the mailbox a throwaway to turn off.
	if err := s.CreateAlias(ctx, "sales@example.com", "ankush@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := e.SetThrowawayOff(me, "sales@example.com", true); !errors.Is(err, ErrNotYourThrowaway) {
		t.Errorf("an operator's alias turned off as a throwaway: %v", err)
	}
	if err := e.RemoveThrowaway(me, "sales@example.com"); !errors.Is(err, ErrNotYourThrowaway) {
		t.Errorf("an operator's alias removed as a throwaway: %v", err)
	}
	if !e.submissionSenderAllowed("ankush@example.com", tw.Address) {
		t.Error("the holder may not send from their own throwaway address")
	}

	// Off: refused, even with a catch-all for the domain, and not counted.
	if err := s.CreateAlias(ctx, "*@example.com", "erin@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := e.SetThrowawayOff(me, tw.Address, true); err != nil {
		t.Fatal(err)
	}
	if e.isLocalRecipient(tw.Address) {
		t.Error("an address turned off still takes mail")
	}
	if _, err := e.DeliverInbound("news@shop.example", tw.Address, []byte("From: news@shop.example\r\nSubject: Again\r\n\r\nhi\r\n")); !errors.Is(err, ErrAddressOff) {
		t.Errorf("delivered to an address turned off: %v", err)
	}

	if !e.isLocalRecipient("anything-else@example.com") {
		t.Error("the catch-all stopped taking other addresses")
	}
	if err := e.SetThrowawayOff(me, tw.Address, false); err != nil || !e.isLocalRecipient(tw.Address) {
		t.Errorf("turned on again: %v", err)
	}
	if err := e.RemoveThrowaway(me, tw.Address); err != nil || e.mailboxFor(tw.Address) == "ankush@example.com" {
		t.Errorf("removed: %v", err)
	}

	for i := 0; i < MaxThrowaways; i++ {
		if _, err := e.NewThrowaway(me, "site"); err != nil {
			t.Fatalf("throwaway %d: %v", i, err)
		}
	}
	if _, err := e.NewThrowaway(me, "one more"); !errors.Is(err, ErrTooManyThrowaways) {
		t.Errorf("past the ceiling: %v", err)
	}
}
