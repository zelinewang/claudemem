package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/models"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var (
	searchBranch      string
	searchDateRange   string
	sessionSearchTags []string
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

  # Only sessions carrying all these tags
  claudemem session search "deploy" --tag team

  # Combine filters
  claudemem session search "database" --branch feature/db --date-range 30d`,
	Args: cobra.ExactArgs(1),
	RunE: runSessionSearch,
}

func init() {
	sessionCmd.AddCommand(sessionSearchCmd)

	sessionSearchCmd.Flags().StringVar(&searchBranch, "branch", "", "Filter by branch")
	sessionSearchCmd.Flags().StringVar(&searchDateRange, "date-range", "", "Filter by date range (START..END or 7d)")
	sessionSearchCmd.Flags().StringArrayVar(&sessionSearchTags, "tag", nil, "Only sessions carrying all these tags (comma-separated or repeated)")
}

func runSessionSearch(cmd *cobra.Command, args []string) error {
	query := args[0]
	tags, err := tagFlag(cmd, "tag")
	if err != nil {
		return err
	}

	stores, err := openReadStores()
	if err != nil {
		return err
	}
	defer closeReadStores(stores)

	// Build options
	opts := storage.SessionListOpts{
		Branch: searchBranch,
		Tags:   tags,
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

	// Search sessions in every store; the ranked lists are merged by rank
	lists := make([][]listedSession, 0, len(stores))
	for i, rs := range stores {
		found, err := rs.Store.SearchSessions(query, opts)
		if err != nil {
			if err := readStoreFailed(stores, i, fmt.Errorf("failed to search sessions: %w", err)); err != nil {
				return err
			}
			continue
		}
		list := make([]listedSession, len(found))
		for j, s := range found {
			list[j] = listedSession{session: s, store: storeLabel(stores, i)}
		}
		lists = append(lists, list)
	}
	listed := interleave(lists)
	if len(listed) > maxSessionSearchResults {
		listed = listed[:maxSessionSearchResults]
	}
	sessions := make([]*models.Session, len(listed))
	for i, l := range listed {
		sessions[i] = l.session
	}

	if len(sessions) == 0 {
		OutputText(fmt.Sprintf("No sessions found matching \"%s\"", query))
		return nil
	}

	// Output results
	if outputFormat == "json" {
		if len(stores) < 2 {
			return OutputJSON(sessions)
		}
		labelled := make([]sessionInStore, len(listed))
		for i, l := range listed {
			labelled[i] = sessionInStore{Session: l.session, Store: l.store}
		}
		return OutputJSON(labelled)
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

			if listed[i].store != "" {
				OutputText(fmt.Sprintf("%d. [%s] %s (%s, %s) [%s]",
					i+1, idPrefix, session.Title, session.Date, session.Branch, listed[i].store))
			} else {
				OutputText(fmt.Sprintf("%d. [%s] %s (%s, %s)",
					i+1, idPrefix, session.Title, session.Date, session.Branch))
			}
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

// maxSessionSearchResults is what SearchSessions returns from one store; merged results from several
// stores are cut to the same number.
const maxSessionSearchResults = 50
