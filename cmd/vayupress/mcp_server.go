package main

// mcp_server.go — VayuMCP host wiring (ADR-0139). Builds the Model Context
// Protocol tool registry over VayuPress's existing services and mounts it at
// POST /mcp. Auth reuses the scoped-key model: RequireAPIKey stamps the caller's
// KeyInfo, and every tool declares the section:action it needs — checked with
// KeyInfo.Can before the tool is listed or called. So a connector is exactly as
// powerful as its key: a superuser ("*:*") key exposes every tool ("full
// control"); a scoped key exposes only what it grants.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/mcp"
	"github.com/johalputt/vayupress/internal/settings"
)

// mcpPublicSettingKeys is the allowlist of presentational, non-sensitive site
// settings that the site_settings tool exposes. It is deliberately NOT the whole
// settings map: GetAll also returns operational config an operator would never
// want handed to a connector — tor.bridges (private obfs4 bridge lines with
// embedded secrets), the shield.* protection thresholds, and other runtime knobs.
// Projecting onto this list keeps the tool's contract ("non-secret site
// configuration") literally true regardless of what else lives in the store.
var mcpPublicSettingKeys = []string{
	settings.KeySiteName,
	settings.KeySiteTagline,
	settings.KeySiteDescription,
	settings.KeySiteAuthor,
	settings.KeyAuthorBio,
	settings.KeyThemePrimaryLight,
	settings.KeyThemePrimaryDark,
	settings.KeyThemeAccentLight,
	settings.KeyThemeAccentDark,
	settings.KeyThemeCustomCSS,
	settings.KeyThemeOGImage,
	settings.KeyHeadKeywords,
	settings.KeyHeadThemeColor,
	settings.KeyHeadRobots,
	settings.KeyNavItems,
	settings.KeyFooterConfig,
	settings.KeyHomeHero,
	settings.KeyFeatureComments,
	settings.KeyMembershipButtons,
}

// mountMCP registers the VayuMCP connector endpoint unless disabled by
// VAYUOS_MCP=off. The endpoint is authenticated (RequireAPIKey) and rate-limited
// like the rest of the API; per-tool capability checks stand in for the URL-based
// requireAPIPermission (one URL, many tools).
func (a *App) mountMCP(r chi.Router) {
	if config.EnvOr("VAYUOS_MCP", "on") == "off" {
		return
	}
	srv := a.buildMCPServer()
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAPIKey, auth.RateLimitMiddleware, a.apiUsageMiddleware)
		r.Post("/mcp", srv.ServeHTTP)
	})
}

