// SPDX-License-Identifier: Apache-2.0

package main

// halcyon_preview.go — Theme Studio's preview of Halcyon.
//
// Every other theme is previewed as a fixed sample page whose stylesheet is
// swapped as the operator edits it, which is all a CSS-only theme can change.
// Halcyon's options change the page itself (which Home composition, which
// article layout, which sections), so its preview is the real page, rendered
// from this site's own posts with the options being edited: Home, a topic, the
// newest post, a search and the sign-in page.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/theme"
)

// halcyonPreviewPages are the pages the Studio can preview, by the name its
// page switch sends.
var halcyonPreviewPages = map[string]bool{"home": true, "topic": true, "article": true, "search": true, "signin": true}

func (a *App) writeHalcyonPreview(w http.ResponseWriter, r *http.Request, tok theme.Tokens, cssHref string) {
	cfg := theme.ResolveHalcyon(tok.Options)
	// The Studio's scheme switch shows each scheme as a reader with that
	// appearance would see it.
	if s := r.URL.Query().Get("scheme"); s == "light" || s == "dark" {
		cfg.Appearance = s
	}
	page := r.URL.Query().Get("page")
	if !halcyonPreviewPages[page] {
		page = "home"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	r = r.WithContext(ctx)

	var p render.HalcyonPreview
	topics, topicTotal := a.halcyonTopics("", a.tagIndexGlobal)
	topic := ""
	for _, t := range topics {
		if !render.Ubiquitous(t.Count, topicTotal) {
			topic = t.Name
			break
		}
	}
	switch page {
	case "topic":
		if topic == "" {
			page = "home"
			break
		}
		arts, total := a.articlesByTag(ctx, topic, 200, "", false)
		p.Topic = render.TopicInput{Domain: config.Cfg.Domain, Version: Version, Tag: topic, Articles: arts,
			Total: total, Topics: topics, TopicTotal: topicTotal}
	case "article":
		var art dbpkg.Article
		var tags string
		if err := dbpkg.Reader().QueryRowContext(ctx,
			`SELECT id,title,slug,content,tags,created_at,updated_at,status FROM articles WHERE status='published' AND is_page=0 ORDER BY created_at DESC LIMIT 1`).
			Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tags, &art.CreatedAt, &art.UpdatedAt, &art.Status); err == nil {
			art.Tags = api.SplitTags(tags)
			p.Article = &art
			p.Related = a.halcyonRelated(r, art.Slug, art.Tags)
			p.Overrides = postOverrides(ctx, art.Slug)
		}
	case "search":
		p.Query = strings.ReplaceAll(topic, "-", " ")
		if p.Query != "" && a.search != nil {
			if res, err := a.searchScoped(ctx, r, p.Query, 30); err == nil {
				for _, h := range res.Hits {
					p.Hits = append(p.Hits, render.SearchHit{Title: h.Title, Slug: h.Slug, Tags: h.Tags, CreatedAt: h.CreatedAt})
				}
			}
		}
	case "signin":
		p.SignIn = a.memberSigninPage(r)
	}
	if page == "home" || page == "article" && p.Article == nil {
		var total int
		dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM articles WHERE status='published' AND is_page=0`).Scan(&total)
		p.Home = a.homeInput(r, 1, total, max(1, (total+homeFeedPageSize-1)/homeFeedPageSize), "", nil, true)
	}

	out, err := render.RenderHalcyonPreview(page, cfg, p, cssHref)
	if err != nil {
		http.Error(w, "preview error", http.StatusInternalServerError)
		return
	}
	writeOSHTML(w, r, out)
}
