package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var noteTagsCmd = &cobra.Command{
	Use:   "tags",
	Short: "List all note tags",
	Long:  `List all unique tags used in notes.`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := getStore()
		if err != nil {
			return err
		}

		tags, err := store.GetTags()
		if err != nil {
			return fmt.Errorf("failed to get tags: %w", err)
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