// buildMCPServer constructs the tool registry. Each tool is a thin adapter over
// an already-tested service, gated by the caller's key grant.
func (a *App) buildMCPServer() *mcp.Server {
	srv := mcp.NewServer("VayuPress", Version)

	// site_info — available to any valid key; a cheap connectivity/identity probe.
	srv.Register(mcp.Tool{
		Name:        "site_info",
		Description: "Return this VayuPress site's name, primary domain, and version. Use it to confirm the connection.",
		InputSchema: mcp.NewObjectSchema(),
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) {
			return jsonStr(map[string]string{
				"platform": "VayuPress",
				"version":  Version,
				"domain":   config.Cfg.Domain,
			}), nil
		},
	})

	// ── posts ────────────────────────────────────────────────────────────────
	srv.Register(mcp.Tool{
		Name:        "create_post",
		Description: "Create and publish a blog post. content is HTML (sanitized server-side). Returns the queued id and slug; the post is live within seconds at /<slug>.",
		InputSchema: objSchema([]string{"title", "content"}, map[string]any{
			"title":   strProp("Post title (1–500 chars)."),
			"slug":    strProp("URL slug (lowercase, hyphenated). Omit to auto-derive from the title."),
			"content": strProp("Post body as HTML."),
			"tags":    arrProp("Optional tags (max 20)."),
		}),
		Visible: a.mcpVisible(apikeys.SectionPosts, apikeys.ActionWrite),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Title, Slug, Content string
				Tags                 []string
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			res, err := a.articles.Create(ctx, in.Title, in.Slug, in.Content, in.Tags)
			if err != nil {
				return "", err
			}
			dbpkg.AuditLog("article.create", mcpActor(ctx), res.Slug, "id="+res.ID+" via=mcp")
			return jsonStr(map[string]string{"status": "queued", "id": res.ID, "slug": res.Slug, "url": "/" + res.Slug}), nil
		},
	})

	srv.Register(mcp.Tool{
		Name:        "update_post",
		Description: "Update an existing post by slug. Only the fields you pass change; omitted fields are left as-is.",
		InputSchema: objSchema([]string{"slug"}, map[string]any{
			"slug":    strProp("Slug of the post to update."),
			"title":   strProp("New title (optional)."),
			"content": strProp("New HTML body (optional)."),
			"tags":    arrProp("Replacement tags (optional)."),
		}),
		Visible: a.mcpVisible(apikeys.SectionPosts, apikeys.ActionWrite),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Slug    string   `json:"slug"`
				Title   *string  `json:"title"`
				Content *string  `json:"content"`
				Tags    []string `json:"tags"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			art, err := a.articles.Update(ctx, in.Slug, in.Title, in.Content, in.Tags)
			if err != nil {
				return "", err
			}
			dbpkg.AuditLog("article.update", mcpActor(ctx), art.Slug, "id="+art.ID+" via=mcp")
			return jsonStr(map[string]string{"status": "queued", "slug": art.Slug}), nil
		},
	})

	srv.Register(mcp.Tool{
		Name:        "delete_post",
		Description: "Delete a post by slug.",
		InputSchema: objSchema([]string{"slug"}, map[string]any{"slug": strProp("Slug of the post to delete.")}),
		Visible:     a.mcpVisible(apikeys.SectionPosts, apikeys.ActionDelete),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Slug string `json:"slug"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			art, err := a.articles.Delete(ctx, in.Slug)
			if err != nil {
				return "", err
			}
			dbpkg.AuditLog("article.delete", mcpActor(ctx), in.Slug, "id="+art.ID+" via=mcp")
			return jsonStr(map[string]string{"status": "queued", "slug": in.Slug}), nil
		},
	})

	srv.Register(mcp.Tool{
		Name:        "list_posts",
		Description: "List published posts (newest first). Supports pagination and an optional tag filter.",
		InputSchema: objSchema(nil, map[string]any{
			"page":  intProp("Page number (default 1)."),
			"limit": intProp("Items per page (default 20, max 100)."),
			"tag":   strProp("Filter to a single tag (optional)."),
		}),
		Visible: a.mcpVisible(apikeys.SectionPosts, apikeys.ActionRead),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Page  int    `json:"page"`
				Limit int    `json:"limit"`
				Tag   string `json:"tag"`
			}
			_ = json.Unmarshal(args, &in)
			if in.Page <= 0 {
				in.Page = 1
			}
			if in.Limit <= 0 {
				in.Limit = 20
			}
			res, err := a.articles.List(ctx, in.Page, in.Limit, in.Tag)
			if err != nil {
				return "", err
			}
			return jsonStr(res), nil
		},
	})

	srv.Register(mcp.Tool{
		Name:        "get_post",
		Description: "Fetch a single post (including its HTML content) by slug.",
		InputSchema: objSchema([]string{"slug"}, map[string]any{"slug": strProp("Slug of the post.")}),
		Visible:     a.mcpVisible(apikeys.SectionPosts, apikeys.ActionRead),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Slug string `json:"slug"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			art, err := a.articles.Get(ctx, in.Slug)
			if err != nil {
				return "", err
			}
			return jsonStr(art), nil
		},
	})

	srv.Register(mcp.Tool{
		Name:        "search_content",
		Description: "Full-text search across published posts. Returns matching titles and slugs.",
		InputSchema: objSchema([]string{"query"}, map[string]any{
			"query": strProp("Search query."),
			"limit": intProp("Max results (default 10)."),
		}),
		Visible: a.mcpVisible(apikeys.SectionPosts, apikeys.ActionRead),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			if in.Limit <= 0 {
				in.Limit = 10
			}
			res, err := a.search.Search(ctx, in.Query, in.Limit)
			if err != nil {
				return "", err
			}
			return jsonStr(res), nil
		},
	})

	// ── pages ──────────────────────────────────────────────────────────────────
	// A page is a standalone article (is_page=1) that renders at /<slug> but is
	// excluded from the blog feed — About, Contact, landing pages, the building
	// blocks of a "custom website". create_page sets the flag atomically through
	// the write pipeline. Reading/editing/removing a page reuses the slug-based
	// post tools (get_post/update_post/delete_post work on any article).
	srv.Register(mcp.Tool{
		Name:        "create_page",
		Description: "Create a standalone page (not a blog post) — e.g. About, Contact, or a landing page. content is HTML (sanitized server-side). Returns the slug; the page is live at /<slug> and does NOT appear in the blog feed. Use update_post/get_post/delete_post (by slug) to edit, read, or remove it.",
		InputSchema: objSchema([]string{"title", "content"}, map[string]any{
			"title":   strProp("Page title (1–500 chars)."),
			"slug":    strProp("URL slug (lowercase, hyphenated). Omit to auto-derive from the title."),
			"content": strProp("Page body as HTML."),
			"tags":    arrProp("Optional tags (max 20)."),
		}),
		Visible: a.mcpVisible(apikeys.SectionPosts, apikeys.ActionWrite),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Title, Slug, Content string
				Tags                 []string
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			res, err := a.articles.CreatePage(ctx, in.Title, in.Slug, in.Content, in.Tags)
			if err != nil {
				return "", err
			}
			dbpkg.AuditLog("page.create", mcpActor(ctx), res.Slug, "id="+res.ID+" via=mcp")
			return jsonStr(map[string]string{"status": "queued", "id": res.ID, "slug": res.Slug, "url": "/" + res.Slug}), nil
		},
	})

	srv.Register(mcp.Tool{
		Name:        "list_pages",
		Description: "List standalone pages (is_page=1, newest-updated first). Pages do not appear in list_posts. Returns titles and slugs; read a page with get_post.",
		InputSchema: objSchema(nil, map[string]any{
			"limit": intProp("Max pages to return (default 100, max 1000)."),
		}),
		Visible: a.mcpVisible(apikeys.SectionPosts, apikeys.ActionRead),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Limit int `json:"limit"`
			}
			_ = json.Unmarshal(args, &in)
			if in.Limit <= 0 {
				in.Limit = 100
			}
			res, err := a.articles.ListPages(ctx, in.Limit)
			if err != nil {
				return "", err
			}
			return jsonStr(res), nil
		},
	})

	// ── site configuration (read) ─────────────────────────────────────────────
	// site_settings lets Claude read the site's own configuration — name,
	// tagline, theme colours, navigation, SEO meta — so it can reason about and
	// describe the site before proposing changes. Read-only: it never returns a
	// third-party secret (those live in the separate encrypted credential store,
	// not here) and it never mutates. Writing settings back is a Stage 3 tool that
	// must also drive the live-render refresh, so it is intentionally not exposed
	// yet.
	srv.Register(mcp.Tool{
		Name:        "site_settings",
		Description: "Read this site's presentational configuration: name, tagline, description, theme colours, navigation, footer, and SEO meta. Returns a key→value map of non-secret settings. Use it to understand the current site before suggesting changes.",
		InputSchema: mcp.NewObjectSchema(),
		Visible:     a.mcpVisible(apikeys.SectionSettings, apikeys.ActionRead),
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			if a.siteSettings == nil {
				return "", fmt.Errorf("site settings are unavailable")
			}
			all, err := a.siteSettings.GetAll(ctx)
			if err != nil {
				return "", err
			}
			// Project onto the presentational allowlist — never the raw GetAll dump,
			// which also carries operational config (tor.bridges, shield.* thresholds)
			// that must not reach a connector.
			return jsonStr(projectPublicSettings(all)), nil
		},
	})

	// update_site_settings — the write counterpart to site_settings. It lets Claude
	// rebrand and restyle the site (name, tagline, theme colours, navigation,
	// footer, custom CSS, SEO meta) — real "build my site" power — while staying
	// bounded to the SAME presentational allowlist the read tool exposes. Operational
	// keys (tor.bridges, shield.* thresholds) are never writable here, even though the
	// underlying SetMany would accept any known key; and SetMany itself still drops
	// anything outside the settings vocabulary. After writing, the live render
	// pipeline is refreshed (shared helper) so the change is visible immediately.
	srv.Register(mcp.Tool{
		Name:        "update_site_settings",
		Description: "Update the site's presentational configuration (branding, theme colours, navigation, footer, custom CSS, SEO meta). Pass a map of key→value; keys outside the allowlist are ignored. Changes apply live. Allowed keys: " + strings.Join(mcpPublicSettingKeys, ", ") + ".",
		InputSchema: objSchema([]string{"settings"}, map[string]any{
			"settings": map[string]any{
				"type":                 "object",
				"description":          "Map of setting key → new value. Only allowlisted presentational keys are applied; any other key is ignored.",
				"additionalProperties": map[string]any{"type": "string"},
			},
		}),
		Visible: a.mcpVisible(apikeys.SectionSettings, apikeys.ActionWrite),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			if a.siteSettings == nil {
				return "", fmt.Errorf("site settings are unavailable")
			}
			var in struct {
				Settings map[string]string `json:"settings"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			// Restrict to the presentational allowlist BEFORE writing — a settings:write
			// connector must not be able to reach operational keys.
			apply, ignored := partitionAllowedSettings(in.Settings)
			if len(apply) == 0 {
				return "", fmt.Errorf("no updatable settings provided; allowed keys: %s", strings.Join(mcpPublicSettingKeys, ", "))
			}
			if err := a.siteSettings.SetMany(ctx, apply); err != nil {
				return "", err
			}
			a.reloadRenderSettings(ctx)
			applied := make([]string, 0, len(apply))
			for k := range apply {
				applied = append(applied, k)
			}
			sort.Strings(applied)
			sort.Strings(ignored)
			dbpkg.AuditLog("settings.update", mcpActor(ctx), strings.Join(applied, ","), "via=mcp")
			return jsonStr(map[string]any{"status": "ok", "applied": applied, "ignored": ignored}), nil
		},
	})

	// ── analytics (read) ──────────────────────────────────────────────────────
	// analytics_summary gives Claude the privacy-first traffic picture (aggregate
	// counts only — VayuPress stores no per-visitor PII) so it can report on and
	// optimise the site. Read-only.
	srv.Register(mcp.Tool{
		Name:        "analytics_summary",
		Description: "Return a privacy-first traffic summary for the last N days: total pageviews, unique visitors, visits, bounce rate, average visit duration, and the top pages. Aggregate counts only — VayuPress stores no per-visitor personal data.",
		InputSchema: objSchema(nil, map[string]any{
			"days":  intProp("Look-back window in days (default 30, max 365)."),
			"limit": intProp("How many top pages to return (default 10, max 50)."),
		}),
		Visible: a.mcpVisible(apikeys.SectionAnalytics, apikeys.ActionRead),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			if a.analytics == nil {
				return "", fmt.Errorf("analytics are unavailable")
			}
			var in struct {
				Days  int `json:"days"`
				Limit int `json:"limit"`
			}
			_ = json.Unmarshal(args, &in)
			if in.Days <= 0 {
				in.Days = 30
			}
			if in.Days > 365 {
				in.Days = 365
			}
			if in.Limit <= 0 {
				in.Limit = 10
			}
			if in.Limit > 50 {
				in.Limit = 50
			}
			overview, err := a.analytics.OverviewSince(ctx, in.Days)
			if err != nil {
				return "", err
			}
			topPages, err := a.analytics.TopPages(ctx, in.Days, in.Limit)
			if err != nil {
				return "", err
			}
			return jsonStr(map[string]any{
				"days":      in.Days,
				"overview":  overview,
				"top_pages": topPages,
			}), nil
		},
	})

	return srv
}

