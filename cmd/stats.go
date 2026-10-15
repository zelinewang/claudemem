package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var (
	statsTopAccessed bool
	statsTags        []string
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Display statistics about stored memory",
	Long: `Display statistics about stored notes and sessions including counts,
categories, tags, and recent activity.

With --tag, only the entries carrying all the given tags are counted, and only
counts are printed (notes, sessions, categories, top tags): no titles, no recent
activity and no storage size.

When the store reads other stores (stores.read), the numbers add up all of
them, with a line per store.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		required, err := tagFlag(cmd, "tag")
		if err != nil {
			return err
		}
		if len(required) > 0 && statsTopAccessed {
			return fmt.Errorf("--top-accessed lists titles, so it cannot be combined with --tag")
		}
		stores, err := openReadStores()
		if err != nil {
			return err
		}
		defer closeReadStores(stores)
		if len(required) > 0 {
			return runTaggedStats(stores, required)
		}

		// Get statistics
		stats, totals, err := storesStats(stores)
		if err != nil {
			return fmt.Errorf("failed to get statistics: %w", err)
		}
		var topAccessed []storage.AccessStat
		if statsTopAccessed {
			if topAccessed, err = storesTopAccessed(stores, 10); err != nil {
				return fmt.Errorf("failed to get top accessed: %w", err)
			}
		}

		// Output statistics
		if outputFormat == "json" {
			if len(stores) > 1 {
				type withStores struct {
					*storage.StoreStats
					Stores []storeTotals `json:"stores"`
				}
				payload := withStores{StoreStats: stats, Stores: totals}
				if statsTopAccessed {
					return OutputJSON(struct {
						withStores
						TopAccessed []storage.AccessStat `json:"top_accessed"`
					}{withStores: payload, TopAccessed: topAccessed})
				}
				return OutputJSON(payload)
			}
			// If --top-accessed is set, include access data in JSON output
			if statsTopAccessed {
				combined := struct {
					*storage.StoreStats
					TopAccessed []storage.AccessStat `json:"top_accessed"`
				}{
					StoreStats:  stats,
					TopAccessed: topAccessed,
				}
				return OutputJSON(combined)
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
		printStoreTotals(totals)
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
				OutputText("  %s [%s] %s%s", dateStr, entry.Type, entry.Title, storeSuffix(entry.Store))
			}
		}

		// Top accessed entries (token economics)
		if statsTopAccessed {
			OutputText("")
			if len(topAccessed) == 0 {
				OutputText("Top Accessed: (no access data yet)")
			} else {
				OutputText("Top Accessed:")
				for i, a := range topAccessed {
					OutputText("  %d. [%s] %s — %d accesses (last: %s)%s",
						i+1, a.Type, a.Title, a.AccessCount, a.LastAccessed[:10], storeSuffix(a.Store))
				}
			}
		}

		return nil
	},
}

// storeTotals is one store's line in the stats of several stores.
type storeTotals struct {
	Store         string `json:"store"`
	TotalNotes    int    `json:"total_notes"`
	TotalSessions int    `json:"total_sessions"`
	StorageSize   int64  `json:"storage_size_bytes,omitempty"` // left out of tagged stats
}

// storesStats returns the stats of the stores read: one store's own stats, or the stats of several
// added up with a line per store (nil for one store).
func storesStats(stores []readStore) (*storage.StoreStats, []storeTotals, error) {
	if len(stores) == 1 {
		stats, err := stores[0].Store.Stats()
		return stats, nil, err
	}
	parts := make([]*storage.StoreStats, 0, len(stores))
	totals := make([]storeTotals, 0, len(stores))
	for i, rs := range stores {
		part, err := rs.Store.StatsAllTags()
		if err != nil {
			if err := readStoreFailed(stores, i, fmt.Errorf("%s: %w", rs.Name, err)); err != nil {
				return nil, nil, err
			}
			continue
		}
		for j := range part.RecentEntries {
			part.RecentEntries[j].Store = rs.Name
		}
		parts = append(parts, part)
		totals = append(totals, storeTotals{Store: rs.Name, TotalNotes: part.TotalNotes, TotalSessions: part.TotalSessions, StorageSize: part.StorageSize})
	}
	return storage.MergeStats(parts), totals, nil
}

// storesTopAccessed returns the limit most accessed entries of the stores read, each naming its store
// when there are several.
func storesTopAccessed(stores []readStore, limit int) ([]storage.AccessStat, error) {
	if len(stores) == 1 {
		return stores[0].Store.GetTopAccessed(limit)
	}
	all := []storage.AccessStat{}
	for i, rs := range stores {
		top, err := rs.Store.GetTopAccessed(limit)
		if err != nil {
			if err := readStoreFailed(stores, i, err); err != nil {
				return nil, err
			}
			continue
		}
		for j := range top {
			top[j].Store = storeLabel(stores, i)
		}
		all = append(all, top...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].AccessCount > all[j].AccessCount })
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// printStoreTotals prints a line per store; nothing for one store.
func printStoreTotals(totals []storeTotals) {
	if len(totals) == 0 {
		return
	}
	OutputText("Stores:")
	for _, t := range totals {
		line := fmt.Sprintf("  %s: %d notes, %d sessions", t.Store, t.TotalNotes, t.TotalSessions)
		if t.StorageSize > 0 {
			line += ", " + formatBytes(t.StorageSize)
		}
		OutputText("%s", line)
	}
}

// runTaggedStats prints the counts of the entries carrying every required tag, and nothing else.
func runTaggedStats(stores []readStore, required []string) error {
	var stats *storage.TaggedStats
	var totals []storeTotals
	if len(stores) == 1 {
		var err error
		if stats, err = stores[0].Store.TaggedStats(required); err != nil {
			return fmt.Errorf("failed to get statistics: %w", err)
		}
	} else {
		parts := make([]*storage.TaggedStats, 0, len(stores))
		for i, rs := range stores {
			part, err := rs.Store.TaggedStatsAllTags(required)
			if err != nil {
				if err := readStoreFailed(stores, i, fmt.Errorf("failed to get statistics: %s: %w", rs.Name, err)); err != nil {
					return err
				}
				continue
			}
			parts = append(parts, part)
			totals = append(totals, storeTotals{Store: rs.Name, TotalNotes: part.TotalNotes, TotalSessions: part.TotalSessions})
		}
		stats = storage.MergeTaggedStats(parts)
	}
	if outputFormat == "json" {
		if len(stores) > 1 {
			return OutputJSON(struct {
				*storage.TaggedStats
				Stores []storeTotals `json:"stores"`
			}{TaggedStats: stats, Stores: totals})
		}
		return OutputJSON(stats)
	}

	OutputText("ClaudeMem Statistics (tagged %s)", strings.Join(required, ", "))
	OutputText("====================")
	OutputText("Notes:    %d", stats.TotalNotes)
	OutputText("Sessions: %d", stats.TotalSessions)
	printStoreTotals(totals)
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
	statsCmd.Flags().StringArrayVar(&statsTags, "tag", nil, "Count only entries carrying all these tags (comma-separated or repeated); prints counts, no titles")
	rootCmd.AddCommand(statsCmd)
}
