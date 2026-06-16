// Command wp2vayu migrates WordPress posts into a VayuPress SQLite database.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/johalputt/wordpress2vayu/internal/convert"
	"github.com/johalputt/wordpress2vayu/internal/vacuum"
	"github.com/johalputt/wordpress2vayu/internal/wpdb"
	"github.com/spf13/cobra"
)

var (
	flagWPDSN       string
	flagVayuDB      string
	flagStatus      string
	flagPostType    string
	flagBatch       int
	flagDelay       time.Duration
	flagResume      bool
	flagDryRun      bool
	flagTablePrefix string
)

func main() {
	root := &cobra.Command{
		Use:   "wp2vayu",
		Short: "Migrate WordPress posts to VayuPress SQLite",
	}

	root.PersistentFlags().StringVar(&flagWPDSN, "wp-dsn", "", "WordPress MySQL DSN (required)")
	root.PersistentFlags().StringVar(&flagVayuDB, "vayu-db", "vayu.db", "VayuPress SQLite database path")
	root.PersistentFlags().StringVar(&flagStatus, "status", "publish", "Post status to migrate: publish, draft, all")
	root.PersistentFlags().StringVar(&flagPostType, "post-type", "post", "Post type to migrate: post, page, both")
	root.PersistentFlags().StringVar(&flagTablePrefix, "table-prefix", "wp_", "WordPress table prefix")
	root.PersistentFlags().IntVar(&flagBatch, "batch", 50, "Number of posts per batch")
	root.PersistentFlags().DurationVar(&flagDelay, "delay", 200*time.Millisecond, "Delay between batches")
	root.PersistentFlags().BoolVar(&flagResume, "resume", true, "Resume from last checkpoint")
	root.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "Simulate migration without writing")

	root.AddCommand(countCmd(), migrateCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func countCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "count",
		Short: "Count posts matching the given filters",
		RunE: func(cmd *cobra.Command, args []string) error {
			dsn, err := ensureMySQLParseTime(flagWPDSN)
			if err != nil {
				return err
			}
			reader, err := wpdb.New(dsn)
			if err != nil {
				return fmt.Errorf("connect to WordPress DB: %w", err)
			}
			defer reader.Close()

			ctx := context.Background()
			count, err := reader.Count(ctx, flagStatus, flagPostType, flagTablePrefix)
			if err != nil {
				return err
			}
			fmt.Printf("Posts matching status=%s type=%s: %d\n", flagStatus, flagPostType, count)
			return nil
		},
	}
}

func migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Migrate WordPress posts to VayuPress",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flagWPDSN == "" {
				return fmt.Errorf("--wp-dsn is required")
			}
			dsn, err := ensureMySQLParseTime(flagWPDSN)
			if err != nil {
				return err
			}

			reader, err := wpdb.New(dsn)
			if err != nil {
				return fmt.Errorf("connect to WordPress DB: %w", err)
			}
			defer reader.Close()

			var writer *vacuum.Writer
			if !flagDryRun {
				writer, err = vacuum.Open(flagVayuDB)
				if err != nil {
					return fmt.Errorf("open VayuPress DB: %w", err)
				}
				defer writer.Close()
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Signal handling.
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

			// Get total count.
			total, err := reader.Count(ctx, flagStatus, flagPostType, flagTablePrefix)
			if err != nil {
				return fmt.Errorf("count posts: %w", err)
			}

			// Load checkpoint.
			afterID := ""
			if flagResume && writer != nil {
				afterID, err = writer.LoadCheckpoint(ctx)
				if err != nil {
					return fmt.Errorf("load checkpoint: %w", err)
				}
				if afterID != "" {
					fmt.Printf("Resuming from post ID %s\n", afterID)
				}
			}

			var totalInserted, totalSkipped int
			batchNum := 0
			lastID := afterID

			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					posts, err := reader.Fetch(ctx, flagStatus, flagPostType, flagTablePrefix, flagBatch, afterID)
					if err != nil {
						fmt.Fprintf(os.Stderr, "fetch error: %v\n", err)
						cancel()
						return
					}
					if len(posts) == 0 {
						return
					}

					batchNum++
					inserted := 0
					skipped := 0

					for _, p := range posts {
						lastID = p.ID

						content := convert.CleanHTML(p.Content)

						// Prepend feature image if present and not already in content.
						if p.FeatureImage != "" && !strings.Contains(content, p.FeatureImage) {
							content = fmt.Sprintf(`<figure><img src="%s"></figure>`, p.FeatureImage) + "\n" + content
						}

						if !flagDryRun && writer != nil {
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
								fmt.Fprintf(os.Stderr, "insert error [%s]: %v\n", p.Slug, err)
								continue
							}
							if ok {
								inserted++
							} else {
								skipped++
							}
						} else {
							inserted++ // dry-run: count as inserted
						}
					}

					totalInserted += inserted
					totalSkipped += skipped
					afterID = lastID

					fmt.Printf("Batch %d: fetched %d, inserted %d, skipped %d (total: %d/%d)\n",
						batchNum, len(posts), inserted, skipped, totalInserted, total)

					// Save checkpoint after each batch.
					if writer != nil {
						if err := writer.SaveCheckpoint(ctx, lastID); err != nil {
							fmt.Fprintf(os.Stderr, "checkpoint error: %v\n", err)
						}
					}

					if len(posts) < flagBatch {
						return
					}

					// Check for cancellation before sleeping.
					select {
					case <-ctx.Done():
						return
					default:
					}

					time.Sleep(flagDelay)
				}
			}()

			select {
			case <-sigCh:
				cancel()
				<-done
				// Save checkpoint on interrupt.
				if writer != nil && lastID != "" {
					saveCtx := context.Background()
					if err := writer.SaveCheckpoint(saveCtx, lastID); err != nil {
						fmt.Fprintf(os.Stderr, "checkpoint save error: %v\n", err)
					}
				}
				fmt.Println("\nMigration interrupted. Resume with --resume")
			case <-done:
			}

			// Final summary.
			fmt.Println()
			fmt.Println("Migration Summary")
			fmt.Println("=================")
			fmt.Printf("Total posts found : %d\n", total)
			fmt.Printf("Total inserted    : %d\n", totalInserted)
			fmt.Printf("Total skipped     : %d\n", totalSkipped)
			fmt.Printf("Batches processed : %d\n", batchNum)
			if flagDryRun {
				fmt.Println("(dry-run: no changes written)")
			}
			return nil
		},
	}
}

// ensureMySQLParseTime appends parseTime=true to the DSN query string if absent.
func ensureMySQLParseTime(dsn string) (string, error) {
	if dsn == "" {
		return "", fmt.Errorf("--wp-dsn is required")
	}
	if strings.Contains(dsn, "parseTime=true") {
		return dsn, nil
	}
	// DSN format: user:pass@tcp(host:port)/dbname?params
	idx := strings.IndexByte(dsn, '?')
	if idx == -1 {
		return dsn + "?parseTime=true", nil
	}
	base := dsn[:idx+1]
	params := dsn[idx+1:]
	// Parse existing params to avoid duplicates.
	vals, err := url.ParseQuery(params)
	if err != nil {
		// Fallback: just append.
		return dsn + "&parseTime=true", nil
	}
	vals.Set("parseTime", "true")
	return base + vals.Encode(), nil
}
