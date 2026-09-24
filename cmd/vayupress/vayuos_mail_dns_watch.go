// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_dns_watch.go — the mail-domain verdict the rest of the console
// can show without asking DNS.
//
// The DNS tab's checklist is live: every render runs MX/SPF/DKIM/DMARC lookups
// for every mail domain. That is right on the tab and wrong everywhere else — a
// badge on every console page would put those lookups on every page render. So
// the verdict is remembered here: written by every live check (the tab, its
// Re-check button) and by a slow background watch, and read — never computed —
// by the notification bell and the DNS tab marker.

import (
	"context"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

// mailDNSVerdict is the last completed mail-domain check.
type mailDNSVerdict struct {
	AllOK   bool
	Next    string // the first unfinished item, as the checklist names it
	Checked time.Time
}

// mailDNSWatchInterval is how often the background watch re-checks. DNS changes
// are made by a person at a registrar, so hours is soon enough; the tab and its
// Re-check button refresh the verdict immediately when someone is working on it.
const mailDNSWatchInterval = 6 * time.Hour

// recordMailDNS stores a completed check.
func (a *App) recordMailDNS(h dnsHealth) {
	next, _ := firstDNSUnfinished(h)
	a.mailDNS.Store(&mailDNSVerdict{AllOK: h.AllOK, Next: next, Checked: time.Now()})
}

// mailDNSNeedsAttention reports a stored failing verdict and its next step. An
// install that has never completed a check reports nothing: "unknown" is not a
// problem to raise.
func (a *App) mailDNSNeedsAttention() (string, bool) {
	v := a.mailDNS.Load()
	if v == nil || v.AllOK {
		return "", false
	}
	return v.Next, true
}

// startMailDNSWatch runs the background check. It waits before the first one so
// boot never pays for DNS, and does nothing in a Tor world, whose mail is
// onion-only and has no public DNS to check.
func (a *App) startMailDNSWatch() {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || config.Cfg.OnionMode {
		return
	}
	go func() {
		time.Sleep(2 * time.Minute)
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			a.vayuDNSHealth(ctx) // records its own verdict
			cancel()
			time.Sleep(mailDNSWatchInterval)
		}
	}()
}
