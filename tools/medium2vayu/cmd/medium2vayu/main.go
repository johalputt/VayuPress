// medium2vayu — migrate a Medium HTML export into VayuPress SQLite.
//
// Usage:
//
//	medium2vayu import \
//	  --input medium-export.zip \
//	  --vayu-db /path/to/vayupress.db \
//	  --skip-drafts
//
// Medium exports a ZIP containing one HTML file per post.
// This tool parses each post, preserves the content HTML, and inserts it
// into the VayuPress articles table with INSERT OR IGNORE for idempotency.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/johalputt/medium2vayu/internal/mediumparse"
	"github.com/johalputt/medium2vayu/internal/vacuum"
	"github.com/spf13/cobra"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var (
	flagInput      string
	flagVayuDB     string
	flagSkipDrafts bool
	flagDryRun     bool
)

var rootCmd = &cobra.Command{
	Use:   "medium2vayu",
	Short: "Migrate a Medium HTML export into VayuPress SQLite",
	Long: `medium2vayu reads a Medium export (ZIP or directory of HTML files)
and imports all posts into a VayuPress SQLite database.

Medium HTML content is passed through and sanitized by VayuPress on render.
Original slugs are preserved. Migration is idempotent — re-running is safe.`,
}

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import posts from a Medium export",
	RunE:  runImport,
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List posts found in the export without importing",
	RunE:  runList,
}

func init() {
	for _, cmd := range []*cobra.Command{importCmd, listCmd} {
		cmd.Flags().StringVar(&flagInput, "input", "", "Path to Medium export ZIP file or directory (required)")
		cmd.MarkFlagRequired("input")
		cmd.Flags().BoolVar(&flagSkipDrafts, "skip-drafts", true, "Skip draft posts")
	}
	importCmd.Flags().StringVar(&flagVayuDB, "vayu-db", "vayupress.db", "Path to VayuPress SQLite database")
	importCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Parse but do not write to database")
	rootCmd.AddCommand(importCmd, listCmd)
}

func loadArticles() ([]mediumparse.Article, error) {
	info, err := os.Stat(flagInput)
	if err != nil {
		return nil, fmt.Errorf("input path: %w", err)
	}
	if info.IsDir() {
		return mediumparse.ParseDir(flagInput, flagSkipDrafts)
	}
	if strings.ToLower(filepath.Ext(flagInput)) == ".zip" {
		return mediumparse.ParseZIP(flagInput, flagSkipDrafts)
	}
	// Single HTML file.
	data, err := os.ReadFile(flagInput)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	a, err := mediumparse.ParseHTML(string(data), filepath.Base(flagInput))
	if err != nil {
		return nil, err
	}
	return []mediumparse.Article{a}, nil
}

func runList(cmd *cobra.Command, _ []string) error {
	articles, err := loadArticles()
	if err != nil {
		return err
	}
	fmt.Printf("Found %d posts in %s\n\n", len(articles), flagInput)
	for _, a := range articles {
		draftMark := ""
		if a.IsDraft {
			draftMark = " [DRAFT]"
		}
		fmt.Printf("  %-40s %s%s\n", a.Slug, a.Title, draftMark)
	}
	return nil
}

func runImport(cmd *cobra.Command, _ []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	articles, err := loadArticles()
	if err != nil {
		return fmt.Errorf("parse export: %w", err)
	}
	fmt.Printf("→ Found %d posts in %s\n\n", len(articles), flagInput)

	var writer *vacuum.Writer
	if !flagDryRun {
		writer, err = vacuum.Open(flagVayuDB)
		if err != nil {
			return fmt.Errorf("open VayuPress DB: %w", err)
		}
		defer writer.Close()
	}

	var inserted, skipped, errs int
	for i, a := range articles {
		if ctx.Err() != nil {
			fmt.Println("\n⚠ Interrupted")
			break
		}

		draftMark := ""
		if a.IsDraft {
			draftMark = " [draft]"
		}
		if flagDryRun {
			fmt.Printf("  [%d/%d] %-40s %q%s\n", i+1, len(articles), a.Slug, truncate(a.Title, 40), draftMark)
			inserted++
			continue
		}

		ok, err := writer.Insert(ctx, vacuum.Article{
			Title:     a.Title,
			Slug:      a.Slug,
			Content:   a.Content,
			Tags:      a.Tags,
			CreatedAt: a.CreatedAt,
			UpdatedAt: a.CreatedAt,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ERR [%d/%d] %s: %v\n", i+1, len(articles), a.Slug, err)
			errs++
			continue
		}
		if ok {
			fmt.Printf("  ✓ [%d/%d] %s — %q%s\n", i+1, len(articles), a.Slug, truncate(a.Title, 40), draftMark)
			inserted++
		} else {
			fmt.Printf("  ~ [%d/%d] %s (already exists, skipped)\n", i+1, len(articles), a.Slug)
			skipped++
		}
	}

	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Inserted : %d\n", inserted)
	fmt.Printf("  Skipped  : %d (already existed)\n", skipped)
	fmt.Printf("  Errors   : %d\n", errs)
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
