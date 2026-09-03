package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
)

// Review of PR #21, P1-1: once the filename stopped changing with the title, two concurrent updates
// of one note shared the temp file newPath+".tmp" and the loser's recovery branch wrote an empty
// note. The temp file is unique now; no iteration may leave a zero-byte or stale file behind.
func TestUpdateNote_ConcurrentUpdatesNeverTruncate(t *testing.T) {
	const iterations = 30
	for i := 0; i < iterations; i++ {
		store := setupTestStore(t)
		note := models.NewNote("infrastructure", "Race Base Title", "BODY-ZERO")
		r, err := store.AddNote(note)
		if err != nil {
			t.Fatalf("AddNote: %v", err)
		}
		var wg sync.WaitGroup
		for w := 0; w < 2; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				n, err := store.GetNote(r.NoteID)
				if err != nil {
					return
				}
				n.Title = fmt.Sprintf("Race Renamed By Worker %d", w)
				n.Content = fmt.Sprintf("BODY-FROM-WORKER-%d", w)
				_ = store.UpdateNote(n)
			}(w)
		}
		wg.Wait()
		p := entryPath(t, store, r.NoteID)
		full := filepath.Join(store.baseDir, p)
		st, err := os.Stat(full)
		if err != nil {
			t.Fatalf("iter %d: note file gone: %v", i, err)
		}
		if st.Size() == 0 {
			t.Fatalf("iter %d: note file %s truncated to zero bytes", i, p)
		}
		body, _ := os.ReadFile(full)
		if !strings.Contains(string(body), "BODY-FROM-WORKER-") {
			t.Fatalf("iter %d: file holds neither worker's content:\n%s", i, string(body))
		}
		ents, _ := os.ReadDir(filepath.Join(store.notesDir, "infrastructure"))
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".tmp") {
				t.Fatalf("iter %d: stray temp file left behind: %s", i, e.Name())
			}
		}
		store.Close()
	}
}

// A legacy entries.filepath without the .md suffix falls back to the slug and the old file is removed
// (review P2-3, mutation M3: this branch had no coverage).
func TestUpdateNote_LegacyNonMarkdownPathFallsBackToSlug(t *testing.T) {
	store := setupTestStore(t)
	note := models.NewNote("infrastructure", "Legacy Note Title", "LEGACY")
	r, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	p := entryPath(t, store, r.NoteID)
	legacyRel := filepath.Join("notes", "infrastructure", "legacy-note-title")
	if err := os.Rename(filepath.Join(store.baseDir, p), filepath.Join(store.baseDir, legacyRel)); err != nil {
		t.Fatalf("rename to the legacy name: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE entries SET filepath = ? WHERE id = ?`, legacyRel, r.NoteID); err != nil {
		t.Fatalf("patch row: %v", err)
	}
	n, err := store.GetNote(r.NoteID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	n.Title = "Legacy Note Renamed"
	if err := store.UpdateNote(n); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	after := entryPath(t, store, r.NoteID)
	if filepath.Base(after) != "legacy-note-renamed.md" {
		t.Fatalf("legacy path should fall back to the current slug: got %s", after)
	}
	if _, err := os.Stat(filepath.Join(store.baseDir, legacyRel)); err == nil {
		t.Fatalf("legacy extensionless file left behind at %s", legacyRel)
	}
	if _, err := os.Stat(filepath.Join(store.baseDir, after)); err != nil {
		t.Fatalf("new file missing: %v", err)
	}
}

// Review P2-1: the direct-path branch of GetNoteByTitle must not return a renamed note under its dead
// title, and must still find the note under its current title.
func TestGetNoteByTitle_DeadTitleIsNotFoundAfterRename(t *testing.T) {
	store := setupTestStore(t)
	note := models.NewNote("infrastructure", "Old Title Here", "BODY")
	r, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	n, _ := store.GetNote(r.NoteID)
	n.Title = "New Title Here"
	if err := store.UpdateNote(n); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	if got, err := store.GetNoteByTitle("infrastructure", "Old Title Here"); err == nil {
		t.Fatalf("dead title still resolves to note %s (%q)", got.ID[:8], got.Title)
	}
	got, err := store.GetNoteByTitle("infrastructure", "New Title Here")
	if err != nil || got.ID != r.NoteID {
		t.Fatalf("current title not found after rename: err=%v", err)
	}
}

// Review P2-4: a corrupted entries.filepath pointing outside the store must never make UpdateNote
// delete or write outside it.
func TestUpdateNote_NeverTouchesPathsOutsideTheStore(t *testing.T) {
	store := setupTestStore(t)
	note := models.NewNote("infrastructure", "Outside Note Title", "OUTSIDE")
	r, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "victim.md")
	if err := os.WriteFile(outside, []byte("DO NOT DELETE"), 0600); err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(store.baseDir, outside) // a ../../… path
	if _, err := store.db.Exec(`UPDATE entries SET filepath = ? WHERE id = ?`, rel, r.NoteID); err != nil {
		t.Fatalf("patch row: %v", err)
	}
	n, err := store.readNoteFile(filepath.Join(store.notesDir, "infrastructure", "outside-note-title.md"))
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	n.Title = "Outside Note Renamed"
	_ = store.UpdateNote(n)
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("UpdateNote deleted a file outside the store: %v", err)
	}
	after := entryPath(t, store, r.NoteID)
	if strings.Contains(after, "..") || !strings.HasPrefix(after, "notes/") {
		t.Fatalf("new path escaped the notes tree: %s", after)
	}
}
