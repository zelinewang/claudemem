package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/models"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var (
	injectLimit   int
	injectProject string
	injectTags    string
)

var contextInjectCmd = &cobra.Command{
	Use:   "inject",
	Short: "Generate context for session start injection",
	Long: `Generate a compact context summary from recent notes and sessions,
suitable for automatic injection at conversation start.

Designed to be called from a Claude Code SessionStart hook to provide
continuity across sessions. Returns recent knowledge and session summaries
in a token-efficient format.

With --tag, only notes and sessions carrying all the given tags are injected,
and the counts cover only them.

Examples:
  claudemem context inject
  claudemem context inject --limit 5 --format json
  claudemem context inject --project /path/to/project
  claudemem context inject --tag project-x`,
	RunE: runContextInject,
}

func init() {
	contextCmd.AddCommand(contextInjectCmd)
	contextInjectCmd.Flags().IntVar(&injectLimit, "limit", 5, "Number of recent items per category")
	contextInjectCmd.Flags().StringVar(&injectProject, "project", "", "Filter by project path")
	contextInjectCmd.Flags().StringVar(&injectTags, "tag", "", "Only notes and sessions carrying all these tags (comma-separated)")
}

type contextPayload struct {
	Tags           []string       `json:"tags,omitempty"`
	RecentNotes    []compactEntry `json:"recent_notes"`
	RecentSessions []compactEntry `json:"recent_sessions"`
	Stats          contextStats   `json:"stats"`
}

type compactEntry struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Category string `json:"category,omitempty"`
	Date     string `json:"date,omitempty"`
	Preview  string `json:"preview,omitempty"`
}

type contextStats struct {
	TotalNotes    int `json:"total_notes"`
	TotalSessions int `json:"total_sessions"`
}

func runContextInject(cmd *cobra.Command, args []string) error {
	required := splitTagList(injectTags)
	store, err := getUnifiedStore()
	if err != nil {
		return err
	}

	// Gather recent notes
	notes, err := store.ListNotesWithTags("", required)
	if err != nil {
		return fmt.Errorf("failed to list notes: %w", err)
	}

	// Gather recent sessions
	sessOpts := storage.SessionListOpts{
		Limit:   injectLimit,
		Project: injectProject,
		Tags:    required,
	}
	sessions, err := store.ListSessions(sessOpts)
	if err != nil {
		return fmt.Errorf("failed to list sessions: %w", err)
	}

	// Sort notes by updated time (most recent first) and take top N
	sortNotesByRecent(notes)
	if len(notes) > injectLimit {
		notes = notes[:injectLimit]
	}

	// Build compact entries
	recentNotes := make([]compactEntry, 0, len(notes))
	for _, n := range notes {
		preview := n.Content
		if len(preview) > 120 {
			preview = preview[:117] + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", " ")

		recentNotes = append(recentNotes, compactEntry{
			ID:       n.ID,
			Title:    n.Title,
			Category: n.Category,
			Date:     n.Updated.Format("2006-01-02"),
			Preview:  preview,
		})
	}

	recentSessions := make([]compactEntry, 0, len(sessions))
	for _, s := range sessions {
		preview := s.Summary
		if len(preview) > 120 {
			preview = preview[:117] + "..."
		}
		preview = strings.ReplaceAll(preview, "\n", " ")

		recentSessions = append(recentSessions, compactEntry{
			ID:      s.ID,
			Title:   s.Title,
			Date:    s.Date,
			Preview: preview,
		})
	}

	// Get stats for context: with --tag, the counts of the tagged set only
	var totals contextStats
	if len(required) == 0 {
		stats, err := store.Stats()
		if err != nil {
			return fmt.Errorf("failed to get stats: %w", err)
		}
		totals = contextStats{TotalNotes: stats.TotalNotes, TotalSessions: stats.TotalSessions}
	} else {
		stats, err := store.TaggedStats(required)
		if err != nil {
			return fmt.Errorf("failed to get stats: %w", err)
		}
		totals = contextStats{TotalNotes: stats.TotalNotes, TotalSessions: stats.TotalSessions}
	}

	payload := contextPayload{
		Tags:           required,
		RecentNotes:    recentNotes,
		RecentSessions: recentSessions,
		Stats:          totals,
	}

	if outputFormat == "json" {
		return OutputJSON(payload)
	}

	// Text output: concise markdown for Claude context
	if len(required) == 0 {
		OutputText("claudemem context (%d notes, %d sessions)", totals.TotalNotes, totals.TotalSessions)
	} else {
		OutputText("claudemem context (%d notes, %d sessions tagged %s)", totals.TotalNotes, totals.TotalSessions, strings.Join(required, ", "))
	}
	OutputText("")

	if len(recentNotes) > 0 {
		OutputText("Recent Notes:")
		for _, n := range recentNotes {
			OutputText("  - [%s] %s (%s) %s", n.ID[:8], n.Title, n.Category, n.Date)
		}
		OutputText("")
	}

	if len(recentSessions) > 0 {
		OutputText("Recent Sessions:")
		for _, s := range recentSessions {
			OutputText("  - [%s] %s (%s)", s.ID[:8], s.Title, s.Date)
			if s.Preview != "" {
				OutputText("    %s", s.Preview)
			}
		}
		OutputText("")
	}

	OutputText("Use: claudemem search \"<topic>\" --compact  for details")

	return nil
}

// sortNotesByRecent sorts notes by Updated time, most recent first
func sortNotesByRecent(notes []*models.Note) {
	for i := 1; i < len(notes); i++ {
		for j := i; j > 0 && notes[j].Updated.After(notes[j-1].Updated); j-- {
			notes[j], notes[j-1] = notes[j-1], notes[j]
		}
	}
}
