package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

var (
	resolveProject string
	resolveWindow  string
)

var sessionResolveCmd = &cobra.Command{
	Use:   "resolve",
	Short: "Get or create a session ID for the current context",
	Long: `Resolve the active session ID for today's work on a project.

If a recent session exists (within the time window) for the same project today,
its session_id is returned for reuse. Otherwise, a new session_id is generated.

This enables multiple wrapups in the same conversation to merge into one session
via claudemem's existing dedup logic.

Examples:
  # Get session ID for current project
  claudemem session resolve --project myapp

  # With custom time window (default: 4h)
  claudemem session resolve --project myapp --window 6h`,
	RunE: runSessionResolve,
}

func init() {
	sessionCmd.AddCommand(sessionResolveCmd)

	sessionResolveCmd.Flags().StringVar(&resolveProject, "project", "", "Project name (required)")
	sessionResolveCmd.Flags().StringVar(&resolveWindow, "window", "4h", "Time window for session reuse (e.g., 4h, 2h30m)")
	sessionResolveCmd.MarkFlagRequired("project")
}

func runSessionResolve(cmd *cobra.Command, args []string) error {
	store, err := getSessionStore()
	if err != nil {
		return err
	}
	defer store.Close()

	window, err := time.ParseDuration(resolveWindow)
	if err != nil {
		return fmt.Errorf("invalid window duration %q: %w", resolveWindow, err)
	}

	sessionID, existing, err := store.ResolveSessionID(resolveProject, window)
	if err != nil {
		return fmt.Errorf("failed to resolve session ID: %w", err)
	}

	if outputFormat == "json" {
		return OutputJSON(map[string]interface{}{
			"session_id": sessionID,
			"existing":   existing,
		})
	}

	// Plain text: just print the session ID (for shell variable capture)
	fmt.Print(sessionID)

	if existing {
		fmt.Fprintf(cmd.ErrOrStderr(), "Resuming session: %s\n", sessionID)
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "New session: %s\n", sessionID)
	}

	return nil
}
