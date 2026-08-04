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
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
				// Reported because it was settable and not readable, which makes it
				// unverifiable: update_site returned "published" whether or not the
				// value had been stored, and there was no second call that could
				// tell the difference. A setting you can only write is a setting you
				// have to take on trust.
				"allow_eval": site.AllowEval,
				"content":    content,
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
			"allow_eval": map[string]any{
				"type": "boolean",
				"description": "Let THIS domain's uploaded site run a front-end library that " +
					"compiles its markup expressions at runtime. Off by default. It relaxes one " +
					"directive of the Content-Security-Policy for this domain's public pages only " +
					"— never the panel, the API or any path carrying a session, and never the " +
					"primary domain. Turn it on only for a static site you control; leave it off " +
					"for anything with a login or user-supplied content.",
			},
		}),
		Visible: a.mcpVisible(apikeys.SectionDomains, apikeys.ActionWrite),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			// Pointers, so "field omitted" is distinguishable from "field set to
			// empty". Without that an assistant editing one line would blank
			// every other field on somebody's live website.
			var in struct {
				Host      string    `json:"host"`
				Serves    *string   `json:"serves"`
				Template  *string   `json:"template"`
				Name      *string   `json:"name"`
				Tagline   *string   `json:"tagline"`
				About     *string   `json:"about"`
				Phone     *string   `json:"phone"`
				Email     *string   `json:"email"`
				Address   *string   `json:"address"`
				Hours     *string   `json:"hours"`
				CTA       *string   `json:"cta"`
				CTALink   *string   `json:"cta_link"`
				HeroImg   *string   `json:"hero_img"`
				ShowBlog  *flexBool `json:"show_blog"`
				AllowEval *flexBool `json:"allow_eval"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			d, err := a.mcpSiteByHost(ctx, in.Host)
			if err != nil {
				return "", err
			}

			if err := mcpSiteWritable(d); err != nil {
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
				c.ShowBlog = in.ShowBlog.Bool()
			}

			cfg, err := scopedWebsiteConfig(d, mode, template, c)
			if err != nil {
				return "", err
			}
			// Carried forward unless this call explicitly changes it. Rebuilding
			// the config from the form fields would otherwise silently re-tighten
			// the policy on a domain that had opted out, on the next unrelated
			// edit — a setting that turns itself off is worse than no setting.
			cfg.AllowEval = prev.AllowEval
			if in.AllowEval != nil {
				cfg.AllowEval = in.AllowEval.Bool()
			}
			if err := a.domains.SetSite(ctx, d.ID, cfg); err != nil {
				return "", err
			}
			if in.AllowEval != nil {
				dbpkg.AuditLog("vayudomains.website.alloweval", mcpActor(ctx), d.Host,
					"allow_eval="+strconv.FormatBool(cfg.AllowEval)+" via=mcp")
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

// mcpSiteWritable refuses a site an assistant must not publish to.
//
// mcpSiteByHost refuses only the primary, which is a question of ownership. This
// is a question of intent: a DISABLED domain is one the operator deliberately
// stopped serving, and a HELD one is deliberately unprovisioned. The console's
// own controls refuse both, and a connector that did not would be a way around
// the panel rather than another door into it.
func mcpSiteWritable(d domain.Domain) error {
	if d.Status != domain.StatusActive {
		return siteLookupError(d.Host + " is disabled — enable it under Lifecycle on its console " +
			"before publishing to it")
	}
	if !d.IsSyncApproved() {
		return siteLookupError(d.Host + " is on manual hold, so it is not being provisioned and " +
			"nothing published to it would be served — approve it under Lifecycle first")
	}
	return nil
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
			if err := mcpSiteWritable(d); err != nil {
				return "", err
			}
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
			// Preserving, not replacing: publishing a hand-built site says what
			// the domain SERVES, and must not erase the business details the
			// operator typed into the template.
			cfg, err := scopedWebsiteConfigPreserving(d, "custom", "")
			if err != nil {
				return "", err
			}
			if err := a.domains.SetSite(ctx, d.ID, cfg); err != nil {
				return "", err
			}
			render.CachePurgeAll()
			dbpkg.AuditLog("vayudomains.website.bundle", mcpActor(ctx), d.Host,
				"built "+itoaSafe(m.Files)+" file(s) via=mcp")
			resp := map[string]any{
				"status": "published", "host": d.Host, "files": m.Files, "bytes": m.Bytes,
				"url": "https://" + d.Host + "/", "serves": "the uploaded site",
			}
			// Publishing succeeded; whether the browser will RUN what was published
			// is a different question, and the operator has to be told the answer
			// here rather than discover a blank page.
			if w := cspBundleWarnings(in.Files); len(w) > 0 {
				resp["csp_warnings"] = w
				resp["note"] = "The bundle is live, but this install serves a strict " +
					"Content-Security-Policy and will refuse the resources listed in " +
					"csp_warnings. The page will render without them."
			}
			return jsonStr(resp), nil
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

// registerCertificateTools exposes the certificate diagnosis and the
// provisioning request through VayuMCP.
//
// WHY THESE EXIST, stated plainly because it is a product finding rather than a
// feature request: diagnosing one stuck certificate ran for a dozen rounds of
// screenshots. Every fact needed was inside the process the whole time, and the
// connector — which exists precisely so an assistant can inspect an install —
// could read posts and settings but not the one page the operator was stuck on.
// So the answer kept being inferred from an image instead of read from the
// server, and two of those inferences were wrong.
//
// Read-only diagnosis is separated from the action. Asking a server to run
// certbot is not a read: failed validations are rate-limited per hostname, so an
// unmetered trigger is a way to burn somebody's issuance budget. It carries the
// domain-write scope for that reason.
func (a *App) registerCertificateTools(srv *mcp.Server) {
	srv.Register(mcp.Tool{
		Name: "diagnose_certificate",
		Description: "Explain why a hosted domain has no TLS certificate: what the certificate " +
			"authority actually said, whether nginx routes this host's ACME challenge on port 80, " +
			"whether this server answers its own challenge, and what the last provisioning run did. " +
			"Returns the same checks the site console shows.",
		InputSchema: objSchema([]string{"host"}, map[string]any{
			"host": map[string]any{"type": "string", "description": "a host from list_sites"},
		}),
		Visible: a.mcpVisible(apikeys.SectionDomains, apikeys.ActionRead),
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var in struct {
				Host string `json:"host"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return "", err
			}
			d, err := a.mcpSiteByHost(ctx, in.Host)
			if err != nil {
				return "", err
			}
			logLines := provisionLogTail(provisionLogLines)
			checks := a.diagnoseCertificate(ctx, d, logLines)
			rows := make([]map[string]any, 0, len(checks))
			blocking := 0
			for _, c := range checks {
				if !c.OK && c.Fatal {
					blocking++
				}
				rows = append(rows, map[string]any{
					"check": c.Label, "ok": c.OK, "blocking": c.Fatal, "detail": c.Detail,
				})
			}
			return jsonStr(map[string]any{
				"host":        d.Host,
				"certificate": d.TLSState,
				"blocking":    blocking,
				"checks":      rows,
				// The worker's own words for this host, so nothing is summarised at
				// the reader — the same segment the console prints.
				"provisioning_log": hostLogSegment(logLines, d.Host),
			}), nil
		},
	})

	srv.Register(mcp.Tool{
		Name: "provision_certificates",
		Description: "Ask this server's privileged helper to obtain certificates and write vhosts " +
			"for every hosted domain whose DNS is pointed here. Runs in the background; call " +
			"diagnose_certificate afterwards to see what happened.",
		InputSchema: objSchema(nil, map[string]any{}),
		Visible:     a.mcpVisible(apikeys.SectionDomains, apikeys.ActionWrite),
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) {
			if !provisionUnitsInstalled() {
				return "", errProvisionUnavailable
			}
			path := filepath.Join(provisionStateDir(), provisionRequestFile)
			// Same delete-then-write as the console button, for the same systemd
			// reason: the watcher is a .path unit with PathExists=, which fires when
			// the file APPEARS. Rewriting a request that was never consumed produces
			// no trigger at all.
			rearmed := false
			if _, err := os.Stat(path); err == nil {
				rearmed = os.Remove(path) == nil
			}
			if err := os.WriteFile(path, nil, 0o644); err != nil { //nolint:gosec // an empty flag a root unit watches for
				return "", err
			}
			return jsonStr(map[string]any{
				"status": "requested", "rearmed": rearmed,
				"note": "the request carries no arguments and its contents are never read, so this " +
					"tool can ask for provisioning and cannot influence what the privileged step does",
			}), nil
		},
	})
}

