// SPDX-License-Identifier: Apache-2.0

package main

// mcp_sites.go — the hosted-site tools an MCP client sees (ADR-0154 D10).
//
// These exist so an operator can say "build the website for client.example" to
// an assistant and have it happen, rather than filling a form field at a time.
// They are the same operations the per-site console offers, through the same
// validator (scopedWebsiteConfig) — a second one is how the two surfaces come to
// disagree about what a valid mode is, and a domain then serves nothing.
//
// THE PRIMARY IS NOT ADDRESSABLE HERE. Registry.SetSite refuses it, because the
// primary's website IS the install-wide Website settings; a tool that appeared
// to accept it and silently did nothing would be worse than one that says no.
// The list tool marks it so an assistant does not try.

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/customsite"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/mcp"
	"github.com/johalputt/vayupress/internal/render"
)

// registerSiteTools adds the per-domain website tools to the MCP server.
func (a *App) registerSiteTools(srv *mcp.Server) {
	srv.Register(mcp.Tool{
		Name: "list_sites",
		Description: "List every domain this install hosts, with what each one serves (blog or website), " +
			"its template and whether it is live. Use this first: every other site tool takes a host from here.",
		InputSchema: objSchema(nil, map[string]any{}),
		Visible:     a.mcpVisible(apikeys.SectionDomains, apikeys.ActionRead),
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			if a.domains == nil {
				return "", errDomainsUnavailable
			}
			list, err := a.domains.List(ctx)
			if err != nil {
				return "", err
			}
			out := make([]map[string]any, 0, len(list))
			for _, d := range list {
				row := map[string]any{
					"host":        d.Host,
					"serves":      scopedSiteMode(d),
					"status":      d.Status,
					"is_primary":  d.IsPrimary,
					"certificate": d.TLSState,
				}
				if s, ok := d.Site(); ok {
					row["template"] = s.Template
				}
				if d.IsPrimary {
					// Said plainly rather than discovered by a failing call.
					row["editable_here"] = false
					row["note"] = "the primary's website is managed from Website settings, not these tools"
				} else {
					row["editable_here"] = true
				}
				out = append(out, row)
			}
			return jsonStr(map[string]any{"sites": out}), nil
		},
	})

	srv.Register(mcp.Tool{
		Name: "get_site",
		Description: "Read one hosted site's full website configuration: what it serves, its template and " +
			"every content field. Call this before update_site so you edit from what is actually stored.",
		InputSchema: objSchema([]string{"host"}, map[string]any{
			"host": strProp("The hosted domain, exactly as list_sites reports it."),
		}),
		Visible: a.mcpVisible(apikeys.SectionDomains, apikeys.ActionRead),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct{ Host string }
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			d, err := a.mcpSiteByHost(ctx, in.Host)
			if err != nil {
				return "", err
			}
			site, _ := d.Site()
			content := bizsite.ParseContent(site.Content)
			return jsonStr(map[string]any{
				"host":     d.Host,
				"serves":   scopedSiteMode(d),
				"template": site.Template,
				"content":  content,
			}), nil
		},
	})

	srv.Register(mcp.Tool{
		Name: "update_site",
		Description: "Set what a hosted domain serves and the content of its website, in one call. " +
			"serves is blog, business (website at /, blog at blog.<host>) or business_subpath (website at /, " +
			"blog at /blog). Content fields you omit keep their stored value, so read with get_site first and " +
			"send back what you want changed. Published within seconds.",
		InputSchema: objSchema([]string{"host"}, map[string]any{
			"host":     strProp("The hosted domain to change."),
			"serves":   strProp("blog | business | business_subpath. Omit to keep the current setting."),
			"template": strProp("Design key — call list_site_templates for the catalogue. Omit to keep the current one."),
			"name":     strProp("Business name shown across the top."),
			"tagline":  strProp("One line under the name."),
			"about":    strProp("A paragraph or two, plain text."),
			"phone":    strProp("Contact phone."),
			"email":    strProp("Contact email."),
			"address":  strProp("Postal address."),
			"hours":    strProp("Opening hours."),
			"cta":      strProp("Hero button label."),
			"cta_link": strProp("Hero button target."),
			"hero_img": strProp("Hero image URL. Omit to use the template's own art."),
			"show_blog": map[string]any{
				"type":        "boolean",
				"description": "Link the blog from the website's navigation and footer.",
			},
		}),
		Visible: a.mcpVisible(apikeys.SectionDomains, apikeys.ActionWrite),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			// Pointers, so "field omitted" is distinguishable from "field set to
			// empty". Without that an assistant editing one line would blank
			// every other field on somebody's live website.
			var in struct {
				Host     string  `json:"host"`
				Serves   *string `json:"serves"`
				Template *string `json:"template"`
				Name     *string `json:"name"`
				Tagline  *string `json:"tagline"`
				About    *string `json:"about"`
				Phone    *string `json:"phone"`
				Email    *string `json:"email"`
				Address  *string `json:"address"`
				Hours    *string `json:"hours"`
				CTA      *string `json:"cta"`
				CTALink  *string `json:"cta_link"`
				HeroImg  *string `json:"hero_img"`
				ShowBlog *bool   `json:"show_blog"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			d, err := a.mcpSiteByHost(ctx, in.Host)
			if err != nil {
				return "", err
			}

			prev, _ := d.Site()
			c := bizsite.ParseContent(prev.Content)
			mode, template := scopedSiteMode(d), prev.Template
			if in.Serves != nil {
				mode = *in.Serves
			}
			if in.Template != nil {
				template = *in.Template
			}
			set := func(dst *string, src *string) {
				if src != nil {
					*dst = strings.TrimSpace(*src)
				}
			}
			set(&c.Name, in.Name)
			set(&c.Tagline, in.Tagline)
			set(&c.About, in.About)
			set(&c.Phone, in.Phone)
			set(&c.Email, in.Email)
			set(&c.Address, in.Address)
			set(&c.Hours, in.Hours)
			set(&c.CTA, in.CTA)
			set(&c.CTALink, in.CTALink)
			set(&c.HeroImg, in.HeroImg)
			if in.ShowBlog != nil {
				c.ShowBlog = *in.ShowBlog
			}

			cfg, err := scopedWebsiteConfig(d, mode, template, c)
			if err != nil {
				return "", err
			}
			if err := a.domains.SetSite(ctx, d.ID, cfg); err != nil {
				return "", err
			}
			render.CachePurgeAll()
			dbpkg.AuditLog("vayudomains.website.save", mcpActor(ctx), d.Host,
				"mode="+cfg.Mode+" template="+cfg.Template+" via=mcp")
			return jsonStr(map[string]any{
				"status": "published", "host": d.Host, "serves": cfg.Mode,
				"template": cfg.Template, "url": "https://" + d.Host + "/",
			}), nil
		},
	})

	srv.Register(mcp.Tool{
		Name:        "list_site_templates",
		Description: "The website designs available to a hosted site, with the kind of business each suits.",
		InputSchema: objSchema(nil, map[string]any{}),
		Visible:     a.mcpVisible(apikeys.SectionDomains, apikeys.ActionRead),
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) {
			out := make([]map[string]string, 0, len(bizsite.All()))
			for _, t := range bizsite.All() {
				out = append(out, map[string]string{
					"key": t.Key, "name": t.Name, "suits": t.Category, "description": t.Tagline,
				})
			}
			return jsonStr(map[string]any{"templates": out}), nil
		},
	})
}

// mcpSiteByHost resolves a hostname to a hosted secondary domain.
//
// The primary is refused BY NAME rather than by letting SetSite fail later, so
// an assistant is told the reason instead of receiving an opaque error it will
// retry. A host that matches nothing is likewise refused rather than defaulted —
// defaulting an unknown host to anything would let a typo edit a real site.
func (a *App) mcpSiteByHost(ctx context.Context, host string) (domain.Domain, error) {
	if a.domains == nil {
		return domain.Domain{}, errDomainsUnavailable
	}
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return domain.Domain{}, siteLookupError("a host is required — call list_sites for the hosts you can edit")
	}
	list, err := a.domains.List(ctx)
	if err != nil {
		return domain.Domain{}, err
	}
	for _, d := range list {
		if strings.ToLower(d.Host) != h {
			continue
		}
		if d.IsPrimary {
			return domain.Domain{}, siteLookupError(
				h + " is this install's primary domain; its website is managed from Website settings, not these tools")
		}
		return d, nil
	}
	return domain.Domain{}, siteLookupError("no hosted site named " + h + " — call list_sites for the hosts you can edit")
}

type siteLookupError string

func (e siteLookupError) Error() string { return string(e) }

var errDomainsUnavailable = siteLookupError("the domain registry is not available on this install")

// registerSiteBuilderTools adds the tools that BUILD a site rather than fill in
// a template's fields (ADR-0154 D12).
//
// The template tools configure a design somebody else drew — eight fields, one
// of eight layouts. This is the other thing: an assistant authors the HTML, CSS
// and assets itself and the result is served at the domain, which is what a site
// of the kind vayupress.com is.
//
// Every deploy goes through customsite.Deploy — the same path an uploaded zip
// takes, with the same os.Root confinement and traversal refusal. Writing files
// straight to disk here would have been a second implementation of the part that
// must never be wrong.
func (a *App) registerSiteBuilderTools(srv *mcp.Server) {
	srv.Register(mcp.Tool{
		Name: "build_site",
		Description: "Author a COMPLETE website for a hosted domain and publish it. Pass files as a map of " +
			"path to contents — index.html is required, and you may add any CSS, JS, SVG or other static " +
			"files alongside it. This REPLACES the site's current bundle atomically and switches the domain " +
			"to serve it; the previous bundle is kept and can be restored with restore_previous_site. Use " +
			"this when the site should be hand-built; use update_site when one of the templates is enough.",
		InputSchema: objSchema([]string{"host", "files"}, map[string]any{
			"host": strProp("The hosted domain to publish to. Call list_sites first."),
			"files": map[string]any{
				"type": "object",
				"description": "Path to contents. Paths are relative and may not escape the site root. " +
					"index.html is required. Example: {\"index.html\": \"<!doctype html>…\", " +
					"\"assets/site.css\": \"body{…}\"}.",
				"additionalProperties": map[string]any{"type": "string"},
			},
		}),
		Visible: a.mcpVisible(apikeys.SectionDomains, apikeys.ActionWrite),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Host  string            `json:"host"`
				Files map[string]string `json:"files"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			d, err := a.mcpSiteByHost(ctx, in.Host)
			if err != nil {
				return "", err
			}
			// index.html is required rather than defaulted. A bundle without an
			// entry point deploys cleanly and serves 404 at the root, which is a
			// site that looks published and is not.
			if !hasIndexHTML(in.Files) {
				return "", bundleError("index.html is required — without it the domain serves nothing at /")
			}
			zipData, err := zipFromFiles(in.Files)
			if err != nil {
				return "", err
			}
			m, err := customsite.Deploy(scopedBundleDir(d), zipData)
			if err != nil {
				return "", err
			}
			// Switch the domain to serve it. Deploying a site and leaving the
			// domain on its blog would be the "control that did nothing" defect:
			// the assistant reports success and the visitor sees the old site.
			cfg, err := scopedWebsiteConfig(d, "custom", "", bizsite.ParseContent(""))
			if err != nil {
				return "", err
			}
			if err := a.domains.SetSite(ctx, d.ID, cfg); err != nil {
				return "", err
			}
			render.CachePurgeAll()
			dbpkg.AuditLog("vayudomains.website.bundle", mcpActor(ctx), d.Host,
				"built "+itoaSafe(m.Files)+" file(s) via=mcp")
			return jsonStr(map[string]any{
				"status": "published", "host": d.Host, "files": m.Files, "bytes": m.Bytes,
				"url": "https://" + d.Host + "/", "serves": "the uploaded site",
			}), nil
		},
	})

	srv.Register(mcp.Tool{
		Name:        "restore_previous_site",
		Description: "Restore the bundle that was live before the last build_site or upload for a hosted domain.",
		InputSchema: objSchema([]string{"host"}, map[string]any{
			"host": strProp("The hosted domain to roll back."),
		}),
		Visible: a.mcpVisible(apikeys.SectionDomains, apikeys.ActionWrite),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct{ Host string }
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			d, err := a.mcpSiteByHost(ctx, in.Host)
			if err != nil {
				return "", err
			}
			if err := customsite.Rollback(scopedBundleDir(d)); err != nil {
				return "", err
			}
			render.CachePurgeAll()
			dbpkg.AuditLog("vayudomains.website.bundle", mcpActor(ctx), d.Host, "rolled back via=mcp")
			return jsonStr(map[string]any{"status": "restored", "host": d.Host}), nil
		},
	})
}

// hasIndexHTML reports whether the authored file set has a root entry point.
func hasIndexHTML(files map[string]string) bool {
	for name := range files {
		clean := strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "./")
		if strings.EqualFold(clean, "index.html") || strings.EqualFold(clean, "index.htm") {
			return true
		}
	}
	return false
}
