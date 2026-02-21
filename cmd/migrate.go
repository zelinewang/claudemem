package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Import data from braindump or claude-done",
	Long: `Import existing data from braindump (~/.braindump/) or claude-done (~/.claude-done/).
Both migrations are idempotent — running them twice will skip already-imported entries.`,
}

var migrateBraindumpCmd = &cobra.Command{
	Use:   "braindump [--source path]",
	Short: "Import notes from braindump (~/.braindump/)",
	RunE: func(cmd *cobra.Command, args []string) error {
		source, _ := cmd.Flags().GetString("source")
		if source == "" {
			home, _ := os.UserHomeDir()
			source = filepath.Join(home, ".braindump")
		}

		if _, err := os.Stat(source); os.IsNotExist(err) {
			return fmt.Errorf("braindump directory not found: %s\nInstall braindump first or specify --source", source)
		}

		store, err := getSessionStore()
		if err != nil {
			return err
		}
		defer store.Close()

		OutputText("Importing from %s...", source)
		result, err := store.MigrateBraindump(source)
		if err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}

		if outputFormat == "json" {
			return OutputJSON(result)
		}

		OutputText("✓ Migration complete:")
		OutputText("  Imported: %d notes", result.Imported)
		OutputText("  Skipped:  %d (already imported)", result.Skipped)
		if len(result.Errors) > 0 {
			OutputText("  Errors:   %d", len(result.Errors))
			for _, e := range result.Errors {
				OutputText("    • %s", e)
			}
		}
		return nil
	},
}

var migrateClaudeDoneCmd = &cobra.Command{
	Use:   "claude-done [--source path]",
	Short: "Import sessions from claude-done (~/.claude-done/)",
	RunE: func(cmd *cobra.Command, args []string) error {
		source, _ := cmd.Flags().GetString("source")
		if source == "" {
			home, _ := os.UserHomeDir()
			source = filepath.Join(home, ".claude-done")
		}

		if _, err := os.Stat(source); os.IsNotExist(err) {
			return fmt.Errorf("claude-done directory not found: %s\nRun /done first or specify --source", source)
		}

		store, err := getSessionStore()
		if err != nil {
			return err
		}
		defer store.Close()

		OutputText("Importing from %s...", source)
		result, err := store.MigrateClaudeDone(source)
		if err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}

		if outputFormat == "json" {
			return OutputJSON(result)
		}

		OutputText("✓ Migration complete:")
		OutputText("  Imported: %d sessions", result.Imported)
		OutputText("  Skipped:  %d (already imported)", result.Skipped)
		if len(result.Errors) > 0 {
			OutputText("  Errors:   %d", len(result.Errors))
			for _, e := range result.Errors {
				OutputText("    • %s", e)
			}
		}
		return nil
	},
}

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Check data integrity (DB-file consistency)",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := getSessionStore()
		if err != nil {
			return err
		}
		defer store.Close()

		result, err := store.VerifyIntegrity()
		if err != nil {
			return fmt.Errorf("verification failed: %w", err)
		}

		if outputFormat == "json" {
			return OutputJSON(result)
		}

		OutputText("Data Integrity Check")
		OutputText("====================")
		OutputText("DB entries: %d", result.EntryCount)
		OutputText("FTS index:  %d", result.FTSCount)

		if result.InSync {
			OutputText("\n✓ All data is in sync!")
		} else {
			OutputText("\n⚠ Issues found:")
			if result.EntryCount != result.FTSCount {
				OutputText("  • FTS index out of sync (entries: %d, FTS: %d)", result.EntryCount, result.FTSCount)
			}
			for _, o := range result.OrphanedEntries {
				OutputText("  • Orphaned [%s] %s → %s", o.Type, o.ID[:8], o.Path)
			}
			OutputText("\nRun 'claudemem repair' to fix issues.")
		}
		return nil
	},
}

var repairCmd = &cobra.Command{
	Use:   "repair",
	Short: "Repair data integrity issues",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := getSessionStore()
		if err != nil {
			return err
		}
		defer store.Close()

		removed, err := store.RepairIntegrity()
		if err != nil {
			return fmt.Errorf("repair failed: %w", err)
		}

		if removed == 0 {
			OutputText("✓ No issues found — data is healthy!")
		} else {
			OutputText("✓ Repaired: removed %d orphaned entries", removed)
		}
		return nil
	},
}

func init() {
	migrateBraindumpCmd.Flags().String("source", "", "braindump directory (default: ~/.braindump/)")
	migrateClaudeDoneCmd.Flags().String("source", "", "claude-done directory (default: ~/.claude-done/)")

	migrateCmd.AddCommand(migrateBraindumpCmd)
	migrateCmd.AddCommand(migrateClaudeDoneCmd)

	rootCmd.AddCommand(migrateCmd)
	rootCmd.AddCommand(verifyCmd)
	rootCmd.AddCommand(repairCmd)
}
