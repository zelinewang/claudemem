package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var (
	sessionUpdateAddTags    []string
	sessionUpdateRemoveTags []string
)

var sessionUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Add or remove tags on a session",
	Long: `Add or remove tags on a saved session, keeping its other tags.

Only the tags line of the session's file changes: the report, its dates and its
embedding stay as they are. The session content itself is written by
"session save" (a re-save with the same --session-id merges into it).

The ID can be a full ID or a prefix (first 8 characters).

Examples:
  claudemem session update abc12345 --add-tag team
  claudemem session update abc12345 --add-tag team --remove-tag personal`,
	Args: cobra.ExactArgs(1),
	RunE: runSessionUpdate,
}

func init() {
	sessionCmd.AddCommand(sessionUpdateCmd)
	sessionUpdateCmd.Flags().StringArrayVar(&sessionUpdateAddTags, "add-tag", nil, "Tags to add (comma-separated or repeated)")
	sessionUpdateCmd.Flags().StringArrayVar(&sessionUpdateRemoveTags, "remove-tag", nil, "Tags to remove (comma-separated or repeated)")
}

func runSessionUpdate(cmd *cobra.Command, args []string) error {
	addTags, err := tagFlag(cmd, "add-tag")
	if err != nil {
		return err
	}
	removeTags, err := tagFlag(cmd, "remove-tag")
	if err != nil {
		return err
	}
	if len(addTags) == 0 && len(removeTags) == 0 {
		return fmt.Errorf("no updates specified (use --add-tag or --remove-tag)")
	}
	if err := storage.ValidateTagEdit(addTags, removeTags); err != nil {
		return err
	}

	store, err := getSessionStore()
	if err != nil {
		return err
	}
	defer store.Close()

	session, err := store.GetSession(args[0])
	if err != nil {
		return readStoreHint(fmt.Errorf("failed to get session: %w", err), "session", args[0])
	}
	res, err := store.EditTags("session", session.ID, addTags, removeTags, false)
	if err != nil {
		return fmt.Errorf("failed to update session tags: %w", err)
	}
	return printTagEdit(res)
}
