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

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/mcp"
)

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

	return srv
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
