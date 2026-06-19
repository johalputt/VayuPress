package main

import (
	"fmt"
	"os"

	"github.com/johalputt/vayu-export/internal/exporter"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "vayu-export",
		Short: "Export VayuPress articles to a static HTML site",
	}

	var (
		dbPath   string
		outDir   string
		baseURL  string
		pageSize int
		clean    bool
	)

	exportCmd := &cobra.Command{
		Use:   "export",
		Short: "Export all articles to a static site",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dbPath == "" {
				return fmt.Errorf("--db is required")
			}
			count, err := exporter.Export(exporter.Options{
				DBPath:   dbPath,
				OutDir:   outDir,
				BaseURL:  baseURL,
				PageSize: pageSize,
				Clean:    clean,
			})
			if err != nil {
				return err
			}
			fmt.Printf("Exported %d articles to %s\n", count, outDir)
			return nil
		},
	}
	exportCmd.Flags().StringVar(&dbPath, "db", "", "Path to vayupress.db (required)")
	exportCmd.Flags().StringVar(&outDir, "out", "./vayu-site", "Output directory")
	exportCmd.Flags().StringVar(&baseURL, "base-url", "", "Base URL (e.g. https://example.com)")
	exportCmd.Flags().IntVar(&pageSize, "page-size", 20, "Articles per page")
	exportCmd.Flags().BoolVar(&clean, "clean", false, "Delete output directory before export")

	countCmd := &cobra.Command{
		Use:   "count",
		Short: "Print the number of articles in the database",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dbPath == "" {
				return fmt.Errorf("--db is required")
			}
			n, err := exporter.Count(dbPath)
			if err != nil {
				return err
			}
			fmt.Printf("%d articles\n", n)
			return nil
		},
	}
	countCmd.Flags().StringVar(&dbPath, "db", "", "Path to vayupress.db (required)")

	root.AddCommand(exportCmd, countCmd)
	root.PersistentFlags().StringVar(&dbPath, "db", "", "Path to vayupress.db")

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
