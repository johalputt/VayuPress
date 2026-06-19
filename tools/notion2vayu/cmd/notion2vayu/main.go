package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/johalputt/notion2vayu/internal/notionparse"
	"github.com/johalputt/notion2vayu/internal/vacuum"
	"github.com/spf13/cobra"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := rootCmd().ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var (
		dir    string
		zip    string
		vayuDB string
		dryRun bool
		resume bool
	)

	root := &cobra.Command{
		Use:   "notion2vayu",
		Short: "Import Notion HTML exports into a Vayu SQLite database",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImport(cmd.Context(), dir, zip, vayuDB, dryRun, resume)
		},
	}

	root.PersistentFlags().StringVar(&dir, "dir", "", "Directory of Notion HTML export")
	root.PersistentFlags().StringVar(&zip, "zip", "", "ZIP file of Notion HTML export")
	root.PersistentFlags().StringVar(&vayuDB, "vayu-db", "vayu.db", "Path to Vayu SQLite database")
	root.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "Parse and print articles without inserting")
	root.PersistentFlags().BoolVar(&resume, "resume", true, "Resume from last checkpoint")

	root.AddCommand(importCmd(&dir, &zip, &vayuDB, &dryRun, &resume))
	root.AddCommand(listCmd(&vayuDB))

	return root
}

func importCmd(dir, zip, vayuDB *string, dryRun, resume *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "import",
		Short: "Parse Notion HTML export and insert articles into Vayu DB",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImport(cmd.Context(), *dir, *zip, *vayuDB, *dryRun, *resume)
		},
	}
}

func runImport(ctx context.Context, dir, zipPath, vayuDB string, dryRun, resume bool) error {
	if dir == "" && zipPath == "" {
		return fmt.Errorf("one of --dir or --zip is required")
	}

	var articles []notionparse.Article
	var tmpDir string
	var err error

	if zipPath != "" {
		fmt.Fprintf(os.Stderr, "Extracting %s...\n", zipPath)
		articles, tmpDir, err = notionparse.ParseZip(zipPath)
		if tmpDir != "" {
			defer os.RemoveAll(tmpDir)
		}
	} else {
		articles, err = notionparse.ParseDir(dir)
	}
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	total := len(articles)
	fmt.Fprintf(os.Stderr, "Found %d articles\n", total)

	if dryRun {
		for i, a := range articles {
			fmt.Printf("[%3d/%d] %s → %q (%s) [dry-run]\n",
				i+1, total, a.Slug, a.Title, a.CreatedAt.Format("2006-01-02"))
		}
		return nil
	}

	w, err := vacuum.Open(vayuDB)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer w.Close()

	var lastSlug string
	if resume {
		lastSlug, err = w.LoadCheckpoint(ctx)
		if err != nil {
			return fmt.Errorf("load checkpoint: %w", err)
		}
		if lastSlug != "" {
			fmt.Fprintf(os.Stderr, "Resuming from checkpoint: %s\n", lastSlug)
		}
	}

	skipping := lastSlug != ""
	inserted := 0
	skipped := 0

	for i, a := range articles {
		if skipping {
			if a.Slug == lastSlug {
				skipping = false
			}
			skipped++
			fmt.Printf("[%3d/%d] %s → %q (%s) ↷ skipped (resume)\n",
				i+1, total, a.Slug, a.Title, a.CreatedAt.Format("2006-01-02"))
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		va := vacuum.Article{
			Title:     a.Title,
			Slug:      a.Slug,
			Content:   a.Content,
			Tags:      a.Tags,
			CreatedAt: a.CreatedAt,
			UpdatedAt: a.CreatedAt,
		}

		ok, err := w.Insert(ctx, va)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%3d/%d] %s ERROR: %v\n", i+1, total, a.Slug, err)
			continue
		}

		if ok {
			inserted++
			fmt.Printf("[%3d/%d] %s → %q (%s) ✓ inserted\n",
				i+1, total, a.Slug, a.Title, a.CreatedAt.Format("2006-01-02"))
		} else {
			skipped++
			fmt.Printf("[%3d/%d] %s → %q (%s) ↷ skipped\n",
				i+1, total, a.Slug, a.Title, a.CreatedAt.Format("2006-01-02"))
		}

		if err := w.SaveCheckpoint(ctx, a.Slug); err != nil {
			fmt.Fprintf(os.Stderr, "checkpoint error: %v\n", err)
		}
	}

	fmt.Fprintf(os.Stderr, "\nDone: %d inserted, %d skipped\n", inserted, skipped)
	return nil
}

func listCmd(vayuDB *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List articles in the Vayu database",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd.Context(), *vayuDB)
		},
	}
}

func runList(ctx context.Context, vayuDB string) error {
	w, err := vacuum.Open(vayuDB)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer w.Close()

	rows, err := w.QueryArticles(ctx)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, title, slug, tags, createdAt string
		if err := rows.Scan(&id, &title, &slug, &tags, &createdAt); err != nil {
			return err
		}
		count++
		fmt.Printf("%s\t%s\t%q\t[%s]\n", createdAt, slug, title, tags)
	}
	fmt.Fprintf(os.Stderr, "\nTotal: %d articles\n", count)
	return nil
}
