package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/models"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var noteGetTags []string

// noteGetCmd represents the note get command
var noteGetCmd = &cobra.Command{
	Use:   "get <id-or-category> [title-pattern]",
	Short: "Get a specific note",
	Long: `Get a specific note by ID or by category and title.

With --tag, only notes carrying all the given tags are considered: a note
without them is reported exactly like a note that does not exist.

When the store reads other stores (stores.read), they are searched after it,
in order, and the output names the store the note came from.

Examples:
  claudemem note get abc12345                    # Get by ID (or ID prefix)
  claudemem note get work "Meeting Notes"        # Get by category and title
  claudemem note get work meeting                # Search in category by title pattern
  claudemem note get abc12345 --tag team         # Only if the note carries team`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		required, err := tagFlag(cmd, "tag")
		if err != nil {
			return err
		}
		stores, err := openReadStores()
		if err != nil {
			return err
		}
		defer closeReadStores(stores)

		// Single argument: treat as ID
		if len(args) == 1 {
			var note *models.Note
			where := -1
			var firstErr error
			for i, rs := range stores {
				n, err := rs.Store.GetNoteWithTags(args[0], required)
				if err != nil {
					if i == 0 {
						firstErr = err
					}
					continue
				}
				if note == nil {
					note, where = n, i
					continue
				}
				fmt.Fprintf(os.Stderr, "warning: %s also matches a note in %s; showing the one in %s\n", args[0], rs.Name, stores[where].Name)
			}
			if note == nil {
				return fmt.Errorf("failed to get note: %w", firstErr)
			}

			// Log access (non-blocking, best-effort)
			stores[where].Store.LogAccess(note.ID, "get")
			return outputNote(note, storeLabel(stores, where))
		}

		// Two arguments: category and title/pattern
		category := args[0]
		titlePattern := args[1]

		// First try exact match, store by store
		for i, rs := range stores {
			note, err := rs.Store.GetNoteByTitle(category, titlePattern)
			if err == nil && storage.HasAllTags(note.Tags, required) {
				// Log access (non-blocking, best-effort)
				rs.Store.LogAccess(note.ID, "get")
				return outputNote(note, storeLabel(stores, i))
			}
		}

		// If not found, list the category in every store and filter by the pattern
		patternLower := strings.ToLower(titlePattern)
		var matches []*models.Note
		var matchStores []int
		for i, rs := range stores {
			notes, err := rs.Store.ListNotesWithTags(category, required)
			if err != nil {
				if err := readStoreFailed(stores, i, fmt.Errorf("failed to list notes: %w", err)); err != nil {
					return err
				}
				continue
			}
			for _, n := range notes {
				if strings.Contains(strings.ToLower(n.Title), patternLower) {
					matches = append(matches, n)
					matchStores = append(matchStores, i)
				}
			}
		}

		if len(matches) == 0 {
			return fmt.Errorf("no notes found in %s matching \"%s\"", category, titlePattern)
		}

		if len(matches) == 1 {
			// Single match, show it
			stores[matchStores[0]].Store.LogAccess(matches[0].ID, "get")
			return outputNote(matches[0], storeLabel(stores, matchStores[0]))
		}

		// Multiple matches, list them
		if outputFormat == "json" {
			if len(stores) < 2 {
				return OutputJSON(matches)
			}
			labelled := make([]noteInStore, len(matches))
			for i, n := range matches {
				labelled[i] = noteInStore{Note: n, Store: storeLabel(stores, matchStores[i])}
			}
			return OutputJSON(labelled)
		}

		OutputText("Multiple notes found in %s matching \"%s\":", category, titlePattern)
		for i, n := range matches {
			idShort := n.ID
			if len(idShort) > 8 {
				idShort = idShort[:8]
			}
			if label := storeLabel(stores, matchStores[i]); label != "" {
				OutputText("  [%s] %s (%s)", idShort, n.Title, label)
				continue
			}
			OutputText("  [%s] %s", idShort, n.Title)
		}
		OutputText("\nUse the full title or note ID to get a specific note")

		return nil
	},
}

// noteInStore is a note with the store it came from, for JSON output when several stores are read.
type noteInStore struct {
	*models.Note
	Store string `json:"store,omitempty"`
}

// outputNote prints one note; store, when set, names the store it came from.
func outputNote(note *models.Note, store string) error {
	if outputFormat == "json" {
		if store != "" {
			return OutputJSON(noteInStore{Note: note, Store: store})
		}
		return OutputJSON(note)
	}

	// Text output: formatted note
	OutputText("ID: %s", note.ID[:8])
	if store != "" {
		OutputText("Store: %s", store)
	}
	OutputText("Category: %s", note.Category)
	OutputText("Title: %s", note.Title)
	if len(note.Tags) > 0 {
		OutputText("Tags: %s", strings.Join(note.Tags, ", "))
	}
	OutputText("Created: %s", note.Created.Format("2006-01-02 15:04:05"))
	OutputText("Updated: %s", note.Updated.Format("2006-01-02 15:04:05"))
	OutputText("\n%s", note.Content)
	return nil
}

func init() {
	noteCmd.AddCommand(noteGetCmd)
	noteGetCmd.Flags().StringArrayVar(&noteGetTags, "tag", nil, "Only consider notes carrying all these tags (comma-separated or repeated); others read as not found")
}