var errProvisionUnavailable = mcpError("one-click provisioning is not installed on this server")

type mcpError string

func (e mcpError) Error() string { return string(e) }

// cspBundleWarnings reports what a strict-CSP install will silently REFUSE to
// load from an uploaded bundle.
//
// WHY THIS EXISTS. Cloning an existing site onto VayuPress is the obvious first
// thing an operator does, and most sites on the web load their CSS framework,
// their JS framework and their fonts from third-party hosts, with a small inline
// <script> to configure them. Every one of those is blocked here: the baseline
// policy is
//
//	script-src 'self' 'nonce-<per-request>'; style-src 'self'; font-src 'self'
//
// and a STATIC bundle cannot carry the per-request nonce, so even a first-party
// inline script is refused. The result is a page that publishes cleanly, reports
// success, and renders as unstyled text with no interactivity — with nothing
// anywhere saying why. That is the same silent failure this product has spent a
// long day removing from its provisioning path.
//
// It WARNS rather than refuses. The policy is not a mistake to be worked around,
// and some bundles reference an external origin in a place the CSP does not
// govern; an operator who understands the trade is not blocked from publishing.
// What they are not left with is a mystery.
//
// Deliberately NOT flagged: <a href> (navigation, ungoverned), and <img src>
// (img-src admits https:, so remote images genuinely work).
func cspBundleWarnings(files map[string]string) []string {
	var out []string
	ext := regexp.MustCompile(`^\s*(?:https?:)?//`)
	// The tag name is written `<scrip[t]` rather than `<script`. The character
	// class is semantically identical to the regex engine and keeps the literal
	// text out of the source, because TestEveryInlineScriptParses scans this
	// repository for inline script blocks and tries to PARSE them as JavaScript —
	// so a pattern that describes a script tag was picked up as if it were one,
	// and CI failed trying to run a regex as code. Do not "tidy" this back.
	scriptSrc := regexp.MustCompile(`(?is)<scrip[t][^>]*\ssrc\s*=\s*["']([^"']+)["']`)
	inlineScript := regexp.MustCompile(`(?is)<scrip[t](?:\s[^>]*)?>\s*([^\s<][\s\S]*?)</scrip[t]>`)
	linkHref := regexp.MustCompile(`(?is)<link[^>]*\shref\s*=\s*["']([^"']+)["'][^>]*>`)
	cssURL := regexp.MustCompile(`(?is)(?:@import\s+(?:url\()?|src\s*:\s*url\()\s*["']?([^"')]+)`)

	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}

	for path, body := range files {
		lower := strings.ToLower(path)
		switch {
		case strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm"):
			for _, m := range scriptSrc.FindAllStringSubmatch(body, -1) {
				if ext.MatchString(m[1]) {
					add("`" + path + "` loads a script from " + originOf(m[1]) + " — script-src is " +
						"'self', so it will not execute. Vendor the file into the bundle instead.")
				}
			}
			// An inline script cannot carry the per-request nonce from a static
			// file, so it is refused however trustworthy it is.
			for _, m := range inlineScript.FindAllStringSubmatch(body, -1) {
				if strings.Contains(strings.ToLower(m[0]), `type="application/ld+json"`) ||
					strings.Contains(strings.ToLower(m[0]), `type='application/ld+json'`) {
					continue // data, not executable code — never blocked
				}
				add("`" + path + "` contains an inline <script>. A static bundle cannot know the " +
					"per-request nonce, so it will not execute — move the code into a same-origin " +
					".js file and load it with <script src>.")
				break
			}
			for _, m := range linkHref.FindAllStringSubmatch(body, -1) {
				if !ext.MatchString(m[1]) {
					continue
				}
				tag := strings.ToLower(m[0])
				if strings.Contains(tag, "stylesheet") {
					add("`" + path + "` loads a stylesheet from " + originOf(m[1]) + " — style-src " +
						"is 'self', so it will not apply. Vendor the CSS into the bundle instead.")
				}
			}
		case strings.HasSuffix(lower, ".css"):
			for _, m := range cssURL.FindAllStringSubmatch(body, -1) {
				if ext.MatchString(m[1]) {
					add("`" + path + "` pulls " + originOf(m[1]) + " — font-src and style-src are " +
						"'self', so a remote font or stylesheet will not load. Embed it in the bundle.")
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// originOf renders just the host of a URL for a message, so a warning stays
// readable when the URL is a long versioned path.
func originOf(u string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	s = strings.TrimPrefix(s, "//")
	if i := strings.IndexAny(s, "/?#"); i > 0 {
		s = s[:i]
	}
	if s == "" {
		return "another origin"
	}
	return s
}
