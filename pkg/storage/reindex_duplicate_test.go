package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zelinewang/claudemem/pkg/models"
)

// writeNoteFile writes a raw note markdown file directly into the store's
// notes dir, bypassing AddNote — simulating a file that arrived via
// cross-machine file union (rsync) rather than through the API.
func writeNoteFile(t *testing.T, fs *FileStore, category, filename string, note *models.Note) string {
	t.Helper()
	dir := filepath.Join(fs.notesDir, category)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(FormatNoteMarkdown(note)), 0600); err != nil {
		t.Fatalf("write note file: %v", err)
	}
	return path
}

// Reindex must survive two files claiming the same uuid (a stale old-slug
// file resurrected by add-only cross-machine sync after a title rename)
// and must keep the file with the newest updated timestamp — regardless
// of which file the directory walk visits first.
func TestReindexSurvivesDuplicateUUID(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	older := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)

	mkNote := func(title string, updated time.Time) *models.Note {
		return &models.Note{
			ID:       id,
			Type:     "note",
			Category: "infrastructure",
			Title:    title,
			Content:  "body of " + title,
			Tags:     []string{"test"},
			Created:  older,
			Updated:  updated,
		}
	}

	cases := []struct {
		name      string
		staleFile string // filename sorts BEFORE or AFTER the current one
	}{
		// filepath.Walk visits lexicographically: "a-..." first, "z-..." last
		{"stale visited first", "a-old-slug.md"},
		{"stale visited last", "z-old-slug.md"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := setupTestStore(t)

			currentPath := writeNoteFile(t, store, "infrastructure", "m-current-slug.md",
				mkNote("current title", newer))
			writeNoteFile(t, store, "infrastructure", tc.staleFile,
				mkNote("old title", older))

			count, err := store.Reindex()
			if err != nil {
				t.Fatalf("Reindex() must survive duplicate uuid, got error: %v", err)
			}
			if count != 1 {
				t.Errorf("count = %d, want 1 (one logical note)", count)
			}

			var gotPath, gotTitle string
			err = store.db.QueryRow(
				`SELECT filepath, title FROM entries WHERE id = ?`, id,
			).Scan(&gotPath, &gotTitle)
			if err != nil {
				t.Fatalf("query indexed entry: %v", err)
			}
			wantRel, _ := filepath.Rel(store.baseDir, currentPath)
			if gotPath != wantRel {
				t.Errorf("indexed filepath = %q, want newest file %q", gotPath, wantRel)
			}
			if gotTitle != "current title" {
				t.Errorf("indexed title = %q, want %q", gotTitle, "current title")
			}
		})
	}
}
