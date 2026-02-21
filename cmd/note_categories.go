package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
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
		// Get store
		store, err := getStore()
		if err != nil {
			return err
		}

		// Get categories
		categories, err := store.GetCategories()
		if err != nil {
			return fmt.Errorf("failed to get categories: %w", err)
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