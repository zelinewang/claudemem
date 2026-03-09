package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var sessionGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Get a specific session by ID",
	Long: `Retrieve and display a specific session by its ID or ID prefix.

Examples:
  # Get session by full ID
  claudemem session get 12345678-abcd-efgh-ijkl-mnopqrstuvwx

  # Get session by ID prefix (first 8 characters)
  claudemem session get 12345678

  # Get session in JSON format
  claudemem session get 12345678 --format json`,
	Args: cobra.ExactArgs(1),
	RunE: runSessionGet,
}

func init() {
	sessionCmd.AddCommand(sessionGetCmd)
}

func runSessionGet(cmd *cobra.Command, args []string) error {
	id := args[0]

	store, err := getSessionStore()
	if err != nil {
		return err
	}
	defer store.Close()

	// Get session
	session, err := store.GetSession(id)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}

	// Log access (non-blocking, best-effort)
	if fs, ok := store.(*storage.FileStore); ok {
		fs.LogAccess(session.ID, "get")
	}

	// Output result
	if outputFormat == "json" {
		return OutputJSON(session)
	} else {
		// Text format - display full session
		idPrefix := session.ID
		if len(idPrefix) > 8 {
			idPrefix = idPrefix[:8]
		}

		OutputText(strings.Repeat("=", 80))
		OutputText(fmt.Sprintf("Session: %s", session.Title))
		OutputText(strings.Repeat("-", 80))
		OutputText(fmt.Sprintf("ID:         %s", idPrefix))
		OutputText(fmt.Sprintf("Date:       %s", session.Date))
		OutputText(fmt.Sprintf("Branch:     %s", session.Branch))
		OutputText(fmt.Sprintf("Project:    %s", session.Project))
		OutputText(fmt.Sprintf("Session ID: %s", session.SessionID))
		if len(session.Tags) > 0 {
			OutputText(fmt.Sprintf("Tags:       %s", strings.Join(session.Tags, ", ")))
		}
		OutputText(fmt.Sprintf("Created:    %s", session.Created.Format("2006-01-02 15:04:05")))
		OutputText(strings.Repeat("-", 80))

		// Display sections
		if session.Summary != "" {
			OutputText("\n## Summary")
			OutputText(session.Summary)
		}

		if len(session.Decisions) > 0 {
			OutputText("\n## Key Decisions")
			for _, decision := range session.Decisions {
				OutputText(fmt.Sprintf("- %s", decision))
			}
		}

		if len(session.Changes) > 0 {
			OutputText("\n## What Changed")
			for _, change := range session.Changes {
				OutputText(fmt.Sprintf("- %s — %s", change.Path, change.Description))
			}
		}

		if len(session.Problems) > 0 {
			OutputText("\n## Problems & Solutions")
			for _, ps := range session.Problems {
				OutputText(fmt.Sprintf("- Problem: %s", ps.Problem))
				if ps.Solution != "" {
					OutputText(fmt.Sprintf("  Solution: %s", ps.Solution))
				}
			}
		}

		if len(session.Questions) > 0 {
			OutputText("\n## Questions Raised")
			for _, question := range session.Questions {
				OutputText(fmt.Sprintf("- %s", question))
			}
		}

		if len(session.NextSteps) > 0 {
			OutputText("\n## Next Steps")
			for _, step := range session.NextSteps {
				OutputText(fmt.Sprintf("- [ ] %s", step))
			}
		}

		OutputText(strings.Repeat("=", 80))
	}

	return nil
}