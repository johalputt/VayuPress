// ghost2vayu — migrate a Ghost CMS database directly into VayuPress SQLite.
//
// Usage:
//
//	ghost2vayu migrate \
//	  --ghost-driver mysql \
//	  --ghost-dsn "user:pass@tcp(localhost:3306)/ghost_production" \
//	  --vayu-db /path/to/vayupress.db \
//	  --status published \
//	  --batch 50 \
//	  --delay 200ms
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/johalputt/ghost-to-vayu/internal/convert"
	"github.com/johalputt/ghost-to-vayu/internal/ghostdb"
	"github.com/johalputt/ghost-to-vayu/internal/vacuum"
	"github.com/spf13/cobra"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var (
	flagGhostDriver string
	flagGhostDSN    string
	flagVayuDB      string
	flagStatus      string
	flagBatch       int
	flagDelay       time.Duration
	flagResume      bool
	flagDryRun      bool
)

var rootCmd = &cobra.Command{
	Use:   "ghost2vayu",
	Short: "Migrate Ghost CMS database directly into VayuPress SQLite",
	Long: `ghost2vayu reads your Ghost database (MySQL or SQLite) without needing
Ghost admin access and imports all posts into a VayuPress SQLite database.

Ghost HTML is passed through (images, links, and formatting preserved) and
sanitized by VayuPress on render. Mobiledoc and Lexical editor formats are
converted to HTML when no rendered html is stored. Original Ghost slugs are
preserved exactly. Migration uses keyset pagination and throttled batching to
stay gentle on your VPS, and resumes from a checkpoint if interrupted.`,
}

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run the migration",
	RunE:  runMigrate,
}

var countCmd = &cobra.Command{
	Use:   "count",
	Short: "Count Ghost posts without migrating",
	RunE:  runCount,
}

func init() {
	for _, cmd := range []*cobra.Command{migrateCmd, countCmd} {
		cmd.Flags().StringVar(&flagGhostDriver, "ghost-driver", "mysql", "Ghost DB driver: mysql or sqlite3")
		cmd.Flags().StringVar(&flagGhostDSN, "ghost-dsn", "", "Ghost DB DSN (required)")
		cmd.MarkFlagRequired("ghost-dsn")
	}

	migrateCmd.Flags().StringVar(&flagVayuDB, "vayu-db", "vayupress.db", "Path to VayuPress SQLite database")
	migrateCmd.Flags().StringVar(&flagStatus, "status", "published", "Ghost post status to migrate: published | draft | all")
	migrateCmd.Flags().IntVar(&flagBatch, "batch", 50, "Posts per batch (lower = gentler on VPS)")
	migrateCmd.Flags().DurationVar(&flagDelay, "delay", 200*time.Millisecond, "Pause between batches (e.g. 200ms, 1s)")
	migrateCmd.Flags().BoolVar(&flagResume, "resume", true, "Resume from last checkpoint if interrupted")
	migrateCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Parse and convert but do not write to VayuPress DB")

	rootCmd.AddCommand(migrateCmd, countCmd)
}

func runCount(cmd *cobra.Command, _ []string) error {
	reader, err := ghostdb.New(flagGhostDriver, flagGhostDSN)
	if err != nil {
		return fmt.Errorf("connect to Ghost DB: %w", err)
	}
	defer reader.Close()

	ctx := cmd.Context()
	for _, status := range []string{"published", "draft", "scheduled"} {
		n, err := reader.Count(ctx, status)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  %s: error (%v)\n", status, err)
			continue
		}
		fmt.Printf("  %-12s %d posts\n", status, n)
	}
	return nil
}

