// SPDX-License-Identifier: Apache-2.0

package main

// mcp_site_documents.go — the connector's view of a hosted site as a document
// (ADR-0161): read it, save a draft, publish, restore. The same validator and
// the same publish path as the console editor, so an assistant cannot put on
// a page anything the editor could not.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/bizsite"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/mcp"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/sitedoc"
)

// hostedSiteDocument is what a hosted site serves as a document, and where
// that came from: its draft, its newest publish, or its legacy content.
func hostedSiteDocument(ctx context.Context, d domain.Domain) (doc sitedoc.Document, source string) {
	if dd, _, ok := siteDraft(ctx, d.ID); ok {
		return dd, "draft"
	}
	if dd, ok := publishedSiteDoc(ctx, d.ID); ok {
		return dd, "published"
	}
	site, _ := d.Site()
	tpl := bizsite.ByKey(site.Template)
	return sitedoc.FromLegacy(tpl, bizsite.EffectiveContent(tpl, site.Content)), "legacy"
}

// siteDocSchema describes the document for a client, briefly: the validator
// is the authority and names any field it refuses.
const siteDocSchema = "A site document: {\"v\":1, \"name\": business name, \"show_blog\": bool, \"pages\": [" +
	"{\"slug\": \"\" for home or lowercase-hyphen address, \"title\", \"description\", \"in_nav\": bool, " +
	"\"sections\": [{\"id\": anchor, \"kind\": hero|text|items|gallery|contact, \"nav\": menu label, \"heading\", ...}]}]}. " +
	"hero: eyebrow, body (one-line tagline), cta, cta_link, image {src, alt} — first section only. " +
	"text: body (one paragraph per line). items: items [{title, desc, price}]. " +
	"gallery: images [{src, alt}] — alt is REQUIRED. contact: phone, email, address, hours, form (bool). " +
	"Links: https, http, mailto:, tel:, /path or #anchor. Images: /media/… or https. " +
	"Call get_site_document first and edit what it returns."

func (a *App) registerSiteDocumentTools(srv *mcp.Server) {
	visibleRead := a.mcpVisible(apikeys.SectionDomains, apikeys.ActionRead)
	visibleWrite := a.mcpVisible(apikeys.SectionDomains, apikeys.ActionWrite)

	srv.Register(mcp.Tool{
		Name: "get_site_document",
		Description: "Read a hosted site's website as a document of pages and sections — the draft if one is " +
			"saved, else what is live — with its publish history. Edit the returned document and send it to " +
			"save_site_draft or publish_site_document.",
		InputSchema: objSchema([]string{"host"}, map[string]any{"host": strProp("The hosted domain. Call list_sites first.")}),
		Visible:     visibleRead,
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct{ Host string }
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			d, err := a.mcpSiteByHost(ctx, in.Host)
			if err != nil {
				return "", err
			}
			doc, source := hostedSiteDocument(ctx, d)
			revs, err := siteRevisions(ctx, d.ID)
			if err != nil {
				return "", err
			}
			return jsonStr(map[string]any{"host": d.Host, "source": source, "doc": doc, "revisions": revs,
				"schema": siteDocSchema}), nil
		},
	})

	docArgs := func(required []string) map[string]any {
		return objSchema(required, map[string]any{
			"host": strProp("The hosted domain."),
			"doc":  map[string]any{"type": "object", "description": siteDocSchema},
		})
	}
	parseDocArg := func(raw json.RawMessage) (sitedoc.Document, error) {
		if len(raw) == 0 {
			return sitedoc.Document{}, errors.New("doc is required")
		}
		return sitedoc.Parse(raw)
	}

	srv.Register(mcp.Tool{
		Name:        "save_site_draft",
		Description: "Save a hosted site's document as its draft without publishing it. Refusals name the field that failed.",
		InputSchema: docArgs([]string{"host", "doc"}),
		Visible:     visibleWrite,
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Host string
				Doc  json.RawMessage
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			d, err := a.mcpSiteByHost(ctx, in.Host)
			if err != nil {
				return "", err
			}
			doc, err := parseDocArg(in.Doc)
			if err != nil {
				return "", err
			}
			at, err := saveSiteDraft(ctx, d.ID, doc, mcpActor(ctx))
			if err != nil {
				return "", err
			}
			return jsonStr(map[string]any{"status": "draft saved", "host": d.Host, "saved_at": at,
				"checks": nonNilChecks(a.checkSite(ctx, d.ID, doc))}), nil
		},
	})

	srv.Register(mcp.Tool{
		Name: "publish_site_document",
		Description: "Publish a hosted site's document: the one sent, or its saved draft when none is sent. It " +
			"becomes the live website and the domain is switched to serve it (blog stays at /blog). Every " +
			"publish is kept in history; restore_site_revision brings an earlier one back. Every page is then " +
			"fetched as a visitor would: verified is true only when each answered with the page just published " +
			"and everything it loads.",
		InputSchema: docArgs([]string{"host"}),
		Visible:     visibleWrite,
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Host string
				Doc  json.RawMessage
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
			var doc sitedoc.Document
			if len(in.Doc) > 0 {
				if doc, err = parseDocArg(in.Doc); err != nil {
					return "", err
				}
			} else {
				var ok bool
				if doc, _, ok = siteDraft(ctx, d.ID); !ok {
					return "", errors.New("there is no saved draft to publish — send doc, or call save_site_draft first")
				}
			}
			return a.mcpPublishSiteDoc(ctx, d, doc, "published")
		},
	})

	srv.Register(mcp.Tool{
		Name:        "restore_site_revision",
		Description: "Publish an earlier revision of a hosted site's document again. The revision ids come from get_site_document.",
		InputSchema: objSchema([]string{"host", "revision"}, map[string]any{
			"host":     strProp("The hosted domain."),
			"revision": map[string]any{"type": "integer", "description": "A revision id from get_site_document."},
		}),
		Visible: visibleWrite,
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Host     string
				Revision int64
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
			doc, err := siteRevisionDoc(ctx, d.ID, in.Revision)
			if err != nil {
				return "", err
			}
			return a.mcpPublishSiteDoc(ctx, d, doc, "restored")
		},
	})
}

