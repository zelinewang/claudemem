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

// Review of PR #21 round 2, N1: freeFilename's check and the write were two steps, so two concurrent
// adds of one slug could both land on slug.md. With O_EXCL the loser steps to the next name. Titles are
// single words so the fuzzy dedup layer does not merge them first.
func TestAddNote_ConcurrentSameSlugNeverShareAFile(t *testing.T) {
	const iterations = 40
	for i := 0; i < iterations; i++ {
		store := setupTestStore(t)
		var wg sync.WaitGroup
		ids := make([]string, 2)
		for w := 0; w < 2; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				r, err := store.AddNote(models.NewNote("infrastructure", "Racing", fmt.Sprintf("BODY-%d", w)))
				if err == nil {
					ids[w] = r.NoteID
				}
			}(w)
		}
		wg.Wait()
		seen := map[string]bool{}
		for w, id := range ids {
			if id == "" {
				continue // a merge or an error is not a loss; a shared file would be
			}
			p := entryPath(t, store, id)
			if seen[p] {
				t.Fatalf("iter %d: two entries share one file %s", i, p)
			}
			seen[p] = true
			body, err := os.ReadFile(filepath.Join(store.baseDir, p))
			if err != nil || !strings.Contains(string(body), id) {
				t.Fatalf("iter %d: worker %d's file does not carry its own id (%v)", i, w, err)
			}
		}
		res, err := store.VerifyIntegrity()
		if err != nil || len(res.SharedFiles) != 0 {
			t.Fatalf("iter %d: verify: err=%v shared=%v", i, err, res.SharedFiles)
		}
		store.Close()
	}
}

// Review round 2, N4: the entries-row half of freeFilename's check. A row that still claims a name
// whose file is gone (hand-deleted, sync-preserved) must keep that name taken — reusing it would
// attach the orphaned row to the new note's file.
func TestFreeFilename_RespectsStaleRowWithMissingFile(t *testing.T) {
	store := setupTestStore(t)
	r, err := store.AddNote(models.NewNote("infrastructure", "Ghost Note Title", "GHOST"))
	if err != nil {
		t.Fatal(err)
	}
	p := entryPath(t, store, r.NoteID)
	if err := os.Remove(filepath.Join(store.baseDir, p)); err != nil {
		t.Fatal(err)
	}
	name, err := store.freeFilename("infrastructure", "ghost-note-title.md", "someone-else")
	if err != nil || name != "ghost-note-title-2.md" {
		t.Fatalf("stale row not respected: name=%s err=%v", name, err)
	}
	name, err = store.freeFilename("infrastructure", "ghost-note-title.md", r.NoteID)
	if err != nil || name != "ghost-note-title.md" {
		t.Fatalf("the row's own note should get its name back: name=%s err=%v", name, err)
	}
}

// Review round 2, N2: something unreadable at the wanted name (a directory) is "taken", not an error.
func TestFreeFilename_StepsAsideFromADirectory(t *testing.T) {
	store := setupTestStore(t)
	dir := filepath.Join(store.notesDir, "infrastructure")
	if err := os.MkdirAll(filepath.Join(dir, "junk-collision-title.md"), 0700); err != nil {
		t.Fatal(err)
	}
	r, err := store.AddNote(models.NewNote("infrastructure", "Junk Collision Title", "BODY"))
	if err != nil {
		t.Fatalf("AddNote next to a directory: %v", err)
	}
	if p := entryPath(t, store, r.NoteID); filepath.Base(p) != "junk-collision-title-2.md" {
		t.Fatalf("expected the -2 name, got %s", p)
	}
}
