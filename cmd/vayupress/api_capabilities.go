// SPDX-License-Identifier: Apache-2.0

package main

// api_capabilities.go — the single source of truth mapping API routes to the
// fine-grained capability (section:action, ADR-0134) an API key must hold to
// call them. One table covers both surfaces — /api/v1 (+legacy /admin API) and
// the /os twins — keyed by handler intent, not URL prefix, so a grant can never
// be bypassed by switching prefixes.
//
// Matching: the most specific (longest) matching prefix rule wins. A rule may
// pin an explicit action (apply/install/export endpoints); otherwise the action
// derives from the HTTP method: GET/HEAD → read, DELETE → delete, everything
// else (POST/PUT/PATCH) → write.
//
// Fail-closed: a key-authenticated request to a protected route with NO matching
// rule requires a superuser key. Scoped keys can only ever reach routes this
// table explicitly names. Session-authenticated operators are untouched — their
// access is governed by session RBAC, never by this table.

import (
	"net/http"
	"sort"
	"strings"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
)

// capRule maps one path prefix to a section, optionally pinning the action for
// every method (e.g. theme apply is "apply" whatever the verb).
type capRule struct {
	prefix  string
	section apikeys.Section
	action  apikeys.Action // "" = derive from the HTTP method
}

// capabilityRules is the ordered rule set. Order in this literal does not
// matter — rules are sorted longest-prefix-first at init so the most specific
// rule always wins (e.g. /api/v1/admin/theme/apply before /api/v1/admin/theme).
var capabilityRules = []capRule{
	// ── posts: articles, queue, scheduling, collections, editor, AI assist ──
	{prefix: "/api/v1/articles", section: apikeys.SectionPosts},
	{prefix: "/api/v1/queue", section: apikeys.SectionPosts},
	{prefix: "/api/v1/admin/articles", section: apikeys.SectionPosts},
	{prefix: "/api/v1/admin/schedule", section: apikeys.SectionPosts},
	{prefix: "/api/v1/collections", section: apikeys.SectionPosts},
	{prefix: "/api/v1/admin/collections", section: apikeys.SectionPosts},
	{prefix: "/api/v1/admin/ai", section: apikeys.SectionPosts},
	{prefix: "/os/api/posts", section: apikeys.SectionPosts},
	{prefix: "/os/api/pages", section: apikeys.SectionPosts},
	{prefix: "/os/api/editor", section: apikeys.SectionPosts},
	{prefix: "/os/api/feed", section: apikeys.SectionPosts},

	// ── comments: moderation, webmentions, contact messages ──
	{prefix: "/api/v1/admin/comments", section: apikeys.SectionComments},
	{prefix: "/api/v1/admin/webmentions", section: apikeys.SectionComments},
	{prefix: "/os/api/comments", section: apikeys.SectionComments},
	{prefix: "/os/api/messages/export.csv", section: apikeys.SectionComments, action: apikeys.ActionExport},
	{prefix: "/os/api/messages", section: apikeys.SectionComments},

	// ── members: readers, tiers, newsletter, orders ──
	{prefix: "/api/v1/admin/members/export.csv", section: apikeys.SectionMembers, action: apikeys.ActionExport},
	{prefix: "/api/v1/admin/members", section: apikeys.SectionMembers},
	{prefix: "/api/v1/admin/tiers", section: apikeys.SectionMembers},
	{prefix: "/api/v1/admin/newsletter", section: apikeys.SectionMembers},
	{prefix: "/os/api/members/export.csv", section: apikeys.SectionMembers, action: apikeys.ActionExport},
	{prefix: "/os/api/members", section: apikeys.SectionMembers},
	{prefix: "/os/api/newsletter/export.csv", section: apikeys.SectionMembers, action: apikeys.ActionExport},
	{prefix: "/os/api/newsletter", section: apikeys.SectionMembers},
	{prefix: "/os/api/orders", section: apikeys.SectionMembers},

	// ── analytics ──
	{prefix: "/api/v1/analytics/export", section: apikeys.SectionAnalytics, action: apikeys.ActionExport},
	{prefix: "/api/v1/analytics", section: apikeys.SectionAnalytics},
	{prefix: "/api/v1/admin/analytics", section: apikeys.SectionAnalytics},
	{prefix: "/os/api/analytics/export", section: apikeys.SectionAnalytics, action: apikeys.ActionExport},
	{prefix: "/os/api/analytics", section: apikeys.SectionAnalytics},
	{prefix: "/os/api/activity", section: apikeys.SectionAnalytics},

	// ── media: uploads, imports, unfurl, diagrams ──
	{prefix: "/api/v1/admin/media", section: apikeys.SectionMedia},
	{prefix: "/api/v1/admin/embed", section: apikeys.SectionMedia},
	{prefix: "/api/v1/admin/diagram", section: apikeys.SectionMedia},
	{prefix: "/os/api/media", section: apikeys.SectionMedia},
	{prefix: "/os/api/embed", section: apikeys.SectionMedia},
	{prefix: "/os/api/diagram", section: apikeys.SectionMedia},

	// ── themes: tokens, presets, preview/apply, export ──
	{prefix: "/api/v1/admin/theme/apply", section: apikeys.SectionThemes, action: apikeys.ActionApply},
	{prefix: "/api/v1/admin/theme/preview", section: apikeys.SectionThemes, action: apikeys.ActionApply},
	{prefix: "/api/v1/admin/theme", section: apikeys.SectionThemes},
	{prefix: "/admin/theme/export", section: apikeys.SectionThemes, action: apikeys.ActionExport},
	{prefix: "/admin/theme", section: apikeys.SectionThemes},
	{prefix: "/os/api/theme", section: apikeys.SectionThemes},

	// ── design: branding, website builder, ads placement ──
	{prefix: "/os/api/branding", section: apikeys.SectionDesign},
	{prefix: "/os/api/website", section: apikeys.SectionDesign},
	{prefix: "/os/api/ads", section: apikeys.SectionDesign},

	// ── plugins: tool/plugin toggles are installs ──
	{prefix: "/os/api/tools", section: apikeys.SectionPlugins, action: apikeys.ActionInstall},
	// VCB contract discovery + manifest validation: read-only whatever the
	// verb (validation is a pure function of the request body). Trailing
	// slash so an unrelated future /api/v1/vcbXYZ route can never inherit
	// this rule.
	{prefix: "/api/v1/vcb/", section: apikeys.SectionPlugins, action: apikeys.ActionRead},

	// ── domains (VayuDomains) ──
	{prefix: "/os/api/domains", section: apikeys.SectionDomains},
	// Provisioning starts a root systemd unit. Mapped here rather than left to
	// the fail-closed superuser default so it states the SAME rule as the MCP
	// tool provision_certificates, which does the identical thing behind
	// domains:write. Two doors to one privileged action must not disagree by
	// omission.
	{prefix: "/os/api/provision", section: apikeys.SectionDomains},

	// ── mail (VayuMail + VayuTalk surfaces) ──
	{prefix: "/os/vayumail", section: apikeys.SectionMail},
	{prefix: "/os/api/vayumail", section: apikeys.SectionMail},
	{prefix: "/os/talk", section: apikeys.SectionMail},
	{prefix: "/os/api/talk", section: apikeys.SectionMail},

	// ── backup ──
	{prefix: "/os/api/backup/export", section: apikeys.SectionBackup, action: apikeys.ActionExport},
	{prefix: "/os/api/backup", section: apikeys.SectionBackup},
	{prefix: "/admin/backup", section: apikeys.SectionBackup},

	// ── settings: staff users, system ops, observability, integrations ──
	{prefix: "/api/v1/admin/users", section: apikeys.SectionSettings},
	{prefix: "/api/v1/admin/webhooks", section: apikeys.SectionSettings},
	{prefix: "/api/v1/admin/redirects", section: apikeys.SectionSettings},
	{prefix: "/api/v1/admin/email-templates", section: apikeys.SectionSettings},
	{prefix: "/api/v1/admin/i18n", section: apikeys.SectionSettings},
	{prefix: "/api/v1/admin/outbox", section: apikeys.SectionSettings},
	{prefix: "/api/v1/admin/trace", section: apikeys.SectionSettings},
	{prefix: "/api/v1/admin/resource", section: apikeys.SectionSettings},
	{prefix: "/api/v1/admin/sandbox", section: apikeys.SectionSettings},
	{prefix: "/api/v1/admin/search", section: apikeys.SectionSettings},
	{prefix: "/api/v1/admin/mode", section: apikeys.SectionSettings},
	{prefix: "/api/v1/admin/fault", section: apikeys.SectionSettings},
	{prefix: "/api/v1/admin/timeline", section: apikeys.SectionSettings},
	{prefix: "/api/v1/admin/severity", section: apikeys.SectionSettings},
	{prefix: "/api/v1/admin/budgets", section: apikeys.SectionSettings},
	{prefix: "/api/v1/stream", section: apikeys.SectionSettings},
	{prefix: "/admin/api/updates", section: apikeys.SectionSettings},
	{prefix: "/os/api/update/apply", section: apikeys.SectionSettings, action: apikeys.ActionApply},
	{prefix: "/os/api/update/rollback", section: apikeys.SectionSettings, action: apikeys.ActionApply},
	{prefix: "/os/api/update", section: apikeys.SectionSettings},
	{prefix: "/os/api/apikeys", section: apikeys.SectionSettings},
	{prefix: "/os/api/credentials", section: apikeys.SectionSettings},
	{prefix: "/os/api/mode", section: apikeys.SectionSettings},
	{prefix: "/os/api/budgets", section: apikeys.SectionSettings},
	{prefix: "/os/api/cmd-index", section: apikeys.SectionSettings},
	{prefix: "/admin/mode", section: apikeys.SectionSettings},
	{prefix: "/admin/fault", section: apikeys.SectionSettings},
	{prefix: "/admin/replay", section: apikeys.SectionSettings},
	{prefix: "/admin/benchmark", section: apikeys.SectionSettings},
	{prefix: "/admin/cache-purge", section: apikeys.SectionSettings},
	{prefix: "/admin/vacuum", section: apikeys.SectionSettings},
	{prefix: "/admin/search", section: apikeys.SectionSettings},
	{prefix: "/debug/pprof", section: apikeys.SectionSettings},
}