// projectPublicSettings returns only the presentational, non-sensitive settings
// from a full GetAll map. Anything not on mcpPublicSettingKeys (tor.bridges,
// shield.* thresholds, and every other operational key) is dropped, so the
// site_settings tool can never leak operational config to a connector.
func projectPublicSettings(all map[string]string) map[string]string {
	out := make(map[string]string, len(mcpPublicSettingKeys))
	for _, k := range mcpPublicSettingKeys {
		if v, ok := all[k]; ok {
			out[k] = v
		}
	}
	return out
}

// partitionAllowedSettings splits a requested settings map into the keys that
// update_site_settings is permitted to write (the presentational allowlist) and
// the keys it must ignore (everything else — operational config, unknown keys).
// This is the write-side guard: an operational key such as tor.bridges or a
// shield.* threshold can never be applied through the tool even if requested.
func partitionAllowedSettings(in map[string]string) (apply map[string]string, ignored []string) {
	allowed := make(map[string]bool, len(mcpPublicSettingKeys))
	for _, k := range mcpPublicSettingKeys {
		allowed[k] = true
	}
	apply = make(map[string]string)
	for k, v := range in {
		if allowed[k] {
			apply[k] = v
		} else {
			ignored = append(ignored, k)
		}
	}
	return apply, ignored
}

