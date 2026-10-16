package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

var noteTagsCmd = &cobra.Command{
	Use:   "tags",
	Short: "List all note tags",
	Long:  `List all unique tags used in notes.`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		stores, err := openReadStores()
		if err != nil {
			return err
		}
		defer closeReadStores(stores)

		// Every tag of the stores read, once
		var tags []string
		seen := make(map[string]bool)
		for i, rs := range stores {
			found, err := rs.Store.GetTags()
			if err != nil {
				if err := readStoreFailed(stores, i, fmt.Errorf("failed to get tags: %w", err)); err != nil {
					return err
				}
				continue
			}
			for _, tag := range found {
				if !seen[tag] {
					seen[tag] = true
					tags = append(tags, tag)
				}
			}
		}
		if len(stores) > 1 {
			sort.Strings(tags)
		}

		if outputFormat == "json" {
			return OutputJSON(tags)
		}

		if len(tags) == 0 {
			OutputText("No tags found")
			return nil
		}

		OutputText("Tags:")
		for _, tag := range tags {
			OutputText("  • %s", tag)
		}

		return nil
	},
}

func init() {
	noteCmd.AddCommand(noteTagsCmd)
}
