// vayu-validate — check a VayuPress SQLite database for content integrity issues.
//
// Usage:
//
//	vayu-validate validate --db vayupress.db
//	vayu-validate stats    --db vayupress.db
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/johalputt/vayu-validate/internal/validate"
	"github.com/spf13/cobra"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var flagDB string

var rootCmd = &cobra.Command{
	Use:   "vayu-validate",
	Short: "Validate a VayuPress SQLite database for content integrity",
}

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Check articles for errors and warnings",
	RunE:  runValidate,
}

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show content statistics for the database",
	RunE:  runStats,
}

func init() {
	for _, cmd := range []*cobra.Command{validateCmd, statsCmd} {
		cmd.Flags().StringVar(&flagDB, "db", "vayupress.db", "Path to VayuPress SQLite database")
	}
	rootCmd.AddCommand(validateCmd, statsCmd)
}

func runValidate(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()
	fmt.Printf("→ Validating %s…\n\n", flagDB)

	report, err := validate.Validate(ctx, flagDB)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	if len(report.Issues) == 0 {
		fmt.Printf("✓ %d articles — no issues found\n", report.Total)
		return nil
	}

	// Group by severity.
	for _, sev := range []validate.Severity{validate.SeverityError, validate.SeverityWarning, validate.SeverityInfo} {
		for _, issue := range report.Issues {
			if issue.Severity == sev {
				icon := "•"
				switch sev {
				case validate.SeverityError:
					icon = "✗"
				case validate.SeverityWarning:
					icon = "⚠"
				}
				fmt.Printf("  %s [%s] %s: %s\n", icon, issue.Rule, issue.Slug, issue.Detail)
			}
		}
	}

	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Articles : %d\n", report.Total)
	fmt.Printf("  Errors   : %d\n", report.Errors)
	fmt.Printf("  Warnings : %d\n", report.Warnings)
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	if report.Errors > 0 {
		os.Exit(1)
	}
	return nil
}

func runStats(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()
	s, err := validate.CollectStats(ctx, flagDB)
	if err != nil {
		return fmt.Errorf("stats: %w", err)
	}

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Total articles  : %d\n", s.Total)
	fmt.Printf("  Avg content size: %s\n", formatBytes(s.AvgSize))
	if !s.OldestAt.IsZero() {
		fmt.Printf("  Oldest article  : %s\n", s.OldestAt.Format(time.DateOnly))
		fmt.Printf("  Newest article  : %s\n", s.NewestAt.Format(time.DateOnly))
	}
	fmt.Printf("  Unique tags     : %d\n", len(s.Tags))

	if len(s.Tags) > 0 {
		fmt.Println("\n  Top tags:")
		type kv struct {
			tag string
			n   int
		}
		var kvs []kv
		for k, v := range s.Tags {
			kvs = append(kvs, kv{k, v})
		}
		sort.Slice(kvs, func(i, j int) bool { return kvs[i].n > kvs[j].n })
		limit := 10
		if len(kvs) < limit {
			limit = len(kvs)
		}
		for _, kv := range kvs[:limit] {
			fmt.Printf("    %-30s %d\n", kv.tag, kv.n)
		}
	}
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return nil
}

func formatBytes(n int64) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