// mcpVisible returns a Visible closure that passes only when the request's key
// grants section:action — the same check requireAPIPermission does per-route.
func (a *App) mcpVisible(sec apikeys.Section, act apikeys.Action) func(context.Context) bool {
	return func(ctx context.Context) bool {
		ki, ok := auth.KeyInfoFromContext(ctx)
		return ok && ki.Can(sec, act)
	}
}

// mcpActor labels an audit row with the calling key's STABLE, UNIQUE id (never
// the raw secret, and never the mutable/non-unique label — two keys can share a
// label, which would make a destructive action unattributable). The label, when
// present, is appended only as a human hint.
func mcpActor(ctx context.Context) string {
	ki, ok := auth.KeyInfoFromContext(ctx)
	if !ok {
		return "mcp"
	}
	actor := "mcp:" + ki.ID
	if ki.Label != "" {
		actor += " (" + ki.Label + ")"
	}
	return actor
}

// ── small schema/format helpers ──────────────────────────────────────────────

func objSchema(required []string, props map[string]any) map[string]any {
	s := mcp.NewObjectSchema()
	s["properties"] = props
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func arrProp(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

func jsonStr(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"could not encode result"}`
	}
	return string(b)
}

func errBadArgs(err error) error { return fmt.Errorf("invalid arguments: %w", err) }
