// SPDX-License-Identifier: Apache-2.0

package main

// mcp_site_edits.go — changing a site document a section at a time.
//
// save_site_draft takes the whole document: to change one phone number an
// assistant reads every page and writes every page back, and a field it
// drops on the way is content gone from the site. edit_site_document names
// only what changes. The list is applied to the draft (else what is live)
// as one change — validated whole, stored only if every entry applied.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/mcp"
	"github.com/johalputt/vayupress/internal/sitedoc"
)

// siteChange is one entry of edit_site_document's list.
type siteChange struct {
	Op      string                     `json:"op"`
	Page    string                     `json:"page"`
	Section string                     `json:"section"`
	After   string                     `json:"after"`
	Fields  map[string]json.RawMessage `json:"fields"`
}

// mergeFields sets the named fields of v and decodes the result strictly: a
// misspelt field is refused by name rather than dropped. A field sent as null
// decodes to nothing, which is how one is removed.
func mergeFields[T any](v T, fields map[string]json.RawMessage) (T, error) {
	var out T
	raw, err := json.Marshal(v)
	if err != nil {
		return out, err
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return out, err
	}
	for k, f := range fields {
		m[k] = f
	}
	if raw, err = json.Marshal(m); err != nil {
		return out, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return out, dec.Decode(&out)
}

func findSitePage(doc sitedoc.Document, slug string) (int, error) {
	var have []string
	for i, p := range doc.Pages {
		if p.Slug == slug {
			return i, nil
		}
		have = append(have, fmt.Sprintf("%q", p.Slug))
	}
	return 0, fmt.Errorf("there is no page %q (the pages are %s; \"\" is the home page)", slug, strings.Join(have, ", "))
}

func findSiteSection(p sitedoc.Page, id string) (int, error) {
	var have []string
	for i, s := range p.Sections {
		if s.ID == id {
			return i, nil
		}
		have = append(have, s.ID)
	}
	return 0, fmt.Errorf("page %q has no section %q (its sections are %s)", p.Slug, id, strings.Join(have, ", "))
}

// applySiteChange applies one change to doc. Validation of the result is the
// caller's: a change may leave the document invalid on its own and valid
// after the next one.
func applySiteChange(doc *sitedoc.Document, c siteChange) error {
	if c.Op == "add_page" {
		p, err := mergeFields(sitedoc.Page{}, c.Fields)
		if err != nil {
			return err
		}
		doc.Pages = append(doc.Pages, p)
		return nil
	}
	pi, err := findSitePage(*doc, c.Page)
	if err != nil {
		return err
	}
	page := &doc.Pages[pi]
	switch c.Op {
	case "set_page":
		np, err := mergeFields(*page, c.Fields)
		if err != nil {
			return err
		}
		*page = np
	case "set_section":
		si, err := findSiteSection(*page, c.Section)
		if err != nil {
			return err
		}
		ns, err := mergeFields(page.Sections[si], c.Fields)
		if err != nil {
			return err
		}
		page.Sections[si] = ns
	case "add_section":
		ns, err := mergeFields(sitedoc.Section{}, c.Fields)
		if err != nil {
			return err
		}
		at := len(page.Sections)
		if c.After != "" {
			si, err := findSiteSection(*page, c.After)
			if err != nil {
				return err
			}
			at = si + 1
		}
		page.Sections = slices.Insert(page.Sections, at, ns)
	case "remove_section":
		si, err := findSiteSection(*page, c.Section)
		if err != nil {
			return err
		}
		page.Sections = slices.Delete(page.Sections, si, si+1)
	default:
		return fmt.Errorf("op %q is not one of set_section, add_section, remove_section, set_page, add_page", c.Op)
	}
	return nil
}

// siteOutline is the shape of a document — its pages and their sections —
// so an assistant can address the next change without reading it all.
func siteOutline(doc sitedoc.Document) []map[string]any {
	out := make([]map[string]any, 0, len(doc.Pages))
	for _, p := range doc.Pages {
		secs := make([]string, 0, len(p.Sections))
		for _, s := range p.Sections {
			secs = append(secs, s.ID+" ("+string(s.Kind)+")")
		}
		out = append(out, map[string]any{"page": p.Slug, "title": p.Title, "sections": secs})
	}
	return out
}

func (a *App) registerSiteEditTool(srv *mcp.Server) {
	srv.Register(mcp.Tool{
		Name: "edit_site_document",
		Description: "Change parts of a hosted site's document without sending all of it: set fields of a " +
			"section or page, add or remove a section, add a page. The changes apply in order to the draft " +
			"(or what is live when there is none) and are kept only if all of them apply and the result is " +
			"valid. Saved as the draft, or published with publish=true — a publish fetches every page as a " +
			"visitor would and reports what they got.",
		InputSchema: objSchema([]string{"host", "changes"}, map[string]any{
			"host": strProp("The hosted domain."),
			"changes": map[string]any{"type": "array", "items": map[string]any{
				"type": "object",
				"description": "{op, page, section, after, fields}. op: set_section | add_section | remove_section | " +
					"set_page | add_page. page: the page's slug (\"\" or omitted for home). section: the section id " +
					"(set_section, remove_section). after: for add_section, the id to insert after (omitted: at the " +
					"end). fields: for set_*, the fields to change (a field given an empty value is cleared; a list " +
					"such as items is replaced whole); for add_*, the new section or page. Field names are those of " +
					"get_site_document.",
			}},
			"publish": map[string]any{"type": "boolean", "description": "Publish the result instead of saving it as the draft."},
		}),
		Visible: a.mcpVisible(apikeys.SectionDomains, apikeys.ActionWrite),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Host    string       `json:"host"`
				Changes []siteChange `json:"changes"`
				Publish *flexBool    `json:"publish"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", errBadArgs(err)
			}
			if len(in.Changes) == 0 {
				return "", fmt.Errorf("changes is empty — nothing to do")
			}
			d, err := a.mcpSiteByHost(ctx, in.Host)
			if err != nil {
				return "", err
			}
			doc, _ := hostedSiteDocument(ctx, d)
			for i, c := range in.Changes {
				if err := applySiteChange(&doc, c); err != nil {
					return "", fmt.Errorf("changes[%d] (%s): %w — nothing was changed", i, c.Op, err)
				}
			}
			if in.Publish.Bool() {
				if err := mcpSiteWritable(d); err != nil {
					return "", err
				}
				return a.mcpPublishSiteDoc(ctx, d, doc, "published")
			}
			at, err := saveSiteDraft(ctx, d.ID, doc, mcpActor(ctx))
			if err != nil {
				return "", err
			}
			return jsonStr(map[string]any{"status": "draft saved", "host": d.Host, "saved_at": at,
				"outline": siteOutline(doc), "checks": nonNilChecks(a.checkSite(ctx, d.ID, doc))}), nil
		},
	})
}
