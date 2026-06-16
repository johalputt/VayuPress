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

Ghost HTML / mobiledoc / Lexical content is converted to plain text.
Original Ghost slugs are preserved exactly. Migration is throttled to
avoid overloading your VPS. Supports resume-on-interrupt via checkpoints.`,
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
		fmt.Println("  ✓ VayuPress DB ready\n")
	} else {
		fmt.Println("→ DRY-RUN mode — nothing will be written\n")
	}

	// ── Resume checkpoint ─────────────────────────────────────────────
	var startOffset int64
	if flagResume && !flagDryRun {
		startOffset, err = writer.LoadCheckpoint(ctx)
		if err != nil {
			return fmt.Errorf("load checkpoint: %w", err)
		}
		if startOffset > 0 {
			fmt.Printf("→ Resuming from offset %d (skipping already-migrated posts)\n\n", startOffset)
		}
	}

	// ── Migration loop ────────────────────────────────────────────────
	var (
		offset   = startOffset
		inserted int64
		skipped  int64
		errors   int64
		batchNum int
	)

	ticker := time.NewTicker(flagDelay)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("\n⚠ Interrupted — saving checkpoint at offset %d\n", offset)
			if writer != nil {
				writer.SaveCheckpoint(context.Background(), offset)
			}
			printSummary(inserted, skipped, errors, total)
			return nil
		case <-ticker.C:
		}

		posts, err := reader.Fetch(ctx, flagStatus, flagBatch, int(offset))
		if err != nil {
			fmt.Fprintf(os.Stderr, "  fetch error at offset %d: %v\n", offset, err)
			errors++
			offset += int64(flagBatch)
			continue
		}
		if len(posts) == 0 {
			break
		}

		batchNum++
		batchInserted := 0
		batchSkipped := 0

		for _, p := range posts {
			content := convert.BestContent(p.HTML, p.Mobiledoc, p.Lexical)
			if content == "" {
				content = p.Title // minimum viable content
			}

			art := vacuum.Article{
				ID:        p.ID,
				Title:     p.Title,
				Slug:      p.Slug,
				Content:   content,
				Tags:      p.Tags,
				CreatedAt: p.CreatedAt,
				UpdatedAt: p.UpdatedAt,
			}

			if flagDryRun {
				fmt.Printf("  [dry] %s — %q (%d chars, %d tags)\n",
					p.Slug, truncate(p.Title, 50), len(content), len(p.Tags))
				batchInserted++
				continue
			}

			ok, err := writer.Insert(ctx, art)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  ERR  %s: %v\n", p.Slug, err)
				errors++
				continue
			}
			if ok {
				batchInserted++
			} else {
				batchSkipped++
			}
		}

		inserted += int64(batchInserted)
		skipped += int64(batchSkipped)
		offset += int64(len(posts))

		pct := float64(offset) / float64(total) * 100
		fmt.Printf("  batch %-4d  offset %-8d  +%-4d  skip %-4d  %.1f%%\n",
			batchNum, offset, batchInserted, batchSkipped, pct)

		// Save checkpoint every 10 batches
		if !flagDryRun && batchNum%10 == 0 {
			if err := writer.SaveCheckpoint(ctx, offset); err != nil {
				fmt.Fprintf(os.Stderr, "  checkpoint err: %v\n", err)
			}
		}

		if len(posts) < flagBatch {
			break // last page
		}
	}

	// Final checkpoint
	if !flagDryRun && writer != nil {
		writer.SaveCheckpoint(context.Background(), offset)
	}

	fmt.Println()
	printSummary(inserted, skipped, errors, total)
	return nil
}

func printSummary(inserted, skipped, errors, total int64) {
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Inserted : %d\n", inserted)
	fmt.Printf("  Skipped  : %d (already existed)\n", skipped)
	fmt.Printf("  Errors   : %d\n", errors)
	fmt.Printf("  Total    : %d\n", total)
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
