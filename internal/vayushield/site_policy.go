// SPDX-License-Identifier: Apache-2.0

package vayushield

// site_policy.go — a hosted site's own shield settings.
//
// An install serving many sites applied one policy to all of them: one site
// under a scraper's attention could only be helped by tightening every site,
// and a site that needed a country refused had to refuse it everywhere. Each
// site may now carry its own settings, under two rules that are the same for
// every field of a kind:
//
//   - Lists ADD to the install's. The install's deny list always applies on
//     every site; a site can refuse, challenge or allow more on top of it, and
//     never less. Allowing a source on one site does not lift an install-wide
//     refusal, because the deny verdict is evaluated first in the merged set.
//   - Thresholds REPLACE the install's, when the site sets one. Unset (0), the
//     site inherits the install's value, and the console says which it is.
//
// Everything is compiled when settings are saved. A request pays one lookup
// of its site in a map that holds only the sites that set something.

import (
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/vayushield/oprules"
	"github.com/johalputt/vayupress/internal/vayushield/policy"
)

// SiteSettings is one site's own settings. A zero threshold inherits the
// install's; the lists are added to the install's.
type SiteSettings struct {
	PoWThreshold, JSThreshold, BlockThreshold float64
	Policy                                    policy.Config
	// Rules is the site's own request rules (oprules), evaluated after the
	// install's: the install's first match decides.
	Rules string
}

// sitePolicy is a site's compiled settings.
type sitePolicy struct {
	pow, js, block float64
	// rules is the install's lists and the site's, merged; nil when the site
	// adds no list, so the install's compiled set is used as it is.
	rules *policy.Rules
	// opRules is the site's own request rules; nil when it writes none.
	opRules *oprules.Set
}

// SetSiteSettings replaces every site's own settings, keyed by the site's id
// (the value Config.SiteFn returns for its requests). It returns, per site,
// the entries that could not be parsed, so the console can name them.
func (m *Manager) SetSiteSettings(sites map[string]SiteSettings) map[string][]string {
	cp := make(map[string]SiteSettings, len(sites))
	for k, v := range sites {
		cp[k] = v
	}
	m.siteCfg.Store(&cp)
	return m.compileSites()
}

// compileSites rebuilds every site's merged rules from the install's policy
// as it now is. ApplySettings calls it too, so a change to the install's
// lists reaches every site that added its own.
func (m *Manager) compileSites() map[string][]string {
	cfgs := m.siteCfg.Load()
	if cfgs == nil || len(*cfgs) == 0 {
		m.sites.Store(nil)
		return nil
	}
	var install policy.Config
	if s := m.curSettings.Load(); s != nil {
		install = s.Policy
	}
	clamp := func(f float64) float64 {
		if f <= 0 || f > 1 {
			return 0
		}
		return f
	}
	out := make(map[string]*sitePolicy, len(*cfgs))
	issues := map[string][]string{}
	for site, c := range *cfgs {
		sp := &sitePolicy{pow: clamp(c.PoWThreshold), js: clamp(c.JSThreshold), block: clamp(c.BlockThreshold)}
		if hasLists(c.Policy) {
			merged := install
			merged.AllowCIDRs = joinLists(install.AllowCIDRs, c.Policy.AllowCIDRs)
			merged.DenyCIDRs = joinLists(install.DenyCIDRs, c.Policy.DenyCIDRs)
			merged.AllowCountries = joinLists(install.AllowCountries, c.Policy.AllowCountries)
			merged.DenyCountries = joinLists(install.DenyCountries, c.Policy.DenyCountries)
			merged.ChallengeCountries = joinLists(install.ChallengeCountries, c.Policy.ChallengeCountries)
			merged.RefuseUnknownCountry = install.RefuseUnknownCountry || c.Policy.RefuseUnknownCountry
			rules, bad := policy.Compile(merged)
			sp.rules = &rules
			issues[site] = append(issues[site], bad...)
		}
		if strings.TrimSpace(c.Rules) != "" {
			set, bad := oprules.Parse(c.Rules)
			sp.opRules = set
			issues[site] = append(issues[site], bad...)
		}
		if sp.pow == 0 && sp.js == 0 && sp.block == 0 && sp.rules == nil && sp.opRules == nil {
			continue
		}
		out[site] = sp
	}
	m.sites.Store(&out)
	for site, bad := range issues {
		if len(bad) == 0 {
			delete(issues, site)
		}
	}
	return issues
}

func hasLists(c policy.Config) bool {
	return len(c.AllowCIDRs)+len(c.DenyCIDRs)+len(c.AllowCountries)+len(c.DenyCountries)+len(c.ChallengeCountries) > 0 ||
		c.RefuseUnknownCountry
}

// joinLists is a then b, as a new slice: the install's list is never written to.
func joinLists(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	out := make([]string, 0, len(a)+len(b))
	return append(append(out, a...), b...)
}

// siteFor is the compiled settings of r's site, or nil when it sets none.
func (m *Manager) siteFor(r *http.Request) *sitePolicy {
	if m.cfg.SiteFn == nil || r == nil {
		return nil
	}
	all := m.sites.Load()
	if all == nil {
		return nil
	}
	return (*all)[m.cfg.SiteFn(r)]
}

// policyFor is the rule set that governs r: its site's merged lists, or the
// install's.
func (m *Manager) policyFor(r *http.Request) policy.Rules {
	if sp := m.siteFor(r); sp != nil && sp.rules != nil {
		return *sp.rules
	}
	return m.Policy()
}

// thresholdsFor is r's challenge and block thresholds: its site's where it
// sets one, else the install's (pow, js, block as given).
func (m *Manager) thresholdsFor(r *http.Request, pow, js, block float64) (float64, float64, float64) {
	sp := m.siteFor(r)
	if sp == nil {
		return pow, js, block
	}
	if sp.pow > 0 {
		pow = sp.pow
	}
	if sp.js > 0 {
		js = sp.js
	}
	if sp.block > 0 {
		block = sp.block
	}
	return pow, js, block
}
