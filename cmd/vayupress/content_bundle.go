// SPDX-License-Identifier: Apache-2.0

package main

// content_bundle.go — VayuPress-native content bundle export/import (ADR-0141).
//
// This is the sync/migrate primitive for moving content between two SEPARATE
// VayuPress installs — e.g. a public Clearnet install and an anonymous Tor
// install. A bundle is a single, checksummed JSON file that can be moved fully
// offline (even by USB), which is the safe path for an air-gapped anonymous box.
//
// CONTENT ONLY, by design: posts and pages (title, slug, content, tags, excerpt,
// feature image, flags, dates) cross — NEVER accounts, mailboxes, PGP keys,
// sessions or Talk identities. Importing identity would let the two installs be
// correlated and de-anonymise the Tor one. author_id is deliberately not carried.
//
//   vayupress migrate export --file site.vaybundle [--include-pages=false]
//   vayupress migrate import --file site.vaybundle [--mode=merge|add-only] [--dry-run]

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// bundleFormat is the on-disk format tag; import refuses anything else.
const bundleFormat = "vaybundle/1"

// bundlePost is one content record. Content-only — no identity fields.
type bundlePost struct {
	Title        string   `json:"title"`
	Slug         string   `json:"slug"`
	Content      string   `json:"content"`
	Tags         []string `json:"tags"`
	Excerpt      string   `json:"excerpt,omitempty"`
	FeatureImage string   `json:"featureImage,omitempty"`
	Featured     bool     `json:"featured,omitempty"`
	IsPage       bool     `json:"isPage,omitempty"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
}

// contentBundle is the portable, checksummed export moved between installs.
type contentBundle struct {
	Format    string       `json:"format"`
	Kind      string       `json:"kind"`
	Generator string       `json:"generator"`
	Count     int          `json:"count"`
	Checksum  string       `json:"checksum"`
	Posts     []bundlePost `json:"posts"`
}

// bundleChecksum is sha256 over the canonical JSON of the posts slice, so import
// detects a truncated or tampered bundle before writing anything.
func bundleChecksum(posts []bundlePost) (string, error) {
	raw, err := json.Marshal(posts)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// exportBundle reads the content-only posts from db into a checksummed bundle.
func exportBundle(db *sql.DB, includePages bool) (contentBundle, error) {
	if db == nil {
		return contentBundle{}, fmt.Errorf("database not initialised")
	}
	q := `SELECT title, slug, content, COALESCE(tags,'[]'), COALESCE(excerpt,''),
	             COALESCE(feature_image,''), COALESCE(featured,0), COALESCE(is_page,0),
	             created_at, updated_at
	      FROM articles`
	if !includePages {
		q += ` WHERE COALESCE(is_page,0)=0`
	}
	q += ` ORDER BY created_at`
	rows, err := db.Query(q)
	if err != nil {
		return contentBundle{}, fmt.Errorf("query articles: %w", err)
	}
	defer rows.Close()

	posts := []bundlePost{}
	for rows.Next() {
		var p bundlePost
		var tagsJSON, createdS, updatedS string
		var featured, isPage int
		if err := rows.Scan(&p.Title, &p.Slug, &p.Content, &tagsJSON, &p.Excerpt,
			&p.FeatureImage, &featured, &isPage, &createdS, &updatedS); err != nil {
			return contentBundle{}, fmt.Errorf("scan: %w", err)
		}
		p.Tags = []string{}
		_ = json.Unmarshal([]byte(tagsJSON), &p.Tags)
		if p.Tags == nil {
			p.Tags = []string{}
		}
		p.Featured = featured != 0
		p.IsPage = isPage != 0
		p.CreatedAt = parseImportTime(createdS, "").Format(time.RFC3339)
		p.UpdatedAt = parseImportTime(updatedS, createdS).Format(time.RFC3339)
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return contentBundle{}, fmt.Errorf("rows: %w", err)
	}
	sum, err := bundleChecksum(posts)
	if err != nil {
		return contentBundle{}, err
	}
	return contentBundle{
		Format:    bundleFormat,
		Kind:      "content",
		Generator: "VayuPress " + Version,
		Count:     len(posts),
		Checksum:  sum,
		Posts:     posts,
	}, nil
}

// verifyBundle checks the format tag and (when present) the content checksum.
func verifyBundle(b contentBundle) error {
	if b.Format != bundleFormat {
		return fmt.Errorf("unsupported bundle format %q (want %q)", b.Format, bundleFormat)
	}
	if b.Checksum == "" {
		return nil // unsigned bundle — allowed, integrity simply not verified
	}
	want, err := bundleChecksum(b.Posts)
	if err != nil {
		return err
	}
	if b.Checksum != want {
		return fmt.Errorf("bundle checksum mismatch — file is corrupt or tampered")
	}
	return nil
}

// importBundle writes a bundle's content into db by slug. mode "merge" upserts
// (new inserted, existing updated); "add-only" inserts new and leaves existing
// untouched. Content is re-sanitised on the way in. Returns counts.
func importBundle(db *sql.DB, b contentBundle, mode string, dryRun bool, out io.Writer) (ins, upd, skip int, err error) {
	if mode != "merge" && mode != "add-only" {
		return 0, 0, 0, fmt.Errorf("--mode must be 'merge' or 'add-only'")
	}
	if !dryRun && db == nil {
		return 0, 0, 0, fmt.Errorf("database not initialised")
	}
	for i, p := range b.Posts {
		slug := strings.TrimSpace(p.Slug)
		if slug == "" {
			slug = migrateSlugify(p.Title)
		}
		prefix := fmt.Sprintf("[%3d/%d]", i+1, len(b.Posts))

		var exists bool
		if !dryRun {
			var one int
			switch db.QueryRow(`SELECT 1 FROM articles WHERE slug=?`, slug).Scan(&one) {
			case nil:
				exists = true
			case sql.ErrNoRows:
				exists = false
			default:
				// treat a scan error as non-existent; the write below will surface any real problem
			}
		}
		if exists && mode == "add-only" {
			skip++
			fmt.Fprintf(out, "%s %q (exists, skipped)\n", prefix, p.Title)
			continue
		}
		if dryRun {
			fmt.Fprintf(out, "%s %q slug=%s%s\n", prefix, p.Title, slug, pageLabel(p.IsPage))
			ins++ // dry-run counts everything as "would import"
			continue
		}

		sanitised := bluemonday.UGCPolicy().Sanitize(p.Content)
		tagsJSON := "[]"
		if len(p.Tags) > 0 {
			if tb, mErr := json.Marshal(p.Tags); mErr == nil {
				tagsJSON = string(tb)
			}
		}
		created := parseImportTime(p.CreatedAt, "").UTC().Format(time.RFC3339)
		updated := parseImportTime(p.UpdatedAt, p.CreatedAt).UTC().Format(time.RFC3339)

		if exists {
			if _, uErr := db.Exec(`UPDATE articles SET title=?, content=?, tags=?, excerpt=?,
			        feature_image=?, featured=?, is_page=?, updated_at=? WHERE slug=?`,
				p.Title, sanitised, tagsJSON, p.Excerpt, p.FeatureImage,
				boolToInt(p.Featured), boolToInt(p.IsPage), updated, slug); uErr != nil {
				fmt.Fprintf(out, "%s %q → ERROR: %v\n", prefix, p.Title, uErr)
				continue
			}
			upd++
			fmt.Fprintf(out, "%s %q ✓ updated\n", prefix, p.Title)
			continue
		}

		id, idErr := newMigrateID()
		if idErr != nil {
			return ins, upd, skip, idErr
		}
		if _, iErr := db.Exec(`INSERT INTO articles(id,title,slug,content,tags,excerpt,
		        feature_image,featured,is_page,created_at,updated_at)
		        VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id, p.Title, slug, sanitised, tagsJSON, p.Excerpt, p.FeatureImage,
			boolToInt(p.Featured), boolToInt(p.IsPage), created, updated); iErr != nil {
			fmt.Fprintf(out, "%s %q → ERROR: %v\n", prefix, p.Title, iErr)
			continue
		}
		ins++
		fmt.Fprintf(out, "%s %q ✓ new\n", prefix, p.Title)
	}
	return ins, upd, skip, nil
}

