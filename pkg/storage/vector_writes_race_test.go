package storage

import (
	"testing"

	"github.com/zelinewang/claudemem/pkg/vectors"
)

// ─── Bulk vector writes and a move running meanwhile ──────────────────────────
//
// A backfill or a rebuild embeds every document first, which can take minutes, and writes the rows
// after. Meanwhile a move can take an entry out of the store, and a note add can put one in with its
// vectors.

// hookEmbedder embeds like storageCountingEmbedder and, on the first batch after hook is set, runs
// hook: the moment a move runs while the documents are embedded.
type hookEmbedder struct {
	storageCountingEmbedder
	hook func()
}

func (e *hookEmbedder) EmbedBatch(texts []string, it vectors.InputType) ([][]float32, error) {
	if hook := e.hook; hook != nil {
		e.hook = nil
		hook()
	}
	return e.storageCountingEmbedder.EmbedBatch(texts, it)
}

func vectorRowsOf(t *testing.T, store *FileStore, id string) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM vectors WHERE doc_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func attachHookEmbedder(t *testing.T, store *FileStore) *hookEmbedder {
	t.Helper()
	emb := &hookEmbedder{storageCountingEmbedder: storageCountingEmbedder{name: "fake-cloud", model: "test-v1", dim: 4}}
	vs, err := vectors.NewVectorStore(store.db, emb)
	if err != nil {
		t.Fatal(err)
	}
	store.vectorStore = vs
	return emb
}

// An entry that leaves the store while the backfill embeds it gets no vectors written back there.
func TestIndexMissingVectors_LeavesOutAnEntryThatLeftWhileItWasEmbedded(t *testing.T) {
	store := setupTestStore(t)
	kept := addTaggedNote(t, store, "ops", "Kept runbook", "text of the kept runbook", "team")
	moved := addTaggedNote(t, store, "misc", "Moved checklist", "text of the moved checklist", "team")
	emb := attachHookEmbedder(t, store)
	emb.hook = func() {
		if err := store.DeleteNote(moved.ID); err != nil { // as a move does to the store it leaves
			t.Error(err)
		}
	}

	indexed, missing, err := store.IndexMissingVectors()
	if err != nil {
		t.Fatal(err)
	}
	if n := vectorRowsOf(t, store, moved.ID); n != 0 {
		t.Errorf("the entry that left has %d vector rows here, want 0", n)
	}
	if n := vectorRowsOf(t, store, kept.ID); n != 1 {
		t.Errorf("the kept entry has %d vector rows, want 1", n)
	}
	if indexed != 1 || missing != 1 {
		t.Errorf("indexed %d of %d missing, want 1 of 1 (the entry that left is not missing)", indexed, missing)
	}
}

// An entry that comes in with its vectors while a rebuild embeds the others (a note added meanwhile is
// embedded as it is written) keeps them.
func TestReindexVectors_KeepsTheVectorsOfAnEntryThatCameInMeanwhile(t *testing.T) {
	store := setupTestStore(t)
	old := addTaggedNote(t, store, "ops", "Old runbook", "text of the old runbook", "team")
	emb := attachHookEmbedder(t, store)
	var arrived string
	emb.hook = func() { // a note is added, and embedded, while the rebuild embeds the others
		arrived = addTaggedNote(t, store, "misc", "Arrived checklist", "text of the arrived checklist", "team").ID
	}

	n, err := store.ReindexVectors()
	if err != nil {
		t.Fatal(err)
	}
	if got := vectorRowsOf(t, store, arrived); got != 1 {
		t.Errorf("the entry that came in has %d vector rows, want 1", got)
	}
	if got := vectorRowsOf(t, store, old.ID); got != 1 || n != 1 {
		t.Errorf("rebuilt %d documents and the old entry has %d rows, want 1 and 1", n, got)
	}
}

// An entry that leaves while the backfill embeds it, and whose file name another note takes
// meanwhile, gets no vectors written back: the file there now holds the other note.
func TestIndexMissingVectors_LeavesOutAnEntryWhoseFileNameWasTaken(t *testing.T) {
	store := setupTestStore(t)
	moved := addTaggedNote(t, store, "ops", "Deploy runbook", "text of the moved runbook", "team")
	rel := entryFile(t, store, moved.ID)
	emb := attachHookEmbedder(t, store)
	var taker string
	emb.hook = func() {
		if err := store.DeleteNote(moved.ID); err != nil { // as a move does to the store it leaves
			t.Error(err)
		}
		taker = addTaggedNote(t, store, "ops", "Deploy runbook", "another note under the same name", "team").ID
	}

	if _, _, err := store.IndexMissingVectors(); err != nil {
		t.Fatal(err)
	}
	if got := entryFile(t, store, taker); got != rel {
		t.Fatalf("the new note is at %s, not at the old name %s", got, rel)
	}
	if n := vectorRowsOf(t, store, moved.ID); n != 0 {
		t.Errorf("the entry that left has %d vector rows here, want 0", n)
	}
}

// An entry that leaves the store while the vectors are embedded gets none written back, also when a
// copy of its file comes back meanwhile (an add-only sync can bring one): the index does not list
// it, so its vectors would belong to no entry.
func TestVectorWrites_SkipAnEntryTheIndexDoesNotList(t *testing.T) {
	for _, write := range []string{"backfill", "rebuild"} {
		t.Run(write, func(t *testing.T) {
			store := setupTestStore(t)
			kept := addTaggedNote(t, store, "ops", "Kept runbook", "text of the kept runbook", "team")
			left := addTaggedNote(t, store, "misc", "Moved checklist", "text of the moved checklist", "team")
			rel := entryFile(t, store, left.ID)
			data := readRelFile(t, store, rel)
			emb := attachHookEmbedder(t, store)
			emb.hook = func() {
				if err := store.DeleteNote(left.ID); err != nil { // as a move does to the store it leaves
					t.Error(err)
				}
				writeRel(t, store, rel, data) // then a copy of its file comes back
			}
			var err error
			if write == "backfill" {
				_, _, err = store.IndexMissingVectors()
			} else {
				_, err = store.ReindexVectors()
			}
			if err != nil {
				t.Fatal(err)
			}
			if n := vectorRowsOf(t, store, left.ID); n != 0 {
				t.Errorf("the entry the index does not list has %d vector rows, want 0", n)
			}
			if n := vectorRowsOf(t, store, kept.ID); n != 1 {
				t.Errorf("the kept entry has %d vector rows, want 1", n)
			}
		})
	}
}
