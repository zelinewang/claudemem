//go:build !windows

package storage

import (
	"testing"
	"time"

	"github.com/zelinewang/claudemem/pkg/vectors"
)

func TestRemoveOrphanRows_RemovesOnlyOrphansAndWaitsForTheLock(t *testing.T) {
	store := setupTestStore(t)
	vs, err := vectors.NewVectorStore(store.db, &storageCountingEmbedder{name: "fake-cloud", model: "test-v1", dim: 4})
	if err != nil {
		t.Fatal(err)
	}
	store.vectorStore = vs
	kept := addTaggedNote(t, store, "ops", "Kept runbook", "text of the kept runbook", "team")
	gone := addTaggedNote(t, store, "misc", "Gone checklist", "text of the gone checklist", "team")
	if _, err := store.db.Exec(`DELETE FROM entries WHERE id = ?`, gone.ID); err != nil {
		t.Fatal(err)
	}

	unlock, err := store.lockStore()
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		removed int
		err     error
	}
	done := make(chan result, 1)
	go func() {
		n, err := store.RemoveOrphanRows()
		done <- result{n, err}
	}()
	select {
	case <-done:
		t.Error("RemoveOrphanRows ran while another writer held the store lock")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	res := <-done
	if res.err != nil {
		t.Fatal(res.err)
	}
	if res.removed != 2 { // the gone note's search row and its one vector
		t.Errorf("removed %d rows, want 2", res.removed)
	}
	for _, c := range []struct {
		query string
		id    string
		want  int
	}{
		{`SELECT COUNT(*) FROM memory_fts WHERE id = ?`, kept.ID, 1},
		{`SELECT COUNT(*) FROM vectors WHERE doc_id = ?`, kept.ID, 1},
		{`SELECT COUNT(*) FROM memory_fts WHERE id = ?`, gone.ID, 0},
		{`SELECT COUNT(*) FROM vectors WHERE doc_id = ?`, gone.ID, 0},
	} {
		var n int
		if err := store.db.QueryRow(c.query, c.id).Scan(&n); err != nil || n != c.want {
			t.Errorf("%s [%s] = %d (%v), want %d", c.query, c.id[:8], n, err, c.want)
		}
	}
}

// A store that never ran semantic search has no vectors table: only its search rows are checked.
func TestRemoveOrphanRows_WithoutAVectorsTable(t *testing.T) {
	store := setupTestStore(t)
	gone := addTaggedNote(t, store, "misc", "Gone checklist", "text of the gone checklist", "team")
	if _, err := store.db.Exec(`DELETE FROM entries WHERE id = ?`, gone.ID); err != nil {
		t.Fatal(err)
	}
	if hasVectorsByDoc(store.db) {
		t.Fatal("the test store has a vectors table")
	}
	if n, err := store.RemoveOrphanRows(); err != nil || n != 1 {
		t.Errorf("RemoveOrphanRows = %d, %v; want its 1 search row", n, err)
	}
}
