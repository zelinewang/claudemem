package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
)

// Once filenames are stable ids (a title change keeps the name), a slug no longer proves whose
// file it is. Every path that writes or moves a note file must therefore check ownership first;
// these scenarios come from the adversarial review of PR #21 (P0-1..5) and each one destroyed a
// note before freeFilename existed.

func fileHolds(t *testing.T, store *FileStore, id, marker string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(store.baseDir, entryPath(t, store, id)))
	if err != nil {
		t.Fatalf("note %s: file missing: %v", id[:8], err)
	}
	if !strings.Contains(string(body), marker) {
		t.Fatalf("note %s: file no longer holds %q:\n%s", id[:8], marker, string(body))
	}
	got, err := store.GetNote(id)
	if err != nil {
		t.Fatalf("GetNote(%s): %v", id[:8], err)
	}
	if got.ID != id {
		t.Fatalf("GetNote(%s) returned %s", id[:8], got.ID[:8])
	}
}

func distinctPaths(t *testing.T, store *FileStore, ids ...string) {
	t.Helper()
	seen := map[string]string{}
	for _, id := range ids {
		p := entryPath(t, store, id)
		if other, dup := seen[p]; dup {
			t.Fatalf("two entries share one file %s: %s and %s", p, other[:8], id[:8])
		}
		seen[p] = id
	}
}

// P0-1: a new note reusing a renamed note's OLD title must not overwrite the renamed note's file.
func TestAddNote_DoesNotOverwriteRenamedNoteFile(t *testing.T) {
	store := setupTestStore(t)
	orig := models.NewNote("infrastructure", "Alpha Bravo Charlie", "ORIGINAL-CONTENT-MUST-SURVIVE")
	r, err := store.AddNote(orig)
	if err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	n, _ := store.GetNote(r.NoteID)
	n.Title = "Zulu Yankee Xray" // no word overlap: the title-based dedup cannot see the collision coming
	if err := store.UpdateNote(n); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	fresh := models.NewNote("infrastructure", "Alpha Bravo Charlie", "NEW-NOTE-CONTENT")
	fr, err := store.AddNote(fresh)
	if err != nil {
		t.Fatalf("AddNote fresh: %v", err)
	}
	if fr.Action != "created" {
		t.Fatalf("fresh note action = %q, want created", fr.Action)
	}
	fileHolds(t, store, r.NoteID, "ORIGINAL-CONTENT-MUST-SURVIVE")
	fileHolds(t, store, fr.NoteID, "NEW-NOTE-CONTENT")
	distinctPaths(t, store, r.NoteID, fr.NoteID)
	if p := entryPath(t, store, fr.NoteID); !strings.HasSuffix(p, "alpha-bravo-charlie-2.md") {
		t.Fatalf("fresh note should take the -2 name, got %s", p)
	}
}

// P0-2: titles that differ only in punctuation slugify equal; the collision is the same.
func TestAddNote_SlugFlatteningDoesNotOverwrite(t *testing.T) {
	store := setupTestStore(t)
	a := models.NewNote("decisions", "Gateway: rollout plan", "A-CONTENT-MUST-SURVIVE")
	ar, err := store.AddNote(a)
	if err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	n, _ := store.GetNote(ar.NoteID)
	n.Title = "Superseded by the v2 migration writeup"
	if err := store.UpdateNote(n); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	b := models.NewNote("decisions", "Gateway rollout plan", "B-CONTENT")
	br, err := store.AddNote(b)
	if err != nil {
		t.Fatalf("AddNote b: %v", err)
	}
	fileHolds(t, store, ar.NoteID, "A-CONTENT-MUST-SURVIVE")
	fileHolds(t, store, br.NoteID, "B-CONTENT")
	distinctPaths(t, store, ar.NoteID, br.NoteID)
}

