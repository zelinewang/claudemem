package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zanelabz/claudemem/pkg/storage"
)

var (
	searchBranch    string
	searchDateRange string
)

var sessionSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search sessions by content",
	Long: `Search sessions using full-text search.

The search looks through all session content including:
- Title and summary
- Key decisions
- File changes
- Problems and solutions
- Questions and next steps
- Tags

Examples:
  # Search for sessions mentioning "authentication"
  claudemem session search "authentication"

  # Search within a specific branch
  claudemem session search "bug fix" --branch main

  # Search within a date range
  claudemem session search "refactor" --date-range 7d

  # Combine filters
  claudemem session search "database" --branch feature/db --date-range 30d`,
	Args: cobra.ExactArgs(1),
	RunE: runSessionSearch,
}

func init() {
	sessionCmd.AddCommand(sessionSearchCmd)

	sessionSearchCmd.Flags().StringVar(&searchBranch, "branch", "", "Filter by branch")
	sessionSearchCmd.Flags().StringVar(&searchDateRange, "date-range", "", "Filter by date range (START..END or 7d)")
}

func runSessionSearch(cmd *cobra.Command, args []string) error {
	query := args[0]

	store, err := getSessionStore()
	if err != nil {
		return err
	}
	defer store.Close()

	// Build options
	opts := storage.SessionListOpts{
		Branch: searchBranch,
	}

	// Handle date range
	if searchDateRange != "" {
		if strings.Contains(searchDateRange, "..") {
			// Explicit range: START..END
			parts := strings.Split(searchDateRange, "..")
			if len(parts) == 2 {
				opts.StartDate = parts[0]
				opts.EndDate = parts[1]
			}
		} else {
			// Relative range: 7d, 30d, etc.
			start, end, err := parseDateRange(searchDateRange)
			if err != nil {
				return fmt.Errorf("invalid date range: %w", err)
			}
			opts.StartDate = start
			opts.EndDate = end
		}
	}

	// Search sessions
	sessions, err := store.SearchSessions(query, opts)
	if err != nil {
		return fmt.Errorf("failed to search sessions: %w", err)
	}

	if len(sessions) == 0 {
		OutputText(fmt.Sprintf("No sessions found matching \"%s\"", query))
		return nil
	}

	// Output results
	if outputFormat == "json" {
		return OutputJSON(sessions)
	} else {
		// Text format
		OutputText(fmt.Sprintf("Found %d sessions matching \"%s\":\n", len(sessions), query))

		for i, session := range sessions {
			idPrefix := session.ID
			if len(idPrefix) > 8 {
				idPrefix = idPrefix[:8]
			}

			// Create preview from summary
			preview := session.Summary
			if preview == "" && len(session.Decisions) > 0 {
				preview = fmt.Sprintf("Decisions: %s", session.Decisions[0])
			}
			if len(preview) > 100 {
				preview = preview[:97] + "..."
			}
			preview = strings.ReplaceAll(preview, "\n", " ")

			OutputText(fmt.Sprintf("%d. [%s] %s (%s, %s)",
				i+1, idPrefix, session.Title, session.Date, session.Branch))
			if preview != "" {
				OutputText(fmt.Sprintf("   %s", preview))
			}
			if len(session.Tags) > 0 {
				OutputText(fmt.Sprintf("   Tags: %s", strings.Join(session.Tags, ", ")))
			}
			OutputText("")
		}
	}

	return nil
}