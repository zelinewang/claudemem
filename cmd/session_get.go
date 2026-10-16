package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/models"
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
  claudemem session get 12345678 --format json

  # Only if the session carries team; otherwise it says that it does not
  claudemem session get 12345678 --tag team`,
	Args: cobra.ExactArgs(1),
	RunE: runSessionGet,
}

var sessionGetTags []string

func init() {
	sessionCmd.AddCommand(sessionGetCmd)
	sessionGetCmd.Flags().StringArrayVar(&sessionGetTags, "tag", nil, "Only consider sessions carrying all these tags (comma-separated or repeated)")
}

func runSessionGet(cmd *cobra.Command, args []string) error {
	id := args[0]
	required, err := tagFlag(cmd, "tag")
	if err != nil {
		return err
	}

	stores, err := openReadStores()
	if err != nil {
		return err
	}
	defer closeReadStores(stores)

	// Get session: the primary store first, then the stores it reads
	var session *models.Session
	where := -1
	var firstErr, untagged error
	for i, rs := range stores {
		s, err := rs.Store.GetSessionWithTags(id, required)
		if err != nil {
			if i == 0 {
				firstErr = err
			}
			if untagged == nil {
				untagged = missingTagsIn(stores, i, err)
			}
			continue
		}
		if session == nil {
			session, where = s, i
			continue
		}
		fmt.Fprintf(os.Stderr, "warning: %s also matches a session in %s; showing the one in %s\n", id, rs.Name, stores[where].Name)
	}
	if session == nil {
		if untagged != nil {
			return fmt.Errorf("failed to get session: %w", untagged)
		}
		return fmt.Errorf("failed to get session: %w", firstErr)
	}

	// Log access (non-blocking, best-effort)
	stores[where].Store.LogAccess(session.ID, "get")
	label := storeLabel(stores, where)

	// Output result
	if outputFormat == "json" {
		if label != "" {
			return OutputJSON(sessionInStore{Session: session, Store: label})
		}
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
		if label != "" {
			OutputText(fmt.Sprintf("Store:      %s", label))
		}
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

// sessionInStore is a session with the store it came from, for JSON output when several stores are
// read.
type sessionInStore struct {
	*models.Session
	Store string `json:"store,omitempty"`
}