// P0-3: a category move whose slug equals a renamed note's stale filename must land elsewhere.
func TestUpdateNote_CategoryMoveOntoStaleFilenameDoesNotOverwrite(t *testing.T) {
	store := setupTestStore(t)
	victim := models.NewNote("decisions", "Quarterly Planning Doc", "VICTIM-MUST-SURVIVE")
	vr, _ := store.AddNote(victim)
	vn, _ := store.GetNote(vr.NoteID)
	vn.Title = "Totally Different Heading Now"
	if err := store.UpdateNote(vn); err != nil {
		t.Fatalf("UpdateNote victim: %v", err)
	}
	mover := models.NewNote("infrastructure", "Quarterly Planning Doc", "MOVER-CONTENT")
	mr, _ := store.AddNote(mover)
	mn, _ := store.GetNote(mr.NoteID)
	mn.Category = "decisions"
	if err := store.UpdateNote(mn); err != nil {
		t.Fatalf("UpdateNote mover: %v", err)
	}
	fileHolds(t, store, vr.NoteID, "VICTIM-MUST-SURVIVE")
	fileHolds(t, store, mr.NoteID, "MOVER-CONTENT")
	distinctPaths(t, store, vr.NoteID, mr.NoteID)
	if p := entryPath(t, store, mr.NoteID); !strings.Contains(p, "/decisions/") {
		t.Fatalf("mover did not move: %s", p)
	}
}

// P0-4 (pre-existing): a category change onto a same-slug note in the target category.
func TestUpdateNote_CategoryChangeDoesNotOverwriteSameSlugNote(t *testing.T) {
	store := setupTestStore(t)
	victim := models.NewNote("decisions", "Shared Title", "VICTIM-CONTENT-DO-NOT-LOSE")
	vr, err := store.AddNote(victim)
	if err != nil {
		t.Fatalf("AddNote victim: %v", err)
	}
	attacker := models.NewNote("infrastructure", "Shared Title", "ATTACKER-CONTENT")
	ar, err := store.AddNote(attacker)
	if err != nil {
		t.Fatalf("AddNote attacker: %v", err)
	}
	n, _ := store.GetNote(ar.NoteID)
	n.Category = "decisions"
	if err := store.UpdateNote(n); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	fileHolds(t, store, vr.NoteID, "VICTIM-CONTENT-DO-NOT-LOSE")
	fileHolds(t, store, ar.NoteID, "ATTACKER-CONTENT")
	distinctPaths(t, store, vr.NoteID, ar.NoteID)
}

// P0-5 (pre-existing): a hand-edited frontmatter category (markdown is the source of truth) followed
// by a plain append used to move the file onto another note's path.
func TestUpdateNote_FrontmatterCategoryMismatchAppendDoesNotOverwrite(t *testing.T) {
	store := setupTestStore(t)
	victim := models.NewNote("decisions", "Recategorized Note Title", "VICTIM-IN-DECISIONS")
	vr, err := store.AddNote(victim)
	if err != nil {
		t.Fatalf("AddNote victim: %v", err)
	}
	other := models.NewNote("infrastructure", "Recategorized Note Title", "OTHER-IN-INFRA")
	or, err := store.AddNote(other)
	if err != nil {
		t.Fatalf("AddNote other: %v", err)
	}
	full := filepath.Join(store.baseDir, entryPath(t, store, or.NoteID))
	raw, _ := os.ReadFile(full)
	edited := strings.Replace(string(raw), "category: infrastructure", "category: decisions", 1)
	if err := os.WriteFile(full, []byte(edited), 0600); err != nil {
		t.Fatalf("hand edit: %v", err)
	}
	if _, err := store.Reindex(); err != nil {
		t.Fatalf("Reindex: %v", err)
	}
	n, _ := store.GetNote(or.NoteID)
	n.Content += "\n\nappended line"
	if err := store.UpdateNote(n); err != nil {
		t.Fatalf("UpdateNote (append path): %v", err)
	}
	fileHolds(t, store, vr.NoteID, "VICTIM-IN-DECISIONS")
	fileHolds(t, store, or.NoteID, "appended line")
	distinctPaths(t, store, vr.NoteID, or.NoteID)
}

