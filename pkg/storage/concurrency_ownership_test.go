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
		errs := make([]error, 2)
		for w := 0; w < 2; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				r, err := store.AddNote(models.NewNote("infrastructure", "Racing", fmt.Sprintf("BODY-%d", w)))
				if err == nil {
					ids[w] = r.NoteID
				}
				errs[w] = err
			}(w)
		}
		wg.Wait()
		for w, err := range errs {
			if err != nil {
				t.Fatalf("iter %d: worker %d's add failed — the retry loop must make both adds succeed (review round 3, R3-2): %v", i, w, err)
			}
		}
		// Both ids equal = the slower worker's dedup check saw the faster one's committed row and MERGED into it
		// (one note, one file) — legitimate, and what serialized adds would do; CI's slower runner reaches it
		// where the Mac rarely does. Two DISTINCT ids on one path is the loss this test exists to catch.
		seen := map[string]string{}
		for w, id := range ids {
			if id == "" {
				t.Fatalf("iter %d: worker %d has no note id", i, w)
			}
			p := entryPath(t, store, id)
			if other, dup := seen[p]; dup && other != id {
				t.Fatalf("iter %d: two entries share one file %s (%s and %s)", i, p, other[:8], id[:8])
			}
			seen[p] = id
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

// Review round 3, R3-1: a note whose OWN file is already on disk without an index row (an import that
// died between the file write and the DB insert; a file the add-only sync brought back) is adopted,
// not refused — freeFilename re-offers a file carrying the note's id, and the create must not insist on
// exclusivity for it.
func TestAddNote_AdoptsItsOwnFileAlreadyOnDisk(t *testing.T) {
	store := setupTestStore(t)
	dir := filepath.Join(store.notesDir, "infrastructure")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	note := models.NewNote("infrastructure", "Reimported Note Title", "CONTENT-FROM-THE-EARLIER-ATTEMPT")
	if err := os.WriteFile(filepath.Join(dir, "reimported-note-title.md"), []byte(FormatNoteMarkdown(note)), 0600); err != nil {
		t.Fatal(err)
	}
	note.Content = "CONTENT-FROM-THIS-ATTEMPT"
	r, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote must adopt its own file, got: %v", err)
	}
	if r.Action != "created" || r.NoteID != note.ID {
		t.Fatalf("unexpected result: %+v", r)
	}
	p := entryPath(t, store, note.ID)
	if filepath.Base(p) != "reimported-note-title.md" {
		t.Fatalf("adopted the wrong name: %s", p)
	}
	body, _ := os.ReadFile(filepath.Join(store.baseDir, p))
	if !strings.Contains(string(body), "CONTENT-FROM-THIS-ATTEMPT") {
		t.Fatalf("file not rewritten with this attempt's content:\n%s", string(body))
	}
	if _, err := os.Stat(filepath.Join(dir, "reimported-note-title-2.md")); err == nil {
		t.Fatalf("a -2 file was created for the note's own name")
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