// sortedCapRules is capabilityRules ordered longest-prefix-first, computed once
// at init so lookup is a simple first-match scan (the table is small and the
// scan is branch-predictable; no per-request allocation).
var sortedCapRules = func() []capRule {
	rules := make([]capRule, len(capabilityRules))
	copy(rules, capabilityRules)
	sort.SliceStable(rules, func(i, j int) bool {
		return len(rules[i].prefix) > len(rules[j].prefix)
	})
	return rules
}()

// actionForMethod derives the capability action from the HTTP verb when a rule
// does not pin one explicitly.
func actionForMethod(method string) apikeys.Action {
	switch method {
	case http.MethodGet, http.MethodHead:
		return apikeys.ActionRead
	case http.MethodDelete:
		return apikeys.ActionDelete
	default: // POST, PUT, PATCH
		return apikeys.ActionWrite
	}
}

// capabilityFor resolves the capability a caller needs for (method, path).
// ok=false means the path is not mapped — the fail-closed default (superuser
// only) applies.
func capabilityFor(method, path string) (apikeys.Section, apikeys.Action, bool) {
	for _, rule := range sortedCapRules {
		if strings.HasPrefix(path, rule.prefix) {
			act := rule.action
			if act == "" {
				act = actionForMethod(method)
			}
			return rule.section, act, true
		}
	}
	return "", "", false
}

