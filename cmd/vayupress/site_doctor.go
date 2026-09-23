// SPDX-License-Identifier: Apache-2.0

package main

// site_doctor.go — the publish gate run again on what each site serves now.
//
// A site passes the gate the day it is published and can fail it a week
// later: a picture deleted from Media, a post a button linked to taken down.
// Nothing about the site changed, so nothing re-checked it. The doctor reads
// what is live — never the draft — and puts what a visitor would hit into the
// console's attention strip, the site's console and the connector.

import (
	"context"
	"strings"

	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/sitedoc"
)

// liveHostedDocument is what a hosted site serves: its newest publish, else
// its legacy content as a document.
func liveHostedDocument(ctx context.Context, d domain.Domain) sitedoc.Document {
	if doc, ok := publishedSiteDoc(ctx, d.ID); ok {
		return doc
	}
	site, _ := d.Site()
	tpl := bizsite.ByKey(site.Template)
	return sitedoc.FromLegacy(tpl, bizsite.EffectiveContent(tpl, site.Content))
}

// livePrimaryDocument is what the primary site serves, and whether it serves
// a template website at all.
func (a *App) livePrimaryDocument(ctx context.Context) (sitedoc.Document, bool) {
	if a.siteSettings == nil {
		return sitedoc.Document{}, false
	}
	get := func(k string) string { return a.siteSettings.Get(ctx, settings.ForPrimary(), k) }
	if !strings.HasPrefix(strings.TrimSpace(get(settings.KeySiteMode)), "business") {
		return sitedoc.Document{}, false
	}
	if doc, ok := publishedSiteDoc(ctx, ""); ok {
		return doc, true
	}
	tpl := bizsite.ByKey(strings.TrimSpace(get(settings.KeyBizTemplate)))
	return sitedoc.FromLegacy(tpl, bizsite.EffectiveContent(tpl, get(settings.KeyBizContent))), true
}

// hostedSiteChecks is the doctor's reading of a hosted site: everything the
// gate finds about what it serves. A site not serving a template website
// has nothing for it to read.
func (a *App) hostedSiteChecks(ctx context.Context, d domain.Domain) []siteCheck {
	if d.IsPrimary || !strings.HasPrefix(scopedSiteMode(d), "business") {
		return nil
	}
	return a.checkSite(ctx, d.ID, liveHostedDocument(ctx, d))
}

func errorCount(checks []siteCheck) int {
	n := 0
	for _, c := range checks {
		if c.Level == "error" {
			n++
		}
	}
	return n
}
