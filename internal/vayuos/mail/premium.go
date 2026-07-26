// SPDX-License-Identifier: Apache-2.0

package mail

// premium.go — the premium (vanity) localpart classifier for the mail-ID
// marketplace (Phase 3).
//
// Addresses fall into three classes for the member self-service claim path:
//
//   - RESERVED (reserved.go): role / infrastructure names the operator always
//     keeps. Never claimable, never sold to members.
//   - PREMIUM (this file): high-demand generic handles — ultra-short addresses
//     and a curated set of sought-after words. NOT given away on the free claim
//     path; they are the operator's sellable vanity inventory.
//   - GENERIC: everything else — the ordinary address a paying member's tier
//     includes and can self-claim for free.
//
// This file only CLASSIFIES. Pricing and the purchase flow live in the handler
// layer; keeping the classifier here (next to reserved.go) means the free-claim
// guard and the future marketplace share one source of truth for "what counts as
// premium". Operator-created mailboxes are unaffected — the guard applies only to
// the member self-service claim.

import "strings"

// premiumMaxShortLen is the inclusive upper bound below which a localpart is
// premium purely by brevity: 1–2 character addresses are vanishingly rare and
// the most sought-after, so they are never handed out on the free path.
const premiumMaxShortLen = 2

// premiumLocalparts is the seed set of high-demand generic handles kept out of
// the free-claim path so the operator can sell them. These deliberately do NOT
// overlap the RFC-2142 / infrastructure / business-role names in reserved.go
// (those are never claimable at all); a name that is both reserved and listed
// here is simply refused as reserved first. Lowercase, matched exactly.
var premiumLocalparts = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range []string{
		// status / identity handles
		"vip", "pro", "boss", "ceo", "cfo", "cto", "coo", "official", "real",
		"the", "one", "king", "queen", "best", "top", "gold",
		// commerce handles
		"money", "cash", "shop", "store", "buy", "pay", "deals", "offers",
		// comms handles
		"email", "inbox", "chat", "app", "apps", "now",
	} {
		m[s] = true
	}
	return m
}()

// IsPremiumLocalpart reports whether local is a premium (sellable vanity)
// address: an ultra-short handle or a member of the curated premium set. It is
// case-insensitive and independent of reserved status — callers on the claim
// path check reserved first (reserved wins), then premium, so a name is only
// treated as premium when it would otherwise be a claimable generic address.
func IsPremiumLocalpart(local string) bool {
	local = strings.ToLower(strings.TrimSpace(local))
	if local == "" {
		return false
	}
	if len(local) <= premiumMaxShortLen {
		return true
	}
	return premiumLocalparts[local]
}
