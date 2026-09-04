package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

// Review of PR #22, round 1 (P2-1): SaveSession is the same read-modify-write merge as the note
// path (24 parallel `session save --session-id X` lost up to 15 of 25 summaries), and /wrapup is
// exactly this call. Every merged summary must survive.
func TestSaveSession_ConcurrentMergesKeepEverySummary(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	const sid = "SID-concurrent-1"
	if _, err := store.SaveSession(sessionWithSummary(sid, "SUM-base")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	const workers = 24
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []string
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			if _, err := store.SaveSession(sessionWithSummary(sid, fmt.Sprintf("SUM-%03d", w))); err != nil {
				mu.Lock()
				errs = append(errs, err.Error())
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("%d errors, first: %s", len(errs), errs[0])
	}
	rows, err := store.db.Query(`SELECT filepath FROM entries WHERE type = 'session' AND session_id = ?`, sid)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	var paths []string
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			t.Fatalf("scan: %v", err)
		}
		paths = append(paths, fp)
	}
	rows.Close()
	if len(paths) != 1 {
		t.Fatalf("expected exactly one session row for %s, got %d", sid, len(paths))
	}
	body, err := os.ReadFile(filepath.Join(store.baseDir, paths[0]))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	missing := 0
	for w := 0; w < workers; w++ {
		if !strings.Contains(string(body), fmt.Sprintf("SUM-%03d", w)) {
			missing++
		}
	}
	if !strings.Contains(string(body), "SUM-base") {
		missing++
	}
	if missing > 0 {
		t.Fatalf("%d of %d merged summaries are missing after concurrent session saves", missing, workers+1)
	}
}

func sessionWithSummary(sessionID, summary string) *models.Session {
	s := models.NewSession("Concurrent wrapup", "main", "claudemem-test", sessionID)
	s.Summary = summary
	return s
}

// Review of PR #22, round 1 (P2-2 / P2-3): a held lock must not block a writer forever, and a
// re-entry (the lock is not reentrant) must fail loudly instead of hanging. The wait is bounded by
// lockTimeout, shortened here so the test runs in milliseconds.
func TestLockStore_BoundedWaitFailsLoudly(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()
	saved := lockTimeout
	lockTimeout = 150 * time.Millisecond
	defer func() { lockTimeout = saved }()

	unlock, err := store.lockStore()
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	start := time.Now()
	_, err = store.AddNote(models.NewNote("infrastructure", "Blocked Writer", "body"))
	elapsed := time.Since(start)
	unlock()
	if err == nil {
		t.Fatalf("AddNote succeeded while the store lock was held by this process (the lock is not exclusive?)")
	}
	if !strings.Contains(err.Error(), "locked by another claudemem writer") {
		t.Fatalf("unexpected error text: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the bounded wait took %s — the timeout is not bounding it", elapsed)
	}
	// and once released, the same write goes through
	if _, err := store.AddNote(models.NewNote("infrastructure", "Blocked Writer", "body")); err != nil {
		t.Fatalf("AddNote after release: %v", err)
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
