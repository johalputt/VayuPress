// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_brand.go — the one path a hosted domain's branding is written by.
//
// There are two endpoints: the operator's /os/api/domains/{id}/brand and the
// client's own /os/api/mysite/brand. They write the same six fields, and before
// this file they wrote them two different ways.
//
// WHAT WAS WRONG WITH THAT, twice over.
//
// Both called domains.SetBrand and stopped there. SetBrand writes
// domains.config_json — which ADR-0153 Phase 4 removed from the public render
// path. brandForRequest now asks settings.ForDomain(d.ID) first and returns as
// soon as GetAll succeeds; GetAll on a scope with no rows succeeds, handing back
// the compiled-in defaults. d.Brand() is consulted only when the settings store
// ERRORS. So a brand save changed nothing a visitor could see, while the client's
// page said "Changes go live immediately". ADR-0154 D3 had already written down
// the remedy — "writing through to the scoped store" — for the operator's
// endpoint, and it was never done for either.
//
// And only the OPERATOR's endpoint validated. It hex-checked the three colour
// fields and length-clipped the three text fields; the client's — the one
// reachable by a principal the operator does not employ — did neither. That was
// close to inert while the brand reached nothing. Making the brand reach the
// render path is precisely what would have armed it, so the write-through and
// the validation had to land together or the fix would have opened the hole.
//
// Hence one path. A second copy of a rule is a future divergence; here it was
// the divergence already.

import (
	"context"
	"errors"
	"strings"

	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
)

// errBrandColor is returned for a colour field that is neither empty nor a plain
// hex colour. Callers turn it into a 400 naming the field.
var errBrandColor = errors.New("must be a hex colour like #2563eb")

// brandColorChecks pairs each colour with the label an operator sees, so the
// refusal names the box they typed in rather than a struct field.
func brandColorChecks(b domain.Brand) []struct{ Label, Value string } {
	return []struct{ Label, Value string }{
		{"Accent (light)", b.AccentLight},
		{"Accent (dark)", b.AccentDark},
		{"Theme colour", b.ThemeColor},
	}
}

// normalizeBrand trims, caps and validates. It is pure so the rules can be
// tested without a store, and so neither endpoint can hold a different version
// of them.
//
// The colour check is not cosmetic: these values are interpolated into the
// domain's /theme.css and its <meta theme-color>, so anything but a hex colour
// is a CSS injection into the client's own page.
func normalizeBrand(b domain.Brand) (domain.Brand, error) {
	clip := func(s string, n int) string {
		s = strings.TrimSpace(s)
		if len(s) > n {
			s = strings.TrimSpace(s[:n])
		}
		return s
	}
	out := domain.Brand{
		SiteName:    clip(b.SiteName, 120),
		Tagline:     clip(b.Tagline, 200),
		Description: clip(b.Description, 320),
		AccentLight: strings.TrimSpace(b.AccentLight),
		AccentDark:  strings.TrimSpace(b.AccentDark),
		ThemeColor:  strings.TrimSpace(b.ThemeColor),
	}
	for _, c := range brandColorChecks(out) {
		// Empty is how a field is CLEARED, and refusing it would leave a client
		// unable to undo a colour they had set.
		if c.Value != "" && !hexColorRe.MatchString(c.Value) {
			return domain.Brand{}, errors.New(c.Label + " " + errBrandColor.Error())
		}
	}
	return out, nil
}

// brandSettingValues maps a brand onto the settings keys the render path reads.
//
// Every field is written, including the empty ones. Writing only the non-empty
// fields would make a save that CLEARS a tagline a no-op, so the box would look
// un-editable in the one direction a client most often wants: taking something
// off their site.
func brandSettingValues(b domain.Brand) map[string]string {
	return map[string]string{
		settings.KeySiteName:         b.SiteName,
		settings.KeySiteTagline:      b.Tagline,
		settings.KeySiteDescription:  b.Description,
		settings.KeyThemeAccentLight: b.AccentLight,
		settings.KeyThemeAccentDark:  b.AccentDark,
		settings.KeyHeadThemeColor:   b.ThemeColor,
	}
}

// saveBrand validates a brand and writes it to both stores that matter.
//
// The scoped settings store is what the public site reads. The domain record is
// what brandForRequest falls back to when that store cannot answer, so dropping
// it would leave the fallback with nothing to fall back to — a save that works
// until the day the database is the thing that is wrong.
func (a *App) saveBrand(ctx context.Context, domainID string, in domain.Brand) (domain.Brand, error) {
	b, err := normalizeBrand(in)
	if err != nil {
		return domain.Brand{}, err
	}
	if a.domains == nil {
		return domain.Brand{}, errors.New("domain registry not initialised")
	}
	if err := a.domains.SetBrand(ctx, domainID, b); err != nil {
		return domain.Brand{}, err
	}
	if sc := settings.ForDomain(domainID); sc.Valid() && a.siteSettings != nil {
		if err := a.siteSettings.SetMany(ctx, sc, brandSettingValues(b)); err != nil {
			return domain.Brand{}, err
		}
	}
	return b, nil
}
