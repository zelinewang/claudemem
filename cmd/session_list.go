package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var (
	listLast       int
	listDate       string
	listDateRange  string
	listBranch     string
	listProject    string
)

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List sessions",
	Long: `List saved sessions with optional filters.

Examples:
  # List last 10 sessions (default)
  claudemem session list

  # List last 20 sessions
  claudemem session list --last 20

  # List sessions from a specific date
  claudemem session list --date 2024-01-15

  # List sessions from the last 7 days
  claudemem session list --date-range 7d

  # List sessions from a date range
  claudemem session list --date-range 2024-01-01..2024-01-31

  # List sessions from a specific branch
  claudemem session list --branch feature/auth

  # Combine filters
  claudemem session list --branch main --date-range 7d --last 5`,
	RunE: runSessionList,
}

func init() {
	sessionCmd.AddCommand(sessionListCmd)

	sessionListCmd.Flags().IntVar(&listLast, "last", 0, "Number of sessions to show")
	sessionListCmd.Flags().StringVar(&listDate, "date", "", "Filter by specific date (YYYY-MM-DD)")
	sessionListCmd.Flags().StringVar(&listDateRange, "date-range", "", "Filter by date range (START..END or 7d)")
	sessionListCmd.Flags().StringVar(&listBranch, "branch", "", "Filter by branch")
	sessionListCmd.Flags().StringVar(&listProject, "project", "", "Filter by project path")
}

func runSessionList(cmd *cobra.Command, args []string) error {
	store, err := getSessionStore()
	if err != nil {
		return err
	}
	defer store.Close()

	// Build options
	opts := storage.SessionListOpts{
		Branch:  listBranch,
		Project: listProject,
		Limit:   listLast,
	}

	// Handle date filters
	if listDate != "" {
		// Specific date - set both start and end to same date
		if listDate == "today" {
			listDate = time.Now().Format("2006-01-02")
		} else if listDate == "yesterday" {
			listDate = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
		}
		opts.StartDate = listDate
		opts.EndDate = listDate
	} else if listDateRange != "" {
		// Date range
		if strings.Contains(listDateRange, "..") {
			// Explicit range: START..END
			parts := strings.Split(listDateRange, "..")
			if len(parts) == 2 {
				opts.StartDate = parts[0]
				opts.EndDate = parts[1]
			}
		} else {
			// Relative range: 7d, 30d, etc.
			start, end, err := parseDateRange(listDateRange)
			if err != nil {
				return fmt.Errorf("invalid date range: %w", err)
			}
			opts.StartDate = start
			opts.EndDate = end
		}
	}

	// Get sessions
	sessions, err := store.ListSessions(opts)
	if err != nil {
		return fmt.Errorf("failed to list sessions: %w", err)
	}

	if len(sessions) == 0 {
		OutputText("No sessions found.")
		return nil
	}

	// Output results
	if outputFormat == "json" {
		return OutputJSON(sessions)
	} else {
		// Text format - table
		OutputText(fmt.Sprintf("%-10s %-20s %-50s %-10s", "Date", "Branch", "Title", "ID"))
		OutputText(strings.Repeat("-", 90))

		for _, session := range sessions {
			branch := session.Branch
			if len(branch) > 18 {
				branch = branch[:15] + "..."
			}

			title := session.Title
			if len(title) > 48 {
				title = title[:45] + "..."
			}

			idPrefix := session.ID
			if len(idPrefix) > 8 {
				idPrefix = idPrefix[:8]
			}

			OutputText(fmt.Sprintf("%-10s %-20s %-50s %-10s",
				session.Date, branch, title, idPrefix))
		}

		OutputText(fmt.Sprintf("\nTotal: %d sessions", len(sessions)))
	}

	return nil
}

// parseDateRange parses relative date ranges like "7d" or "today"
func parseDateRange(s string) (string, string, error) {
	now := time.Now()
	today := now.Format("2006-01-02")

	switch s {
	case "today":
		return today, today, nil
	case "yesterday":
		yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
		return yesterday, yesterday, nil
	case "week", "7d":
		weekAgo := now.AddDate(0, 0, -7).Format("2006-01-02")
		return weekAgo, today, nil
	case "month", "30d":
		monthAgo := now.AddDate(0, 0, -30).Format("2006-01-02")
		return monthAgo, today, nil
	default:
		// Check if it's a number followed by 'd' (days)
		if strings.HasSuffix(s, "d") {
			var days int
			if _, err := fmt.Sscanf(s, "%dd", &days); err == nil {
				start := now.AddDate(0, 0, -days).Format("2006-01-02")
				return start, today, nil
			}
		}
		return "", "", fmt.Errorf("unsupported format: %s", s)
	}
}