func runMigrate(cmd *cobra.Command, _ []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// ── Connect to Ghost ──────────────────────────────────────────────
	fmt.Printf("→ Connecting to Ghost DB (%s)…\n", flagGhostDriver)
	reader, err := ghostdb.New(flagGhostDriver, flagGhostDSN)
	if err != nil {
		return fmt.Errorf("Ghost DB: %w", err)
	}
	defer reader.Close()
	fmt.Println("  ✓ Ghost DB connected")

	total, err := reader.Count(ctx, flagStatus)
	if err != nil {
		return fmt.Errorf("count: %w", err)
	}
	fmt.Printf("  ✓ %d Ghost posts (status=%s)\n\n", total, flagStatus)

	// ── Connect to VayuPress ──────────────────────────────────────────
	var writer *vacuum.Writer
	if !flagDryRun {
		fmt.Printf("→ Opening VayuPress DB at %s…\n", flagVayuDB)
		writer, err = vacuum.Open(flagVayuDB)
		if err != nil {
			return fmt.Errorf("VayuPress DB: %w", err)
		}
		defer writer.Close()
		fmt.Println("  ✓ VayuPress DB ready")
		fmt.Println()
	} else {
		fmt.Println("→ DRY-RUN mode — nothing will be written")
		fmt.Println()
	}

	// ── Resume checkpoint ─────────────────────────────────────────────
	var afterID string
	if flagResume && !flagDryRun {
		afterID, err = writer.LoadCheckpoint(ctx)
		if err != nil {
			return fmt.Errorf("load checkpoint: %w", err)
		}
		if afterID != "" {
			fmt.Printf("→ Resuming after Ghost post id %s\n\n", afterID)
		}
	}

	// ── Migration loop (keyset pagination) ────────────────────────────
	var (
		processed int64
		inserted  int64
		skipped   int64
		errs      int64
		batchNum  int
	)

	for {
		if err := ctx.Err(); err != nil {
			fmt.Printf("\n⚠ Interrupted — checkpoint saved at id %s\n", afterID)
			break
		}

		posts, err := reader.Fetch(ctx, flagStatus, flagBatch, afterID)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			return fmt.Errorf("fetch after id %q: %w", afterID, err)
		}
		if len(posts) == 0 {
			break // reached the end
		}

		batchNum++
		var batchIns, batchSkip int

		for _, p := range posts {
			content := convert.BestContent(p.HTML, p.Mobiledoc, p.Lexical, p.FeatureImage)
			if content == "" {
				content = "<p>" + p.Title + "</p>" // minimum viable body
			}

			if flagDryRun {
				fmt.Printf("  [dry] %s — %q (%d bytes html, %d tags)\n",
					p.Slug, truncate(p.Title, 50), len(content), len(p.Tags))
				batchIns++
				afterID = p.ID
				continue
			}

			ok, err := writer.Insert(ctx, vacuum.Article{
				ID:        p.ID,
				Title:     p.Title,
				Slug:      p.Slug,
				Content:   content,
				Tags:      p.Tags,
				CreatedAt: p.CreatedAt,
				UpdatedAt: p.UpdatedAt,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "  ERR  %s: %v\n", p.Slug, err)
				errs++
				afterID = p.ID
				continue
			}
			if ok {
				batchIns++
			} else {
				batchSkip++
			}
			afterID = p.ID
		}

		processed += int64(len(posts))
		inserted += int64(batchIns)
		skipped += int64(batchSkip)

		pct := 0.0
		if total > 0 {
			pct = float64(processed) / float64(total) * 100
		}
		fmt.Printf("  batch %-4d  id %-26s  +%-4d  skip %-4d  %d/%d (%.1f%%)\n",
			batchNum, afterID, batchIns, batchSkip, processed, total, pct)

		if !flagDryRun {
			if err := writer.SaveCheckpoint(ctx, afterID); err != nil {
				fmt.Fprintf(os.Stderr, "  checkpoint err: %v\n", err)
			}
		}

		if len(posts) < flagBatch {
			break // last (partial) page
		}

		// Throttle: pause between batches, but wake immediately on interrupt.
		select {
		case <-ctx.Done():
		case <-time.After(flagDelay):
		}
	}

	fmt.Println()
	printSummary(inserted, skipped, errs, total)
	return nil
}

func printSummary(inserted, skipped, errs, total int64) {
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Inserted : %d\n", inserted)
	fmt.Printf("  Skipped  : %d (already existed)\n", skipped)
	fmt.Printf("  Errors   : %d\n", errs)
	fmt.Printf("  Total    : %d\n", total)
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
