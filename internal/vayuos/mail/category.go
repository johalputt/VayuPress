// SPDX-License-Identifier: Apache-2.0

package mail

// category.go — which of the Inbox's categories a message belongs in, read
// from its headers and subject alone, with no model and nothing sent
// anywhere: Primary (people), Updates (what systems tell you), Newsletters,
// Receipts and Social. It is computed with the listing's cached summary
// (summarize), so sorting a folder into categories costs no extra read.

import (
	"net/textproto"
	"regexp"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// The categories, as the list's tabs name them in the URL.
const (
	CategoryPrimary     = "primary"
	CategoryUpdates     = "updates"
	CategoryNewsletters = "newsletters"
	CategoryReceipts    = "receipts"
	CategorySocial      = "social"
)

// Categories is every category, in the order the tabs show them.
var Categories = []string{CategoryPrimary, CategoryUpdates, CategoryNewsletters, CategoryReceipts, CategorySocial}

// receiptSubject is a subject that says money changed hands: a receipt, an
// invoice, an order or a payment, said the ways shops say it.
var receiptSubject = regexp.MustCompile(`(?i)\b(receipt|invoice|order (confirmation|confirmed|#?\d)|your order|order #|payment (received|confirmation|confirmed)|booking confirm|refund (issued|processed)|purchase confirm)`)

// socialSites are the networks whose notices go under Social, by their
// registrable domain.
var socialSites = map[string]bool{
	"facebook.com": true, "facebookmail.com": true, "instagram.com": true, "linkedin.com": true,
	"x.com": true, "twitter.com": true, "reddit.com": true, "redditmail.com": true, "pinterest.com": true,
	"tiktok.com": true, "youtube.com": true, "quora.com": true, "discord.com": true, "meetup.com": true,
	"nextdoor.com": true, "strava.com": true, "mastodon.social": true, "bsky.app": true, "threads.net": true,
}

// automatedLocal are the sender names that mean no person wrote it.
var automatedLocal = map[string]bool{
	"noreply": true, "no-reply": true, "donotreply": true, "do-not-reply": true, "notifications": true,
	"notification": true, "notify": true, "alerts": true, "alert": true, "updates": true, "mailer-daemon": true,
	"postmaster": true, "bounce": true, "bounces": true, "automated": true,
}

// categorize sorts one message by its header block, From address and
// subject. The order matters: a shop's receipt and a network's notice often
// carry a list's unsubscribe header too, and are still a receipt and a
// notice.
func categorize(hdr textproto.MIMEHeader, from, subject string) string {
	addr := strings.ToLower(from)
	if i := strings.LastIndexByte(addr, '<'); i >= 0 {
		addr = strings.TrimSuffix(addr[i+1:], ">")
	}
	addr = strings.TrimSpace(addr)
	local, dom, _ := strings.Cut(addr, "@")
	if site, err := publicsuffix.EffectiveTLDPlusOne(dom); err == nil {
		dom = site
	}
	switch {
	case receiptSubject.MatchString(subject):
		return CategoryReceipts
	case socialSites[dom]:
		return CategorySocial
	case hdr.Get("List-Unsubscribe") != "" || hdr.Get("List-Id") != "" ||
		strings.EqualFold(hdr.Get("Precedence"), "bulk") || strings.EqualFold(hdr.Get("Precedence"), "list"):
		return CategoryNewsletters
	case automatedLocal[local] || (hdr.Get("Auto-Submitted") != "" && !strings.EqualFold(hdr.Get("Auto-Submitted"), "no")):
		return CategoryUpdates
	}
	return CategoryPrimary
}