// mcpPublishSiteDoc publishes and, if the domain was serving something else,
// switches it to serve its website — a publish the visitor cannot see would be
// the assistant reporting success for a change nobody gets.
func (a *App) mcpPublishSiteDoc(ctx context.Context, d domain.Domain, doc sitedoc.Document, verb string) (string, error) {
	id, checks, err := a.publishSite(ctx, d.ID, doc, mcpActor(ctx))
	if err != nil {
		return "", err
	}
	serves := scopedSiteMode(d)
	if serves != "business" && serves != "business_subpath" {
		site, _ := d.Site()
		cfg, err := scopedWebsiteConfigPreserving(d, "business_subpath", site.Template)
		if err != nil {
			return "", err
		}
		if err := a.domains.SetSite(ctx, d.ID, cfg); err != nil {
			return "", err
		}
		serves = "business_subpath"
	}
	render.CachePurgeAll()
	dbpkg.AuditLog("website.document", mcpActor(ctx), d.Host, verb+" revision "+itoaSafe(int(id))+" via=mcp")
	visits, verified := a.visitSitePages(ctx, d, doc)
	return jsonStr(map[string]any{"status": verb, "host": d.Host, "revision": id,
		"url": "https://" + d.Host + "/", "serves": serves, "checks": nonNilChecks(checks),
		"verified": verified, "pages": visits}), nil
}

// sitePageVisit is what a visitor to one page of a site got.
type sitePageVisit struct {
	Path     string   `json:"path"`
	Status   int      `json:"status"`
	Problems []string `json:"problems,omitempty"`
}

// visitSitePages fetches every page of doc from this server as a visitor to
// d would, straight after a publish: what each answered, whether it is the
// page just published, and what preview_site finds wrong in what it loads.
// It is the evidence that a publish reached visitors, rather than a report
// that the database took it. The page is recognised by its title, taken from
// the renderer rather than restated here.
func (a *App) visitSitePages(ctx context.Context, d domain.Domain, doc sitedoc.Document) ([]sitePageVisit, bool) {
	all := true
	out := make([]sitePageVisit, 0, len(doc.Pages))
	for _, p := range doc.Pages {
		v := sitePageVisit{Path: "/" + p.Slug}
		pv, err := a.previewSite(ctx, d, v.Path)
		switch {
		case err != nil:
			v.Problems = []string{err.Error()}
		case pv.Status != http.StatusOK:
			v.Status = pv.Status
			v.Problems = []string{fmt.Sprintf("a visitor gets HTTP %d", pv.Status)}
		default:
			v.Status = pv.Status
			v.Problems = pv.Problems
			if m := rePreviewTitle.FindStringSubmatch(sitedoc.Render(doc, p, sitedoc.Options{})); m != nil && strings.TrimSpace(m[1]) != pv.Title {
				v.Problems = append(v.Problems, fmt.Sprintf("a visitor gets %q, not the page just published (%q)", pv.Title, strings.TrimSpace(m[1])))
			}
		}
		all = all && len(v.Problems) == 0
		out = append(out, v)
	}
	return out, all
}
