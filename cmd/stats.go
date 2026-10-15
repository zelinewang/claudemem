package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var (
	statsTopAccessed bool
	statsTags        string
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Display statistics about stored memory",
	Long: `Display statistics about stored notes and sessions including counts,
categories, tags, and recent activity.

With --tag, only the entries carrying all the given tags are counted, and only
counts are printed (notes, sessions, categories, top tags): no titles, no recent
activity and no storage size, so a filtered view can report its own size.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if required := splitTagList(statsTags); len(required) > 0 {
			if statsTopAccessed {
				return fmt.Errorf("--top-accessed lists titles, so it cannot be combined with --tag")
			}
			return runTaggedStats(required)
		}

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
			// If --top-accessed is set, include access data in JSON output
			if statsTopAccessed {
				if fs, ok := store.(*storage.FileStore); ok {
					topAccessed, err := fs.GetTopAccessed(10)
					if err != nil {
						return fmt.Errorf("failed to get top accessed: %w", err)
					}
					combined := struct {
						*storage.StoreStats
						TopAccessed []storage.AccessStat `json:"top_accessed"`
					}{
						StoreStats:  stats,
						TopAccessed: topAccessed,
					}
					return OutputJSON(combined)
				}
			}
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

		// Top accessed entries (token economics)
		if statsTopAccessed {
			if fs, ok := store.(*storage.FileStore); ok {
				topAccessed, err := fs.GetTopAccessed(10)
				if err != nil {
					return fmt.Errorf("failed to get top accessed: %w", err)
				}

				OutputText("")
				if len(topAccessed) == 0 {
					OutputText("Top Accessed: (no access data yet)")
				} else {
					OutputText("Top Accessed:")
					for i, a := range topAccessed {
						idShort := a.ID
						if len(idShort) > 8 {
							idShort = idShort[:8]
						}
						OutputText("  %d. [%s] %s — %d accesses (last: %s)",
							i+1, a.Type, a.Title, a.AccessCount, a.LastAccessed[:10])
					}
				}
			}
		}

		return nil
	},
}

// runTaggedStats prints the counts of the entries carrying every required tag, and nothing else.
func runTaggedStats(required []string) error {
	store, err := getUnifiedStore()
	if err != nil {
		return err
	}
	stats, err := store.TaggedStats(required)
	if err != nil {
		return fmt.Errorf("failed to get statistics: %w", err)
	}
	if outputFormat == "json" {
		return OutputJSON(stats)
	}

	OutputText("ClaudeMem Statistics (tagged %s)", strings.Join(required, ", "))
	OutputText("====================")
	OutputText("Notes:    %d", stats.TotalNotes)
	OutputText("Sessions: %d", stats.TotalSessions)
	OutputText("")
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
	if len(stats.TopTags) > 0 {
		var tagStrings []string
		for _, tag := range stats.TopTags {
			tagStrings = append(tagStrings, fmt.Sprintf("%s (%d)", tag.Name, tag.Count))
		}
		OutputText("Top Tags (top %d):", len(stats.TopTags))
		OutputText("  %s", strings.Join(tagStrings, ", "))
	}
	return nil
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
	statsCmd.Flags().BoolVar(&statsTopAccessed, "top-accessed", false, "Show most accessed entries (token economics)")
	statsCmd.Flags().StringVar(&statsTags, "tag", "", "Count only entries carrying all these tags (comma-separated); prints counts, no titles")
	rootCmd.AddCommand(statsCmd)
}
