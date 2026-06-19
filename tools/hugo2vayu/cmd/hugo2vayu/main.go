package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johalputt/hugo2vayu/internal/hugoparse"
	"github.com/johalputt/hugo2vayu/internal/vacuum"
	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "hugo2vayu",
		Short: "Import Hugo Markdown content into a VayuPress SQLite database",
	}
	root.AddCommand(importCmd())
	root.AddCommand(listCmd())
	return root
}

func importCmd() *cobra.Command {
	var (
		site       string
		vayuDB     string
		contentDir string
		recursive  bool
		skipDrafts bool
		dryRun     bool
		resume     bool
	)

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import Hugo Markdown files into VayuPress",
		RunE: func(cmd *cobra.Command, args []string) error {
			if site == "" {
				return fmt.Errorf("--site is required")
			}
			if vayuDB == "" {
				return fmt.Errorf("--vayu-db is required")
			}

			files, err := collectFiles(filepath.Join(site, contentDir), recursive)
			if err != nil {
				return fmt.Errorf("collect files: %w", err)
			}

			w, err := vacuum.Open(vayuDB)
			if err != nil {
				return fmt.Errorf("open db: %w", err)
			}
			defer w.Close()

			var checkpoint string
			if resume {
				checkpoint, err = w.GetCheckpoint()
				if err != nil {
					return fmt.Errorf("get checkpoint: %w", err)
				}
			}

			skipping := checkpoint != ""
			total := len(files)

			for i, path := range files {
				if skipping {
					if path == checkpoint {
						skipping = false
					}
					continue
				}

				doc, err := hugoparse.Parse(path)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[%3d/%d] %s → parse error: %v\n", i+1, total, path, err)
					continue
				}

				if skipDrafts && doc.Draft {
					fmt.Printf("[%3d/%d] %s → skipped (draft)\n", i+1, total, doc.Slug)
					continue
				}

				if dryRun {
					fmt.Printf("[%3d/%d] %s → %q (%s) [dry-run]\n",
						i+1, total, doc.Slug, doc.Title, doc.Date.Format("2006-01-02"))
					continue
				}

				inserted, err := w.InsertArticle(doc)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[%3d/%d] %s → insert error: %v\n", i+1, total, doc.Slug, err)
					continue
				}

				status := "✓ inserted"
				if !inserted {
					status = "- skipped (exists)"
				}
				fmt.Printf("[%3d/%d] %s → %q (%s) %s\n",
					i+1, total, doc.Slug, doc.Title, doc.Date.Format("2006-01-02"), status)

				if resume {
					if err := w.SetCheckpoint(path); err != nil {
						fmt.Fprintf(os.Stderr, "warning: set checkpoint: %v\n", err)
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&site, "site", "", "Hugo site root directory (required)")
	cmd.Flags().StringVar(&vayuDB, "vayu-db", "", "Path to VayuPress SQLite database (required)")
	cmd.Flags().StringVar(&contentDir, "content-dir", "content", "Content directory relative to site root")
	cmd.Flags().BoolVar(&recursive, "recursive", true, "Walk content directory recursively")
	cmd.Flags().BoolVar(&skipDrafts, "skip-drafts", true, "Skip draft posts")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview without writing to database")
	cmd.Flags().BoolVar(&resume, "resume", true, "Resume from last checkpoint")

	return cmd
}

func listCmd() *cobra.Command {
	var (
		site       string
		contentDir string
		recursive  bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Hugo Markdown files and their parsed metadata",
		RunE: func(cmd *cobra.Command, args []string) error {
			if site == "" {
				return fmt.Errorf("--site is required")
			}

			files, err := collectFiles(filepath.Join(site, contentDir), recursive)
			if err != nil {
				return fmt.Errorf("collect files: %w", err)
			}

			for _, path := range files {
				doc, err := hugoparse.Parse(path)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s: parse error: %v\n", path, err)
					continue
				}
				draftStr := ""
				if doc.Draft {
					draftStr = " [DRAFT]"
				}
				fmt.Printf("slug: %s | title: %q | date: %s | tags: [%s]%s\n",
					doc.Slug,
					doc.Title,
					doc.Date.Format("2006-01-02"),
					strings.Join(doc.Tags, ", "),
					draftStr,
				)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&site, "site", "", "Hugo site root directory (required)")
	cmd.Flags().StringVar(&contentDir, "content-dir", "content", "Content directory relative to site root")
	cmd.Flags().BoolVar(&recursive, "recursive", true, "Walk content directory recursively")

	return cmd
}

// collectFiles returns all .md files under root, optionally recursive.
func collectFiles(root string, recursive bool) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if !recursive && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
