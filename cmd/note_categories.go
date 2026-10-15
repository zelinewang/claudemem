package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

// noteCategoriesCmd represents the note categories command
var noteCategoriesCmd = &cobra.Command{
	Use:   "categories",
	Short: "List all note categories",
	Long: `List all note categories with their note counts.

Examples:
  claudemem note categories`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		stores, err := openReadStores()
		if err != nil {
			return err
		}
		defer closeReadStores(stores)

		// Get categories, added up by name across the stores read
		var categories []storage.CategoryInfo
		counts := make(map[string]int)
		for _, rs := range stores {
			found, err := rs.Store.GetCategories()
			if err != nil {
				return fmt.Errorf("failed to get categories: %w", err)
			}
			for _, cat := range found {
				if _, seen := counts[cat.Name]; !seen {
					categories = append(categories, storage.CategoryInfo{Name: cat.Name})
				}
				counts[cat.Name] += cat.Count
			}
		}
		for i := range categories {
			categories[i].Count = counts[categories[i].Name]
		}
		if len(stores) > 1 {
			sort.Slice(categories, func(i, j int) bool { return categories[i].Name < categories[j].Name })
		}

		// Output results
		if outputFormat == "json" {
			return OutputJSON(categories)
		}

		if len(categories) == 0 {
			OutputText("No categories found")
			return nil
		}

		OutputText("Categories:")
		for _, cat := range categories {
			OutputText("  %s (%d note%s)", cat.Name, cat.Count, pluralS(cat.Count))
		}

		return nil
	},
}

func init() {
	noteCmd.AddCommand(noteCategoriesCmd)
}

func pluralS(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
