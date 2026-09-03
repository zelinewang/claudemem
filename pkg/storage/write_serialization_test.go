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

// Review of 2026-09-03 (evening), item 5: the dedup-merge path in AddNote is read-modify-write with
// no serialisation, so concurrent merges into one note each read the same base and the last writer
// wins — every AddNote returns "merged" and most paragraphs are gone. Every appended paragraph must
// survive.
func TestAddNote_ConcurrentMergesKeepEveryParagraph(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	const title = "Racing Merge Log"
	if _, err := store.AddNote(models.NewNote("infrastructure", title, "PARA-base")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	const workers = 40
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []string
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			res, err := store.AddNote(models.NewNote("infrastructure", title, fmt.Sprintf("PARA-%03d", w)))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err.Error())
			} else if res.Action != "merged" {
				errs = append(errs, fmt.Sprintf("worker %d: action=%s (expected merged)", w, res.Action))
			}
		}(w)
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("%d errors, first: %s", len(errs), errs[0])
	}
	note, err := store.GetNoteByTitle("infrastructure", title)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	missing := 0
	for w := 0; w < workers; w++ {
		if !strings.Contains(note.Content, fmt.Sprintf("PARA-%03d", w)) {
			missing++
		}
	}
	if !strings.Contains(note.Content, "PARA-base") {
		missing++
	}
	if missing > 0 {
		t.Fatalf("%d of %d merged paragraphs are missing from the note after concurrent merges", missing, workers+1)
	}
}

// Same review, item 5b: a category move picks a free filename, then renames the temp file over it
// later — two notes with one slug moving into one category can both be told the same name is free,
// and the second rename overwrites the first note's file (os.Rename replaces). Both notes must keep
// their own file and content.
func TestUpdateNote_ConcurrentCategoryMovesNeverOverwrite(t *testing.T) {
	for iter := 0; iter < 20; iter++ {
		store := setupTestStore(t)
		const n = 6
		ids := make([]string, n)
		for i := 0; i < n; i++ {
			// one title per source category → one slug ("gateway"), six distinct notes
			res, err := store.AddNote(models.NewNote(fmt.Sprintf("src%d", i), "Gateway", fmt.Sprintf("BODY-%d", i)))
			if err != nil {
				t.Fatalf("seed %d: %v", i, err)
			}
			ids[i] = res.NoteID
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		var errs []string
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				note, err := store.GetNote(ids[i])
				if err != nil {
					mu.Lock()
					errs = append(errs, err.Error())
					mu.Unlock()
					return
				}
				note.Category = "dest"
				if err := store.UpdateNote(note); err != nil {
					mu.Lock()
					errs = append(errs, err.Error())
					mu.Unlock()
				}
			}(i)
		}
		wg.Wait()
		if len(errs) > 0 {
			t.Fatalf("iter %d: %d move errors, first: %s", iter, len(errs), errs[0])
		}
		paths := map[string]string{}
		for i := 0; i < n; i++ {
			var fp string
			if err := store.db.QueryRow(`SELECT filepath FROM entries WHERE id = ?`, ids[i]).Scan(&fp); err != nil {
				t.Fatalf("iter %d: row for note %d: %v", iter, i, err)
			}
			if other, dup := paths[fp]; dup {
				t.Fatalf("iter %d: notes %s and %s share the file %s", iter, other, ids[i], fp)
			}
			paths[fp] = ids[i]
			body, err := os.ReadFile(filepath.Join(store.baseDir, fp))
			if err != nil {
				t.Fatalf("iter %d: note %d file %s unreadable: %v", iter, i, fp, err)
			}
			if !strings.Contains(string(body), ids[i]) || !strings.Contains(string(body), fmt.Sprintf("BODY-%d", i)) {
				t.Fatalf("iter %d: file %s does not hold note %d's own id and body (overwritten by another mover)", iter, fp, i)
			}
		}
		store.Close()
	}
}
