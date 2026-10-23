// SPDX-License-Identifier: Apache-2.0

package vayushield

// operator_rules.go — the operator's own request rules (package oprules), as
// the middleware applies them.
//
// They run after the classification ladder has decided, so a rule sees the
// shield's verdict (client type, score) as well as the request, and a fact the
// operator wrote outranks what the shield inferred. Everything that returns
// earlier still does: a verified session, a confirmed crawler, the jail, the
// rate limiter and the bypassed paths never reach a rule, so a rule cannot
// lock the operator out of the console or de-index a site through a crawler
// whose identity the network confirmed.

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/oprules"
)

// matchRule is the rule that decides r, or nil, and how an event names it
// ("line 3", or "site line 3" for the site's own): the install's rules first,
// then r's site's, so a site can add rules and never override the install's.
// source is who a limit rule counts r against.
func (m *Manager) matchRule(r *http.Request, v Verdict, source string) (*oprules.Rule, string) {
	install := m.opRules.Load()
	var site *oprules.Set
	if sp := m.siteFor(r); sp != nil {
		site = sp.opRules
	}
	if len(install.Rules()) == 0 && len(site.Rules()) == 0 {
		return nil, ""
	}
	f := oprules.Facts{
		Path:   r.URL.Path,
		Method: r.Method,
		Client: strings.ToLower(string(v.Result.ClientType)),
		Score:  v.Result.BotScore,
		Header: r.Header.Get,
	}
	// In a Tor Space every visitor arrives as 127.0.0.1 and a country lookup
	// answers with the server's own location, so both facts are withheld: a
	// country rule matches nobody rather than everybody, and a limit never
	// holds the whole audience to one allowance.
	if !m.cfg.OnionMode {
		f.Source = source
		if m.cfg.CountryFn != nil {
			f.Country = m.cfg.CountryFn(r, ipOnly(m.cfg.ClientIP(r)))
		}
	}
	now := time.Now()
	if rule := install.Match(f, now); rule != nil {
		return rule, "line " + strconv.Itoa(rule.Line)
	}
	if rule := site.Match(f, now); rule != nil {
		return rule, "site line " + strconv.Itoa(rule.Line)
	}
	return nil, ""
}

// OperatorRules is the install's compiled request rules, with their hit
// counts, and the lines the last save could not read.
func (m *Manager) OperatorRules() ([]*oprules.Rule, []string) {
	var issues []string
	if p := m.opRuleIssues.Load(); p != nil {
		issues = *p
	}
	return m.opRules.Load().Rules(), issues
}

// SiteOperatorRules is one site's compiled request rules, with their hit
// counts; nil when the site writes none.
func (m *Manager) SiteOperatorRules(site string) []*oprules.Rule {
	all := m.sites.Load()
	if all == nil {
		return nil
	}
	if sp := (*all)[site]; sp != nil {
		return sp.opRules.Rules()
	}
	return nil
}