func pageLabel(isPage bool) string {
	if isPage {
		return " (page)"
	}
	return ""
}

// ---- CLI wrappers -----------------------------------------------------------

func runExportBundle(args []string) error {
	file := parseStringFlag(args, "file", "")
	if file == "" {
		return fmt.Errorf("--file <bundle.vaybundle> is required")
	}
	includePages := parseBoolFlag(args, "include-pages", true)
	b, err := exportBundle(dbpkg.DB, includePages)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(file, raw, 0o600); err != nil { //nosec G703 -- operator-supplied CLI bundle path; the operator controls the host filesystem
		return fmt.Errorf("write bundle: %w", err)
	}
	fmt.Printf("Exported %d content item(s) to %s\n  %s\n", b.Count, file, b.Checksum)
	printBundleOmissions(os.Stderr, bundleOmissions(dbpkg.DB))
	return nil
}

func runImportBundle(args []string) error {
	file := parseStringFlag(args, "file", "")
	if file == "" {
		return fmt.Errorf("--file <bundle.vaybundle> is required")
	}
	mode := parseStringFlag(args, "mode", "merge")
	dryRun := parseBoolFlag(args, "dry-run", false)

	raw, err := os.ReadFile(file) //nosec G703 -- operator-supplied CLI bundle path; the operator controls the host filesystem
	if err != nil {
		return fmt.Errorf("read bundle: %w", err)
	}
	var b contentBundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return fmt.Errorf("parse bundle: %w", err)
	}
	if err := verifyBundle(b); err != nil {
		return err
	}
	fmt.Printf("Bundle: %d content item(s), mode=%s%s\n\n", len(b.Posts), mode, dryRunLabelText(dryRun))
	ins, upd, skip, err := importBundle(dbpkg.DB, b, mode, dryRun, os.Stdout)
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Printf("\nDry run: %d item(s) would be imported (%s).\n", ins, mode)
		return nil
	}
	fmt.Printf("\nDone. Inserted: %d  Updated: %d  Skipped: %d\n", ins, upd, skip)
	// Stated on arrival as well as on departure. An operator who did not run the
	// export — a bundle handed to them, or one made months ago — reads this line
	// on the install that will be missing the automations, which is the moment it
	// is worth knowing.
	printBundleOmissions(os.Stderr, bundleOmissions(nil))
	return nil
}

