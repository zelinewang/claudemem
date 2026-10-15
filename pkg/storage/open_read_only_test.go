package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
)

// OpenReadOnly opens a store only to read it: a directory that is not a store is refused and left
// alone, reads work, and every write is refused before it touches the store.
func TestOpenReadOnly(t *testing.T) {
	plain := t.TempDir()
	if _, err := OpenReadOnly(plain); err == nil {
		t.Error("a directory without an index was opened")
	}
	if entries, _ := os.ReadDir(plain); len(entries) != 0 {
		t.Errorf("opening a directory that is not a store wrote %d entries into it", len(entries))
	}

	dir := t.TempDir()
	writer, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	note := addTaggedNote(t, writer, "ops", "Runbook", "rokw steps", "team")
	writer.Close()

	store, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if !store.IsReadOnly() {
		t.Error("IsReadOnly is false")
	}
	if got, err := store.GetNote(note.ID); err != nil || got.Content != "rokw steps" {
		t.Errorf("GetNote = %+v, %v", got, err)
	}
	if results, err := store.SearchWithOpts(SearchOpts{Query: "rokw", Limit: 10}); err != nil || len(results) != 1 {
		t.Errorf("search = %+v, %v", results, err)
	}

	if err := store.InitVectorStore(); err == nil || store.HasVectorStore() {
		t.Errorf("a store opened read-only set up vectors from its own settings: %v", err)
	}
	store.LogAccess(note.ID, "get")
	if top, err := store.GetTopAccessed(10); err != nil || len(top) != 0 {
		t.Errorf("an access was logged in a store opened read-only: %+v, %v", top, err)
	}
	if _, err := store.AddNote(models.NewNote("ops", "New", "never written")); err == nil || !strings.Contains(err.Error(), "only for reading") {
		t.Errorf("AddNote on a read-only store: %v", err)
	}
	if err := store.DeleteNote(note.ID); err == nil {
		t.Error("DeleteNote on a read-only store succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, "notes", "ops", "new.md")); !os.IsNotExist(err) {
		t.Errorf("a refused write left a file: %v", err)
	}
	if _, err := store.DB().Exec(`DELETE FROM entries`); err == nil {
		t.Error("the index of a read-only store accepted a write")
	}
	if got, err := store.GetNote(note.ID); err != nil || got.ID != note.ID {
		t.Errorf("the note is gone after the refused writes: %v", err)
	}
}

// An index without the tables a store needs is not a store to read.
func TestOpenReadOnly_RefusesAnIndexWithoutTables(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".index"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".index", "search.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReadOnly(dir); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Errorf("an empty index: %v", err)
	}
}
