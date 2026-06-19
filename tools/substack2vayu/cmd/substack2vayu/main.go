package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/johalputt/substack2vayu/internal/substackparse"
	"github.com/johalputt/substack2vayu/internal/vacuum"
	"github.com/spf13/cobra"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var (
		csvPath    string
		vayuDB     string
		skipDrafts bool
		dryRun     bool
		resume     bool
	)

	root := &cobra.Command{
		Use:   "substack2vayu",
		Short: "Import Substack CSV exports into a Vayu database",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImport(cmd.Context(), csvPath, vayuDB, skipDrafts, dryRun, resume)
		},
	}

	root.PersistentFlags().StringVar(&csvPath, "csv", "", "path to Substack posts.csv (required for import)")
	root.PersistentFlags().StringVar(&vayuDB, "vayu-db", "vayu.db", "path to Vayu SQLite database")
	root.PersistentFlags().BoolVar(&skipDrafts, "skip-drafts", true, "skip draft posts")
	root.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "parse and preview without writing to DB")
	root.PersistentFlags().BoolVar(&resume, "resume", true, "resume from last checkpoint")

	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Import posts from a Substack CSV export",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImport(cmd.Context(), csvPath, vayuDB, skipDrafts, dryRun, resume)
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List articles in the Vayu database",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd.Context(), vayuDB)
		},
	}

	root.AddCommand(importCmd, listCmd)
	return root
}

func runImport(ctx context.Context, csvPath, vayuDB string, skipDrafts, dryRun, resume bool) error {
	if csvPath == "" {
		return fmt.Errorf("--csv is required")
	}

	articles, err := substackparse.ParseCSV(csvPath, skipDrafts)
	if err != nil {
		return fmt.Errorf("parse csv: %w", err)
	}

	if dryRun {
		fmt.Printf("Dry run: found %d articles\n", len(articles))
		for i, a := range articles {
			fmt.Printf("[%3d/%d] %s → %q (%s)\n",
				i+1, len(articles), a.Slug, a.Title, a.CreatedAt.Format("2006-01-02"))
		}
		return nil
	}

	w, err := vacuum.Open(vayuDB)
	if err != nil {
		return fmt.Errorf("open vayu db: %w", err)
	}
	defer w.Close()

	var startAfterID string
	if resume {
		startAfterID, err = w.LoadCheckpoint(ctx)
		if err != nil {
			return fmt.Errorf("load checkpoint: %w", err)
		}
		if startAfterID != "" {
			fmt.Printf("Resuming after post_id=%s\n", startAfterID)
		}
	}

	skipping := startAfterID != ""
	total := len(articles)
	for i, a := range articles {
		if skipping {
			if a.ID == startAfterID {
				skipping = false
			}
			continue
		}

		now := time.Now().UTC()
		va := vacuum.Article{
			ID:        a.ID,
			Title:     a.Title,
			Slug:      a.Slug,
			Content:   a.Content,
			Tags:      a.Tags,
			CreatedAt: a.CreatedAt,
			UpdatedAt: now,
		}

		inserted, err := w.Insert(ctx, va)
		if err != nil {
			return fmt.Errorf("insert %q: %w", a.Slug, err)
		}

		status := "✓ inserted"
		if !inserted {
			status = "↷ skipped"
		}
		fmt.Printf("[%3d/%d] %s → %q (%s) %s\n",
			i+1, total, a.Slug, a.Title, a.CreatedAt.Format("2006-01-02"), status)

		if resume && a.ID != "" {
			if err := w.SaveCheckpoint(ctx, a.ID); err != nil {
				return fmt.Errorf("save checkpoint: %w", err)
			}
		}
	}

	fmt.Println("Done.")
	return nil
}

func runList(ctx context.Context, vayuDB string) error {
	db, err := sql.Open("sqlite3", vayuDB+"?_foreign_keys=on")
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx,
		`SELECT id, title, slug, created_at FROM articles ORDER BY created_at DESC`)
	if err != nil {
		return fmt.Errorf("query articles: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, title, slug, createdAt string
		if err := rows.Scan(&id, &title, &slug, &createdAt); err != nil {
			return fmt.Errorf("scan row: %w", err)
		}
		fmt.Printf("%s  %-40s  %s  %s\n", createdAt[:10], slug, id[:8], title)
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	fmt.Printf("\n%d article(s) total.\n", count)
	return nil
}
