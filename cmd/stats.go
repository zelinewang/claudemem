package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Display statistics about stored memory",
	Long: `Display statistics about stored notes and sessions including counts,
categories, tags, and recent activity.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Get unified store
		store, err := getUnifiedStore()
		if err != nil {
			return err
		}

		// Get statistics
		stats, err := store.Stats()
		if err != nil {
			return fmt.Errorf("failed to get statistics: %w", err)
		}

		// Output statistics
		if outputFormat == "json" {
			return OutputJSON(stats)
		}

		// Text output - formatted dashboard
		OutputText("ClaudeMem Statistics")
		OutputText("====================")
		OutputText("Notes:    %d", stats.TotalNotes)
		OutputText("Sessions: %d", stats.TotalSessions)

		// Format storage size
		sizeStr := formatBytes(stats.StorageSize)
		OutputText("Storage:  %s", sizeStr)
		OutputText("")

		// Categories
		if len(stats.Categories) > 0 {
			OutputText("Categories (top %d):", min(5, len(stats.Categories)))
			for i, cat := range stats.Categories {
				if i >= 5 {
					break
				}
				plural := "note"
				if cat.Count != 1 {
					plural = "notes"
				}
				OutputText("  %-15s (%d %s)", cat.Name, cat.Count, plural)
			}
			OutputText("")
		}

		// Tags
		if len(stats.TopTags) > 0 {
			OutputText("Top Tags (top %d):", min(10, len(stats.TopTags)))
			var tagStrings []string
			for i, tag := range stats.TopTags {
				if i >= 10 {
					break
				}
				tagStrings = append(tagStrings, fmt.Sprintf("%s (%d)", tag.Name, tag.Count))
			}
			OutputText("  %s", strings.Join(tagStrings, ", "))
			OutputText("")
		}

		// Recent activity
		if len(stats.RecentEntries) > 0 {
			OutputText("Recent Activity:")
			for _, entry := range stats.RecentEntries {
				dateStr := entry.Created.Format("2006-01-02")
				OutputText("  %s [%s] %s", dateStr, entry.Type, entry.Title)
			}
		}

		return nil
	},
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func init() {
	rootCmd.AddCommand(statsCmd)
}