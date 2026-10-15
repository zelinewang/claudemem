//go:build !windows

package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zelinewang/claudemem/pkg/vectors"
)

// ─── Writers of the index take the store lock ─────────────────────────────────
//
// A move commits an entry's rows in the target and then renames its file into place, holding both
// stores' locks. A writer that rebuilds or repairs the index without the lock and runs in between
// drops the moved entry's rows, and the move reports it moved (review of the move, finding 3).

// moveWindow puts the store in the state a move leaves between an entry's rows and its file: it
// takes the store lock and sets the entry's file aside. restore puts the file back and releases
// the lock.
func moveWindow(t *testing.T, store *FileStore, id string) (restore func()) {
	t.Helper()
	unlock, err := store.lockStore()
	if err != nil {
		t.Fatal(err)
	}
	path, _ := readStoreFile(t, store, id)
	aside := filepath.Join(t.TempDir(), "aside.md")
	if err := os.Rename(path, aside); err != nil {
		unlock()
		t.Fatal(err)
	}
	return func() {
		if err := os.Rename(aside, path); err != nil {
			t.Error(err)
		}
		unlock()
	}
}

func TestIndexWritersWaitForAMoveInProgress(t *testing.T) {
	for _, w := range []struct {
		name string
		run  func(*FileStore) error
	}{
		{"Reindex", func(s *FileStore) error { _, err := s.Reindex(); return err }},
		{"RepairIntegrity", func(s *FileStore) error { _, err := s.RepairIntegrity(); return err }},
		{"PruneStaleVectors", func(s *FileStore) error { _, err := s.PruneStaleVectors(); return err }},
	} {
		t.Run(w.name, func(t *testing.T) {
			store := setupTestStore(t)
			vs, err := vectors.NewVectorStore(store.db, &storageCountingEmbedder{name: "fake-cloud", model: "test-v1", dim: 4})
			if err != nil {
				t.Fatal(err)
			}
			store.vectorStore = vs
			note := addTaggedNote(t, store, "ops", "Moved runbook", "text of the moved runbook", "team")

			restore := moveWindow(t, store, note.ID)
			done := make(chan error, 1)
			go func() { done <- w.run(store) }()
			ranDuringTheMove := false
			select {
			case err := <-done:
				ranDuringTheMove = true
				done <- err
			case <-time.After(300 * time.Millisecond):
			}
			restore()
			if err := <-done; err != nil {
				t.Fatalf("%s: %v", w.name, err)
			}
			if ranDuringTheMove {
				t.Errorf("%s ran while a move held the store lock", w.name)
			}
			for _, table := range []string{"entries", "memory_fts"} {
				var n int
				if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE id = ?`, note.ID).Scan(&n); err != nil || n != 1 {
					t.Errorf("after %s the moved note has %d rows in %s (%v), want 1", w.name, n, table, err)
				}
			}
		})
	}
}
