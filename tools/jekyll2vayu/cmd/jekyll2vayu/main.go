package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johalputt/jekyll2vayu/internal/jekyllparse"
	"github.com/johalputt/jekyll2vayu/internal/vacuum"
	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "jekyll2vayu",
		Short: "Import Jekyll posts into a VayuPress SQLite database",
	}

	root.AddCommand(importCmd())
	root.AddCommand(listCmd())
	return root
}

func importCmd() *cobra.Command {
	var (
		site       string
		vayuDB     string
		postsDir   string
		skipDrafts bool
		dryRun     bool
		resume     bool
	)

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import Jekyll posts into VayuPress",
		RunE: func(cmd *cobra.Command, args []string) error {
			if site == "" {
				return fmt.Errorf("--site is required")
			}
			if vayuDB == "" {
				return fmt.Errorf("--vayu-db is required")
			}

			postsPath := filepath.Join(site, postsDir)
			files, err := walkMarkdown(postsPath)
			if err != nil {
				return fmt.Errorf("walk %q: %w", postsPath, err)
			}

			var w *vacuum.Writer
			if !dryRun {
				w, err = vacuum.Open(vayuDB)
				if err != nil {
					return fmt.Errorf("open db: %w", err)
				}
				defer w.Close()
			}

			// Resume support: find checkpoint.
			var checkpointFile string
			resuming := false
			if resume && !dryRun && w != nil {
				checkpointFile, err = w.GetCheckpoint()
				if err != nil {
					return fmt.Errorf("get checkpoint: %w", err)
				}
				if checkpointFile != "" {
					resuming = true
				}
			}

			total := len(files)
			for i, f := range files {
				// Resume: skip until we find the checkpoint file (exclusive).
				if resuming {
					if f == checkpointFile {
						resuming = false
					}
					continue
				}

				doc, err := jekyllparse.Parse(f)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[%3d/%d] %s → parse error: %v\n", i+1, total, filepath.Base(f), err)
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

				status := "skipped (already exists)"
				if inserted {
					status = "✓ inserted"
				}
				fmt.Printf("[%3d/%d] %s → %q (%s) %s\n",
					i+1, total, doc.Slug, doc.Title, doc.Date.Format("2006-01-02"), status)

				if err := w.SetCheckpoint(f); err != nil {
					fmt.Fprintf(os.Stderr, "warning: set checkpoint: %v\n", err)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&site, "site", "", "Jekyll site root directory (required)")
	cmd.Flags().StringVar(&vayuDB, "vayu-db", "", "Path to VayuPress SQLite database (required)")
	cmd.Flags().StringVar(&postsDir, "posts-dir", "_posts", "Posts directory relative to site root")
	cmd.Flags().BoolVar(&skipDrafts, "skip-drafts", true, "Skip posts with published: false")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Parse and print without writing to DB")
	cmd.Flags().BoolVar(&resume, "resume", true, "Resume from last checkpoint")

	return cmd
}

func listCmd() *cobra.Command {
	var (
		site     string
		postsDir string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Jekyll posts with parsed metadata",
		RunE: func(cmd *cobra.Command, args []string) error {
			if site == "" {
				return fmt.Errorf("--site is required")
			}

			postsPath := filepath.Join(site, postsDir)
			files, err := walkMarkdown(postsPath)
			if err != nil {
				return fmt.Errorf("walk %q: %w", postsPath, err)
			}

			for _, f := range files {
				doc, err := jekyllparse.Parse(f)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s → parse error: %v\n", filepath.Base(f), err)
					continue
				}
				draftMark := ""
				if doc.Draft {
					draftMark = " [DRAFT]"
				}
				tags := strings.Join(doc.Tags, ", ")
				fmt.Printf("%s → %q (%s) tags=[%s]%s\n",
					doc.Slug, doc.Title, doc.Date.Format("2006-01-02"), tags, draftMark)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&site, "site", "", "Jekyll site root directory (required)")
	cmd.Flags().StringVar(&postsDir, "posts-dir", "_posts", "Posts directory relative to site root")

	return cmd
}

// walkMarkdown returns all .md files under dir, sorted.
func walkMarkdown(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.ToLower(filepath.Ext(path)) == ".md" {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
