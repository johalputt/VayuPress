// SPDX-License-Identifier: Apache-2.0

package main

// site_checks.go — what is checked about a site document beyond its shape,
// against the install it will be served from: the publish gate.
//
// The validator (internal/sitedoc) knows only the document, so it can say a
// link is well formed but not that anything answers at it. These checks ask
// the install. An error stops a publish, since it is something a visitor
// would hit as a dead end; a warning is reported and publishes anyway. A
// draft save reports both without stopping, so a problem shows while it is
// being made rather than at the end.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/sitedoc"
)

// siteCheck is one finding, pointing at the field it is about in the
// validator's own path form so the editor can mark it.
type siteCheck struct {
	Level   string `json:"level"` // "error" or "warning"
	Path    string `json:"path"`
	Message string `json:"message"`
}

// errSiteChecks is a publish stopped by the first of its errors.
type errSiteChecks struct{ first siteCheck }

func (e errSiteChecks) Error() string { return e.first.Path + ": " + e.first.Message }

// pictureAt is a picture and the path of its src field.
type pictureAt struct {
	path string
	img  sitedoc.Image
}

// pageWeightBudget is the picture weight above which a page is flagged: a
// page heavier than this takes seconds to appear on a phone connection.
const pageWeightBudget = int64(3) << 20

// checkSite reports what would go wrong serving d on this install.
func (a *App) checkSite(ctx context.Context, scope string, d sitedoc.Document) []siteCheck {
	var out []siteCheck
	add := func(level, path, format string, args ...any) {
		out = append(out, siteCheck{Level: level, Path: path, Message: fmt.Sprintf(format, args...)})
	}
	pages := map[string]map[string]bool{}
	for _, p := range d.Pages {
		ids := map[string]bool{}
		for _, s := range p.Sections {
			ids[s.ID] = true
		}
		pages[p.Slug] = ids
	}
	hasContactEmail := false
	for _, p := range d.Pages {
		for _, s := range p.Sections {
			if s.Kind == sitedoc.KindContact && s.Email != "" {
				hasContactEmail = true
			}
		}
	}

	for i, p := range d.Pages {
		at := fmt.Sprintf("pages[%d]", i)
		var weight int64
		hasHeroText := false
		for j, s := range p.Sections {
			sat := fmt.Sprintf("%s.sections[%d]", at, j)
			if s.Kind == sitedoc.KindHero && s.Body != "" {
				hasHeroText = true
			}
			if s.CTALink != "" {
				if msg := a.linkProblem(ctx, scope, p.Slug, pages, s.CTALink); msg != "" {
					add("error", sat+".cta_link", "%s", msg)
				}
			}
			var images []pictureAt
			if s.Image != nil {
				images = append(images, pictureAt{sat + ".image.src", *s.Image})
			}
			for k, img := range s.Images {
				images = append(images, pictureAt{fmt.Sprintf("%s.images[%d].src", sat, k), img})
			}
			for _, im := range images {
				switch {
				case strings.HasPrefix(im.img.Src, "/media/"):
					size, ok := mediaFileSize(im.img.Src)
					if !ok {
						add("error", im.path, "%s is not in the media library — it would show as a broken picture", im.img.Src)
					}
					weight += size
				case strings.HasPrefix(im.img.Src, "https://") && config.Cfg.OnionMode:
					add("warning", im.path, "a picture from another site does not load on the Tor site, whose policy allows only its own — upload it to Media")
				}
			}
			if s.Form && !hasContactEmail && a.primaryContactEmail(ctx) == "" {
				add("warning", sat+".form", "no contact email is set here or in Settings, so messages from this form reach only the console's inbox")
			}
		}
		if p.Description == "" && !hasHeroText {
			add("warning", at+".description", "search engines and link previews have no description for this page")
		}
		if weight > pageWeightBudget {
			add("warning", at, "the pictures on this page weigh %s — above %s it is slow to appear on a phone",
				humanBytes(weight), humanBytes(pageWeightBudget))
		}
	}
	return out
}

// linkProblem says why following link from the page at fromSlug would reach
// nothing, or "" when it reaches something.
func (a *App) linkProblem(ctx context.Context, scope, fromSlug string, pages map[string]map[string]bool, link string) string {
	switch {
	case strings.HasPrefix(link, "#"):
		if id := link[1:]; id != "" && !pages[fromSlug][id] {
			return fmt.Sprintf("there is no section #%s on this page", id)
		}
		return ""
	case !strings.HasPrefix(link, "/"):
		return "" // another site, mail or phone: not this install's to answer
	}
	path, frag, _ := strings.Cut(link, "#")
	path, _, _ = strings.Cut(path, "?")
	slug := strings.Trim(path, "/")
	if ids, ok := pages[slug]; ok {
		if frag != "" && !ids[frag] {
			return fmt.Sprintf("the page /%s has no section #%s", slug, frag)
		}
		return ""
	}
	if mux, _ := a.rootHandler().(*chi.Mux); mux != nil {
		if pattern := mux.Find(chi.NewRouteContext(), http.MethodGet, path); pattern != "" && pattern != "/{slug}" {
			return ""
		}
	}
	if !strings.Contains(slug, "/") && slug != "" {
		var n int
		if err := dbpkg.Reader().QueryRowContext(ctx,
			`SELECT COUNT(1) FROM articles WHERE slug=? AND domain_id=? AND status='published'`, slug, scope).Scan(&n); err == nil && n > 0 {
			return ""
		}
	}
	return fmt.Sprintf("nothing on this site answers at %s — it would be a dead link", path)
}

// mediaFileSize is the size of a /media/<name> picture, and whether it exists.
func mediaFileSize(src string) (int64, bool) {
	name := strings.TrimPrefix(src, "/media/")
	if i := strings.IndexAny(name, "?#"); i >= 0 {
		name = name[:i]
	}
	if !storedMediaName.MatchString(name) {
		return 0, false
	}
	fi, err := os.Stat(filepath.Join(config.Cfg.MediaDir, name)) //nolint:gosec // name matched storedMediaName
	if err != nil || !fi.Mode().IsRegular() {
		return 0, false
	}
	return fi.Size(), true
}

func (a *App) primaryContactEmail(ctx context.Context) string {
	if a.siteSettings == nil {
		return ""
	}
	return strings.TrimSpace(a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyContactEmail))
}

// firstError is the first error-level finding, if any.
func firstError(checks []siteCheck) (siteCheck, bool) {
	for _, c := range checks {
		if c.Level == "error" {
			return c, true
		}
	}
	return siteCheck{}, false
}
