package main

// migrate_import.go — built-in importers for Ghost (JSON export) and WordPress
// (WXR/RSS XML export). These complement the Markdown importer so operators can
// move off the two most common platforms with no external tooling.

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// importedPost is the normalised shape both importers produce before insertion.
type importedPost struct {
	Title   string
	Slug    string
	HTML    string
	Tags    []string
	Created time.Time
	Draft   bool
}

// insertImported writes a batch of normalised posts into the articles table,
// honouring --dry-run and --skip-drafts. It mirrors the Markdown importer's
// sanitisation and dedupe-by-slug behaviour.
func insertImported(posts []importedPost, dryRun, skipDrafts bool) error {
	if dryRun {
		for i, p := range posts {
			flag := ""
			if p.Draft {
				flag = " (draft)"
			}
			fmt.Printf("[%3d/%d] %q  slug=%s  tags=%d%s\n", i+1, len(posts), p.Title, p.Slug, len(p.Tags), flag)
		}
		fmt.Printf("\nDry run: %d post(s) would be imported.\n", len(posts))
		return nil
	}
	db := dbpkg.DB
	if db == nil {
		return fmt.Errorf("database not initialised")
	}
	inserted, skipped := 0, 0
	for i, p := range posts {
		prefix := fmt.Sprintf("[%3d/%d]", i+1, len(posts))
		if skipDrafts && p.Draft {
			fmt.Printf("%s %q (draft, skipped)\n", prefix, p.Title)
			skipped++
			continue
		}
		id, err := newMigrateID()
		if err != nil {
			return err
		}
		dateStr := p.Created.UTC().Format(time.RFC3339)
		tagsJSON := "[]"
		if len(p.Tags) > 0 {
			tagsJSON = `["` + strings.Join(p.Tags, `","`) + `"]`
		}
		sanitised := bluemonday.UGCPolicy().Sanitize(p.HTML)
		res, err := db.Exec(
			`INSERT OR IGNORE INTO articles(id,title,slug,content,tags,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
			id, p.Title, p.Slug, sanitised, tagsJSON, dateStr, dateStr)
		if err != nil {
			fmt.Printf("%s %q → ERROR: %v\n", prefix, p.Title, err)
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			fmt.Printf("%s %q (already exists, skipped)\n", prefix, p.Title)
			skipped++
			continue
		}
		fmt.Printf("%s %q  slug=%s  ✓\n", prefix, p.Title, p.Slug)
		inserted++
	}
	fmt.Printf("\nDone. Inserted: %d  Skipped: %d\n", inserted, skipped)
	return nil
}

// ---- Ghost JSON export -------------------------------------------------------

// runMigrateGhost imports a Ghost export (the JSON downloaded from
// Settings → Labs → Export). It reads db.data.posts plus tag relations.
func runMigrateGhost(args []string) error {
	file := parseStringFlag(args, "file", "")
	if file == "" {
		return fmt.Errorf("--file <ghost-export.json> is required")
	}
	dryRun := parseBoolFlag(args, "dry-run", false)
	skipDrafts := parseBoolFlag(args, "skip-drafts", true)

	raw, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read export: %w", err)
	}
	posts, err := parseGhostExport(raw)
	if err != nil {
		return err
	}
	fmt.Printf("Parsed %d Ghost post(s) from %s\n\n", len(posts), file)
	return insertImported(posts, dryRun, skipDrafts)
}

// parseGhostExport extracts posts from a Ghost export JSON document. Ghost wraps
// the data as {"db":[{"data":{"posts":[...], "tags":[...], "posts_tags":[...]}}]}.
func parseGhostExport(raw []byte) ([]importedPost, error) {
	var doc struct {
		DB []struct {
			Data struct {
				Posts []struct {
					Title       string `json:"title"`
					Slug        string `json:"slug"`
					HTML        string `json:"html"`
					Status      string `json:"status"`
					PublishedAt string `json:"published_at"`
					CreatedAt   string `json:"created_at"`
					ID          string `json:"id"`
				} `json:"posts"`
				Tags []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"tags"`
				PostsTags []struct {
					PostID string `json:"post_id"`
					TagID  string `json:"tag_id"`
				} `json:"posts_tags"`
			} `json:"data"`
		} `json:"db"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse Ghost JSON: %w", err)
	}
	if len(doc.DB) == 0 {
		return nil, fmt.Errorf("no db section in Ghost export")
	}
	data := doc.DB[0].Data
	tagName := map[string]string{}
	for _, t := range data.Tags {
		tagName[t.ID] = t.Name
	}
	postTags := map[string][]string{}
	for _, pt := range data.PostsTags {
		if n, ok := tagName[pt.TagID]; ok {
			postTags[pt.PostID] = append(postTags[pt.PostID], n)
		}
	}
	var out []importedPost
	for _, p := range data.Posts {
		created := parseImportTime(p.PublishedAt, p.CreatedAt)
		slug := p.Slug
		if slug == "" {
			slug = migrateSlugify(p.Title)
		}
		out = append(out, importedPost{
			Title:   p.Title,
			Slug:    slug,
			HTML:    p.HTML,
			Tags:    postTags[p.ID],
			Created: created,
			Draft:   p.Status != "published",
		})
	}
	return out, nil
}

// ---- WordPress WXR (RSS) export ---------------------------------------------

// runMigrateWordPress imports a WordPress eXtended RSS (WXR) export file (Tools
// → Export → All content).
func runMigrateWordPress(args []string) error {
	file := parseStringFlag(args, "file", "")
	if file == "" {
		return fmt.Errorf("--file <wordpress-export.xml> is required")
	}
	dryRun := parseBoolFlag(args, "dry-run", false)
	skipDrafts := parseBoolFlag(args, "skip-drafts", true)

	raw, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read export: %w", err)
	}
	posts, err := parseWXR(raw)
	if err != nil {
		return err
	}
	fmt.Printf("Parsed %d WordPress post(s) from %s\n\n", len(posts), file)
	return insertImported(posts, dryRun, skipDrafts)
}

// parseWXR extracts published/draft posts of post_type "post" from a WXR file.
func parseWXR(raw []byte) ([]importedPost, error) {
	type wxrItem struct {
		Title      string   `xml:"title"`
		PostName   string   `xml:"post_name"`     // namespace wp:post_name
		PostType   string   `xml:"post_type"`     // wp:post_type
		Status     string   `xml:"status"`        // wp:status
		PostDate   string   `xml:"post_date_gmt"` // wp:post_date_gmt
		Encoded    []string `xml:"encoded"`       // content:encoded + excerpt:encoded
		Categories []struct {
			Domain string `xml:"domain,attr"`
			Value  string `xml:",chardata"`
		} `xml:"category"`
	}
	type wxr struct {
		Items []wxrItem `xml:"channel>item"`
	}
	var doc wxr
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse WXR XML: %w", err)
	}
	var out []importedPost
	for _, it := range doc.Items {
		if it.PostType != "" && it.PostType != "post" {
			continue // skip pages, attachments, nav items, etc.
		}
		html := ""
		if len(it.Encoded) > 0 {
			html = it.Encoded[0] // content:encoded is the first encoded element
		}
		var tags []string
		for _, c := range it.Categories {
			// Both "category" and "post_tag" share the <category> element; keep both.
			if c.Value != "" && (c.Domain == "category" || c.Domain == "post_tag") {
				tags = append(tags, c.Value)
			}
		}
		slug := it.PostName
		if slug == "" {
			slug = migrateSlugify(it.Title)
		}
		out = append(out, importedPost{
			Title:   it.Title,
			Slug:    slug,
			HTML:    html,
			Tags:    dedupeTags(tags),
			Created: parseImportTime(it.PostDate, ""),
			Draft:   it.Status != "publish",
		})
	}
	return out, nil
}

// ---- shared helpers ----------------------------------------------------------

func parseImportTime(primary, fallback string) time.Time {
	for _, s := range []string{primary, fallback} {
		s = strings.TrimSpace(s)
		if s == "" || s == "0000-00-00 00:00:00" {
			continue
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05.000Z"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC()
			}
		}
	}
	return time.Now().UTC()
}

func dedupeTags(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return out
}
