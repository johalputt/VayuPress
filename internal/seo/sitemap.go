package seo

import (
	"fmt"
	"strings"
)

// SitemapEntry holds data for one URL in the sitemap.
type SitemapEntry struct {
	Loc        string
	LastMod    string
	ChangeFreq string
	Priority   string
}

// GenerateSitemap returns a sitemap.xml XML string for the given entries.
func GenerateSitemap(entries []SitemapEntry) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, e := range entries {
		fmt.Fprintf(&sb, "  <url>\n    <loc>%s</loc>\n    <lastmod>%s</lastmod>\n    <changefreq>%s</changefreq>\n    <priority>%s</priority>\n  </url>\n",
			e.Loc, e.LastMod, e.ChangeFreq, e.Priority)
	}
	sb.WriteString(`</urlset>`)
	return sb.String()
}
