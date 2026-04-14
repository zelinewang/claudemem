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

// Legacy repairCmd replaced by cmd/repair.go (which also does orphan
// cleanup plus FTS + vector reindex). Kept as a package-level stub here
// only because migrate.go's init() still references repairCmd; the real
// one is in repair.go.

func init() {
	migrateBraindumpCmd.Flags().String("source", "", "braindump directory (default: ~/.braindump/)")
	migrateClaudeDoneCmd.Flags().String("source", "", "claude-done directory (default: ~/.claude-done/)")

	migrateCmd.AddCommand(migrateBraindumpCmd)
	migrateCmd.AddCommand(migrateClaudeDoneCmd)

	rootCmd.AddCommand(migrateCmd)
	rootCmd.AddCommand(verifyCmd)
	// repairCmd is now registered in cmd/repair.go (richer: handles FTS+vector drift too)
}