// requireAPIPermission is the VayuAPI enforcement middleware for the protected
// /api group. It runs after auth.RequireAPIKey (which stamped the caller's
// KeyInfo) and refuses the request unless the key holds the route's capability.
// Superuser keys (the bootstrap API_KEY, the internal system key, and every
// pre-migration key backfilled with a full grant) pass everything, so existing
// automation is unaffected; scoped keys get exactly what they were granted.
func (a *App) requireAPIPermission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ki, ok := auth.KeyInfoFromContext(r.Context())
		if !ok {
			// Unreachable when chained after RequireAPIKey; fail closed anyway.
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "no API-key identity", "/docs/compatibility/vayuapi")
			return
		}
		if !keyMayCall(ki, r.Method, r.URL.Path) {
			sec, act, mapped := capabilityFor(r.Method, r.URL.Path)
			need := "a full-access key (unmapped route)"
			if mapped {
				need = string(sec) + ":" + string(act)
			}
			writeAPIError(w, r, http.StatusForbidden, "insufficient_permissions",
				"this API key does not hold the required capability: "+need, "/docs/compatibility/vayuapi")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// keyMayCall is the single permission predicate shared by the /api middleware
// and the /os session-or-key gate: mapped routes require the route's capability;
// unmapped routes require a superuser key (fail-closed).
func keyMayCall(ki apikeys.KeyInfo, method, path string) bool {
	sec, act, mapped := capabilityFor(method, path)
	if !mapped {
		return ki.IsSuperuser()
	}
	return ki.Can(sec, act)
}
