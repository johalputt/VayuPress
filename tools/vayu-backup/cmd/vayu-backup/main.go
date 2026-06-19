package main

import (
	"fmt"
	"os"
	"time"

	"github.com/johalputt/vayu-backup/internal/backup"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "vayu-backup",
		Short: "VayuPress database backup tool",
	}

	// backup command
	var backupDB, backupOut string
	var backupCompress bool

	backupCmd := &cobra.Command{
		Use:   "backup",
		Short: "Create a backup archive of the VayuPress database",
		RunE: func(cmd *cobra.Command, args []string) error {
			if backupDB == "" {
				return fmt.Errorf("--db is required")
			}
			outPath := backupOut
			if outPath == "" {
				outPath = fmt.Sprintf("vayupress-backup-%s.tar.gz", time.Now().Format("2006-01-02"))
			}
			result, err := backup.Create(backup.Options{
				DBPath:   backupDB,
				OutPath:  outPath,
				Compress: backupCompress,
			})
			if err != nil {
				return err
			}
			fmt.Printf("Backup created: %s\n", result)
			return nil
		},
	}
	backupCmd.Flags().StringVar(&backupDB, "db", "", "Path to vayupress.db (required)")
	backupCmd.Flags().StringVar(&backupOut, "out", "", "Output file path (default: vayupress-backup-YYYY-MM-DD.tar.gz)")
	backupCmd.Flags().BoolVar(&backupCompress, "compress", true, "Compress archive with gzip")

	// restore command
	var restoreBackup, restoreDB string
	var restoreForce bool

	restoreCmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a VayuPress database from a backup archive",
		RunE: func(cmd *cobra.Command, args []string) error {
			if restoreBackup == "" {
				return fmt.Errorf("--backup is required")
			}
			if restoreDB == "" {
				return fmt.Errorf("--db is required")
			}
			if err := backup.Restore(backup.RestoreOptions{
				BackupPath: restoreBackup,
				DBPath:     restoreDB,
				Force:      restoreForce,
			}); err != nil {
				return err
			}
			fmt.Printf("Database restored to: %s\n", restoreDB)
			return nil
		},
	}
	restoreCmd.Flags().StringVar(&restoreBackup, "backup", "", "Path to backup archive (required)")
	restoreCmd.Flags().StringVar(&restoreDB, "db", "", "Target SQLite database path (required)")
	restoreCmd.Flags().BoolVar(&restoreForce, "force", false, "Overwrite existing database")

	// list command
	var listArchive string

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List contents of a backup archive",
		RunE: func(cmd *cobra.Command, args []string) error {
			if listArchive == "" && len(args) > 0 {
				listArchive = args[0]
			}
			if listArchive == "" {
				return fmt.Errorf("archive path required (positional arg or --archive)")
			}
			manifest, err := backup.List(listArchive)
			if err != nil {
				return err
			}
			fmt.Printf("Archive: %s\n", listArchive)
			fmt.Printf("Created:       %s\n", manifest.CreatedAt.Format(time.RFC3339))
			fmt.Printf("Vayu Version:  %s\n", manifest.VayuVersion)
			fmt.Printf("Article Count: %d\n", manifest.ArticleCount)
			fmt.Printf("Files:\n")
			for _, f := range manifest.Files {
				fmt.Printf("  %-30s %10d bytes  sha256:%s\n", f.Name, f.Size, f.SHA256[:16]+"...")
			}
			return nil
		},
	}
	listCmd.Flags().StringVar(&listArchive, "archive", "", "Path to backup archive")

	// verify command
	var verifyArchive string

	verifyCmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify archive integrity using SHA256 checksums",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verifyArchive == "" && len(args) > 0 {
				verifyArchive = args[0]
			}
			if verifyArchive == "" {
				return fmt.Errorf("archive path required")
			}
			if err := backup.Verify(verifyArchive); err != nil {
				fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("OK: archive integrity verified")
			return nil
		},
	}
	verifyCmd.Flags().StringVar(&verifyArchive, "archive", "", "Path to backup archive")

	// schedule command
	scheduleCmd := &cobra.Command{
		Use:   "schedule",
		Short: "Print example crontab entries for scheduling backups",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Example crontab entries for scheduling VayuPress backups:")
			fmt.Println()
			fmt.Println("Daily backup at 2am:    0 2 * * * /usr/local/bin/vayu-backup backup --db /var/lib/vayupress/vayupress.db --out /var/backups/vayu/")
			fmt.Println("Weekly backup at 3am:   0 3 * * 0 /usr/local/bin/vayu-backup backup --db /var/lib/vayupress/vayupress.db --out /var/backups/vayu/")
			fmt.Println()
			fmt.Println("Add to crontab with: crontab -e")
		},
	}

	root.AddCommand(backupCmd, restoreCmd, listCmd, verifyCmd, scheduleCmd)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