// A same-category rename onto another note's slug moves nothing and touches nothing (the stable
// filename is exactly what prevents this collision).
func TestUpdateNote_SameCategoryTitleCollisionTouchesNothing(t *testing.T) {
	store := setupTestStore(t)
	a := models.NewNote("decisions", "Alpha Note One", "ALPHA-CONTENT")
	ar, _ := store.AddNote(a)
	b := models.NewNote("decisions", "Zulu Note Nine", "ZULU-CONTENT")
	br, _ := store.AddNote(b)
	aPath := entryPath(t, store, ar.NoteID)
	bPath := entryPath(t, store, br.NoteID)
	n, _ := store.GetNote(br.NoteID)
	n.Title = "Alpha Note One"
	if err := store.UpdateNote(n); err != nil {
		t.Fatalf("UpdateNote: %v", err)
	}
	fileHolds(t, store, ar.NoteID, "ALPHA-CONTENT")
	fileHolds(t, store, br.NoteID, "ZULU-CONTENT")
	if entryPath(t, store, ar.NoteID) != aPath || entryPath(t, store, br.NoteID) != bPath {
		t.Fatalf("paths moved on a same-category rename")
	}
}

// Review P2-2: verify must report two entries claiming one file instead of "all in sync".
func TestVerifyIntegrity_ReportsSharedFilepath(t *testing.T) {
	store := setupTestStore(t)
	// titles with no word overlap: similar titles would be merged by the dedup layer and leave one entry
	a, err := store.AddNote(models.NewNote("infrastructure", "Gateway Rollout Plan", "A"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.AddNote(models.NewNote("infrastructure", "Quarterly Budget Memo", "B"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Action != "created" || b.Action != "created" {
		t.Fatalf("fixture notes were not both created: %s / %s", a.Action, b.Action)
	}
	res, err := store.VerifyIntegrity()
	if err != nil || !res.InSync || len(res.SharedFiles) != 0 {
		t.Fatalf("clean store should verify in sync: err=%v res=%+v", err, res)
	}
	if _, err := store.db.Exec(`UPDATE entries SET filepath = ? WHERE id = ?`, entryPath(t, store, a.NoteID), b.NoteID); err != nil {
		t.Fatal(err)
	}
	res, err = store.VerifyIntegrity()
	if err != nil {
		t.Fatal(err)
	}
	if res.InSync || len(res.SharedFiles) != 1 || res.SharedFiles[0].Entries != 2 {
		t.Fatalf("shared filepath not reported: %+v", res)
	}
}

// An un-indexed file already on disk under the wanted name (resurrected by add-only sync, or a hand
// copy) counts as taken unless its frontmatter carries the owner's id.
func TestFreeFilename_RespectsUnindexedFileOnDisk(t *testing.T) {
	store := setupTestStore(t)
	dir := filepath.Join(store.notesDir, "infrastructure")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	stray := models.NewNote("infrastructure", "Stray Note", "STRAY-ON-DISK")
	if err := os.WriteFile(filepath.Join(dir, "stray-note.md"), []byte(FormatNoteMarkdown(stray)), 0600); err != nil {
		t.Fatal(err)
	}
	name, err := store.freeFilename("infrastructure", "stray-note.md", "someone-else")
	if err != nil {
		t.Fatalf("freeFilename: %v", err)
	}
	if name != "stray-note-2.md" {
		t.Fatalf("stray file on disk not respected: got %s", name)
	}
	name, err = store.freeFilename("infrastructure", "stray-note.md", stray.ID)
	if err != nil || name != "stray-note.md" {
		t.Fatalf("the owner should get its own file back: name=%s err=%v", name, err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "stray-note.md"))
	if !strings.Contains(string(body), "STRAY-ON-DISK") {
		t.Fatalf("freeFilename must never write")
	}
}
