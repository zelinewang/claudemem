package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/models"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var (
	injectLimit   int
	injectProject string
	injectTags    []string
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
	contextInjectCmd.Flags().StringArrayVar(&injectTags, "tag", nil, "Only notes and sessions carrying all these tags (comma-separated or repeated)")
}

type contextPayload struct {
	Tags           []string       `json:"tags,omitempty"`
	Stores         []string       `json:"stores,omitempty"` // the stores read, when more than one
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
	Store    string `json:"store,omitempty"`
}

type contextStats struct {
	TotalNotes    int `json:"total_notes"`
	TotalSessions int `json:"total_sessions"`
}

func runContextInject(cmd *cobra.Command, args []string) error {
	required, err := tagFlag(cmd, "tag")
	if err != nil {
		return err
	}
	stores, err := openReadStores()
	if err != nil {
		return err
	}
	defer closeReadStores(stores)

	sessOpts := storage.SessionListOpts{
		Limit:   injectLimit,
		Project: injectProject,
		Tags:    required,
	}
	type noteFrom struct {
		note  *models.Note
		store string
	}
	var notes []noteFrom
	var sessions []listedSession
	var totals contextStats
	for i, rs := range stores {
		// Gather recent notes
		found, err := rs.Store.ListNotesWithTags("", required)
		if err != nil {
			return fmt.Errorf("failed to list notes: %w", err)
		}
		for _, n := range found {
			notes = append(notes, noteFrom{note: n, store: storeLabel(stores, i)})
		}

		// Gather recent sessions
		listed, err := rs.Store.ListSessions(sessOpts)
		if err != nil {
			return fmt.Errorf("failed to list sessions: %w", err)
		}
		for _, s := range listed {
			sessions = append(sessions, listedSession{session: s, store: storeLabel(stores, i)})
		}

		// Get stats for context: with --tag, the counts of the tagged set only
		if len(required) == 0 {
			stats, err := rs.Store.Stats()
			if err != nil {
				return fmt.Errorf("failed to get stats: %w", err)
			}
			totals.TotalNotes += stats.TotalNotes
			totals.TotalSessions += stats.TotalSessions
		} else {
			stats, err := rs.Store.TaggedStats(required)
			if err != nil {
				return fmt.Errorf("failed to get stats: %w", err)
			}
			totals.TotalNotes += stats.TotalNotes
			totals.TotalSessions += stats.TotalSessions
		}
	}

	// Sort notes by updated time (most recent first, ties in store order) and take top N
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].note.Updated.After(notes[j].note.Updated) })
	if len(notes) > injectLimit {
		notes = notes[:injectLimit]
	}
	// Sessions come newest first from each store; with several stores, merge them the same way
	if len(stores) > 1 {
		sort.SliceStable(sessions, func(i, j int) bool {
			a, b := sessions[i].session, sessions[j].session
			if a.Date != b.Date {
				return a.Date > b.Date
			}
			return a.Created.After(b.Created)
		})
		if len(sessions) > injectLimit {
			sessions = sessions[:injectLimit]
		}
	}

	// Build compact entries
	recentNotes := make([]compactEntry, 0, len(notes))
	for _, nf := range notes {
		n := nf.note
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
			Store:    nf.store,
		})
	}

	recentSessions := make([]compactEntry, 0, len(sessions))
	for _, ls := range sessions {
		s := ls.session
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
			Store:   ls.store,
		})
	}

	payload := contextPayload{
		Tags:           required,
		RecentNotes:    recentNotes,
		RecentSessions: recentSessions,
		Stats:          totals,
	}
	if len(stores) > 1 {
		for _, rs := range stores {
			payload.Stores = append(payload.Stores, rs.Name)
		}
	}

	if outputFormat == "json" {
		return OutputJSON(payload)
	}

	// Text output: concise markdown for Claude context
	inStores := ""
	if len(stores) > 1 {
		inStores = fmt.Sprintf(" in %d stores", len(stores))
	}
	if len(required) == 0 {
		OutputText("claudemem context (%d notes, %d sessions%s)", totals.TotalNotes, totals.TotalSessions, inStores)
	} else {
		OutputText("claudemem context (%d notes, %d sessions tagged %s%s)", totals.TotalNotes, totals.TotalSessions, strings.Join(required, ", "), inStores)
	}
	OutputText("")

	if len(recentNotes) > 0 {
		OutputText("Recent Notes:")
		for _, n := range recentNotes {
			OutputText("  - [%s] %s (%s) %s%s", n.ID[:8], n.Title, n.Category, n.Date, storeSuffix(n.Store))
		}
		OutputText("")
	}

	if len(recentSessions) > 0 {
		OutputText("Recent Sessions:")
		for _, s := range recentSessions {
			OutputText("  - [%s] %s (%s)%s", s.ID[:8], s.Title, s.Date, storeSuffix(s.Store))
			if s.Preview != "" {
				OutputText("    %s", s.Preview)
			}
		}
		OutputText("")
	}

	OutputText("Use: claudemem search \"<topic>\" --compact  for details")

	return nil
}

// storeSuffix is " [store]" for an entry from a named store, else nothing.
func storeSuffix(store string) string {
	if store == "" {
		return ""
	}
	return " [" + store + "]"
}
