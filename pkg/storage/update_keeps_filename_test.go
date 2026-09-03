package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
)

// A note's filename is a stable id under add-only cross-machine sync (2026-09-03): a title change
// must not produce a new file, or the peer's add-only push resurrects the old one and two files
// claim the same uuid (the daily reindex on the hub aborted on exactly that for two days).
// Only a category change moves the file.
func TestUpdateNote_TitleChangeKeepsFilename(t *testing.T) {
	store := setupTestStore(t)
	note := models.NewNote("infrastructure", "Incident 2026-09-02 chat lane down", "body")
	result, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	pathBefore := entryPath(t, store, result.NoteID)

	n, err := store.GetNote(result.NoteID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	n.Title = "Incident 2026-09-02 RESOLVED chat lane down (renamed)"
	if err := store.UpdateNote(n); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}

	pathAfter := entryPath(t, store, result.NoteID)
	if pathAfter != pathBefore {
		t.Fatalf("filename changed on a title update: %s -> %s", pathBefore, pathAfter)
	}
	full := filepath.Join(store.baseDir, pathAfter)
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("note file missing after update: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.notesDir, "infrastructure", Slugify(n.Title))); err == nil {
		t.Fatalf("a new slug file was created for the renamed title")
	}
	got, err := store.GetNote(result.NoteID)
	if err != nil {
		t.Fatalf("GetNote after update: %v", err)
	}
	if got.Title != n.Title {
		t.Fatalf("title not updated in the index: %q", got.Title)
	}
	data, err := os.ReadFile(full)
	if err != nil || !strings.Contains(string(data), "RESOLVED") {
		t.Fatalf("file on disk does not carry the new title (err=%v)", err)
	}
	entries, err := os.ReadDir(filepath.Join(store.notesDir, "infrastructure"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	files := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			files++
		}
	}
	if files != 1 {
		t.Fatalf("expected exactly one note file after the rename, found %d", files)
	}

	// a second title change keeps it stable too, and reindex from disk agrees with the index
	n.Title = "Incident renamed twice"
	if err := store.UpdateNote(n); err != nil {
		t.Fatalf("second UpdateNote: %v", err)
	}
	if p := entryPath(t, store, result.NoteID); p != pathBefore {
		t.Fatalf("filename changed on the second title update: %s -> %s", pathBefore, p)
	}
	if _, err := store.Reindex(); err != nil {
		t.Fatalf("Reindex after renames: %v", err)
	}
	if p := entryPath(t, store, result.NoteID); p != pathBefore {
		t.Fatalf("reindex from disk disagrees with the index: %s vs %s", p, pathBefore)
	}
}

func TestUpdateNote_CategoryChangeMovesFile(t *testing.T) {
	store := setupTestStore(t)
	note := models.NewNote("infrastructure", "Moving note", "body")
	result, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	// rename first so the kept filename (moving-note.md) and the current slug differ: the category move
	// must re-slugify from the CURRENT title, and a test whose slug equals the kept name cannot tell
	// "kept the basename" from "re-slugified" (review P2-3, mutation M1)
	n, err := store.GetNote(result.NoteID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	n.Title = "Moving note renamed"
	if err := store.UpdateNote(n); err != nil {
		t.Fatalf("UpdateNote (rename): %v", err)
	}
	pathBefore := entryPath(t, store, result.NoteID)
	if filepath.Base(pathBefore) != "moving-note.md" {
		t.Fatalf("rename changed the filename: %s", pathBefore)
	}
	n, _ = store.GetNote(result.NoteID)
	n.Category = "decisions"
	if err := store.UpdateNote(n); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	pathAfter := entryPath(t, store, result.NoteID)
	if pathAfter == pathBefore || !strings.Contains(pathAfter, string(filepath.Separator)+"decisions"+string(filepath.Separator)) {
		t.Fatalf("category change did not move the file: %s -> %s", pathBefore, pathAfter)
	}
	if filepath.Base(pathAfter) != "moving-note-renamed.md" {
		t.Fatalf("category move must re-slugify from the current title: got %s", pathAfter)
	}
	if _, err := os.Stat(filepath.Join(store.baseDir, pathBefore)); err == nil {
		t.Fatalf("old file still present after a category change")
	}
	if _, err := os.Stat(filepath.Join(store.baseDir, pathAfter)); err != nil {
		t.Fatalf("new file missing after a category change: %v", err)
	}
}

func entryPath(t *testing.T, store *FileStore, id string) string {
	t.Helper()
	var p string
	if err := store.db.QueryRow(`SELECT filepath FROM entries WHERE id = ?`, id).Scan(&p); err != nil {
		t.Fatalf("entries row for %s: %v", id, err)
	}
	return p
}