func dryRunLabelText(dryRun bool) string {
	if dryRun {
		return "  (dry run)"
	}
	return ""
}

// bundleOmissions names what exists on THIS install and is deliberately not
// travelling in the bundle.
//
// The omission itself is correct and settled: a content bundle is content-only
// (ADR-0141), and a flow in particular carries an owner account id that would
// not resolve on the target — importing one would store a flow that saves
// cleanly and then refuses on every single fire, for a reason nothing on the
// target's panel could explain. Accounts, mailboxes, PGP keys and Talk IDs never
// cross for the same family of reasons.
//
// What was wrong was the SILENCE. "Exported 412 content item(s)" is a sentence
// an operator reads as "the install is in this file", and they find out
// otherwise when the automation they relied on does not fire on the new host.
// A correct omission that nobody is told about is indistinguishable from data
// loss at the moment it matters.
//
// It COUNTS rather than reciting a fixed list, so the line only appears when
// there is something real to lose and names how much.
func bundleOmissions(db *sql.DB) []string {
	var out []string
	if db != nil {
		var flows int
		// Best effort: an install predating the automation tables has no such
		// table, and a missing count must not fail an export that is otherwise
		// fine. Silence here is safe because zero flows is the same message as
		// no table — there is nothing of that kind to leave behind.
		if err := db.QueryRow(`SELECT COUNT(*) FROM vayuflow_flows`).Scan(&flows); err == nil && flows > 0 {
			out = append(out, fmt.Sprintf(
				"%d automation(s) — a flow borrows an account's authority, and that account does not "+
					"exist on the target, so it cannot travel in a content bundle", flows))
		}
	}
	// Stated whether or not this install has any, because an operator planning a
	// move needs to know the shape of what is missing before they discover it.
	out = append(out,
		"accounts, mailboxes, PGP private keys and Talk identities — these never cross between "+
			"installs by design",
		"uploaded media files — the bundle carries content rows, not the files they reference")
	return out
}

// printBundleOmissions writes the omissions under a heading an operator will
// read. Written to stderr so a piped export still shows it.
func printBundleOmissions(w io.Writer, omissions []string) {
	if len(omissions) == 0 {
		return
	}
	fmt.Fprintln(w, "\nNOT in this bundle:")
	for _, o := range omissions {
		fmt.Fprintln(w, "  - "+o)
	}
	fmt.Fprintln(w, "A full-install move is the encrypted backup, not a content bundle.")
}
