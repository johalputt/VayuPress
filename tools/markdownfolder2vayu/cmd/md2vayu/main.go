// Command md2vayu imports a folder of Markdown files into a VayuPress SQLite database.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johalputt/markdownfolder2vayu/internal/mdparse"
	"github.com/johalputt/markdownfolder2vayu/internal/vacuum"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var (
		dir       string
		vayuDB    string
		recursive bool
		skipDraft bool
		dryRun    bool
		resume    bool
	)

	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Walk a Markdown folder and import articles into VayuPress",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImport(dir, vayuDB, recursive, skipDraft, dryRun, resume)
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "Walk a Markdown folder and list found files (no DB write)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(dir, recursive)
		},
	}

	root := &cobra.Command{
		Use:   "md2vayu",
		Short: "Import Markdown files into a VayuPress SQLite database",
		// Default to import when no subcommand is given.
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImport(dir, vayuDB, recursive, skipDraft, dryRun, resume)
		},
	}

	// Flags on root (also inherited by import).
	root.PersistentFlags().StringVar(&dir, "dir", "", "Path to Markdown folder (required)")
	root.PersistentFlags().BoolVar(&recursive, "recursive", true, "Walk subdirectories")

	root.Flags().StringVar(&vayuDB, "vayu-db", "", "Path to VayuPress SQLite database (required for import)")
	root.Flags().BoolVar(&skipDraft, "skip-drafts", true, "Skip files with draft: true")
	root.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be imported without writing")
	root.Flags().BoolVar(&resume, "resume", true, "Skip files already in DB via INSERT OR IGNORE")

	importCmd.Flags().StringVar(&vayuDB, "vayu-db", "", "Path to VayuPress SQLite database (required)")
	importCmd.Flags().BoolVar(&skipDraft, "skip-drafts", true, "Skip files with draft: true")
	importCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be imported without writing")
	importCmd.Flags().BoolVar(&resume, "resume", true, "Skip files already in DB via INSERT OR IGNORE")

	root.AddCommand(importCmd)
	root.AddCommand(listCmd)

	return root
}

// collectMarkdownFiles walks dir and returns all .md file paths.
func collectMarkdownFiles(dir string, recursive bool) ([]string, error) {
	var files []string
	walkFn := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && !recursive && path != dir {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			files = append(files, path)
		}
		return nil
	}
	if err := filepath.WalkDir(dir, walkFn); err != nil {
		return nil, fmt.Errorf("walk %q: %w", dir, err)
	}
	return files, nil
}

func runImport(dir, vayuDB string, recursive, skipDraft, dryRun, resume bool) error {
	if dir == "" {
		return fmt.Errorf("--dir is required")
	}
	if vayuDB == "" && !dryRun {
		return fmt.Errorf("--vayu-db is required for import (or use --dry-run)")
	}

	fmt.Printf("Scanning %s...\n", dir)
	files, err := collectMarkdownFiles(dir, recursive)
	if err != nil {
		return err
	}
	fmt.Printf("Found %d Markdown files.\n", len(files))

	var w *vacuum.Writer
	if !dryRun {
		w, err = vacuum.Open(vayuDB)
		if err != nil {
			return fmt.Errorf("open db: %w", err)
		}
		defer w.Close()
	}

	total := len(files)
	inserted := 0
	skipped := 0
	errors := 0

	for i, path := range files {
		base := filepath.Base(path)
		prefix := fmt.Sprintf("[%3d/%d] %s", i+1, total, base)

		doc, err := mdparse.Parse(path)
		if err != nil {
			fmt.Printf("%s → ERROR: %v\n", prefix, err)
			errors++
			continue
		}

		if skipDraft && doc.Draft {
			fmt.Printf("%s → %q (draft, skipped)\n", prefix, doc.Title)
			skipped++
			continue
		}

		dateStr := doc.Date.Format("2006-01-02")
		label := fmt.Sprintf("%q (%s)", doc.Title, dateStr)

		if dryRun {
			fmt.Printf("%s → %s [dry-run]\n", prefix, label)
			inserted++
			continue
		}

		ok, err := w.InsertArticle(doc)
		if err != nil {
			fmt.Printf("%s → %s ERROR: %v\n", prefix, label, err)
			errors++
			continue
		}

		if ok {
			fmt.Printf("%s → %s ✓ inserted\n", prefix, label)
			inserted++
			if resume {
				_ = w.SetCheckpoint(path)
			}
		} else {
			fmt.Printf("%s → %s (already exists, skipped)\n", prefix, label)
			skipped++
		}
	}

	fmt.Printf("\nDone. Inserted: %d, Skipped: %d, Errors: %d\n", inserted, skipped, errors)
	return nil
}

func runList(dir string, recursive bool) error {
	if dir == "" {
		return fmt.Errorf("--dir is required")
	}

	files, err := collectMarkdownFiles(dir, recursive)
	if err != nil {
		return err
	}

	fmt.Printf("Found %d Markdown files in %s:\n\n", len(files), dir)
	for _, path := range files {
		doc, err := mdparse.Parse(path)
		if err != nil {
			fmt.Printf("  ERROR %s: %v\n", filepath.Base(path), err)
			continue
		}
		draftLabel := ""
		if doc.Draft {
			draftLabel = " [DRAFT]"
		}
		fmt.Printf("  %s → %q  slug=%s  date=%s%s\n",
			filepath.Base(path),
			doc.Title,
			doc.Slug,
			doc.Date.Format("2006-01-02"),
			draftLabel,
		)
	}
	return nil
}
