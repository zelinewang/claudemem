package storage

import (
	"strings"
	"testing"
	"time"

	"github.com/zelinewang/claudemem/pkg/models"
)

// One store can serve two profiles; vis-shared marks the part the second profile sees. A write must
// never merge across that label, in either direction.

func addNoteResult(t *testing.T, store *FileStore, category, title, content string, tags ...string) *AddNoteResult {
	t.Helper()
	note := models.NewNote(category, title, content)
	note.Tags = tags
	res, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote(%q): %v", title, err)
	}
	return res
}

func TestAddNote_DedupNeverCrossesTheSharedLabel(t *testing.T) {
	store := setupTestStore(t)
	personal := addNoteResult(t, store, "work", "Deploy checklist for staging", "PERSONAL-TEXT", "vis-personal")
	_, personalBefore := readStoreFile(t, store, personal.NoteID)

	// exact title, then a fuzzy one: the fuzzy add may merge into the first shared note, never the personal one
	for _, title := range []string{"Deploy checklist for staging", "Deploy checklist staging steps"} {
		if res := addNoteResult(t, store, "work", title, "SHARED-TEXT "+title, "vis-shared"); res.NoteID == personal.NoteID {
			t.Fatalf("a vis-shared add of %q merged into the personal note", title)
		}
	}
	if _, after := readStoreFile(t, store, personal.NoteID); string(after) != string(personalBefore) {
		t.Errorf("the personal note changed:\nbefore:\n%s\nafter:\n%s", personalBefore, after)
	}
	shared, err := store.ListNotesWithTags("work", []string{"vis-shared"})
	if err != nil || len(shared) != 1 {
		t.Fatalf("want one shared note (the fuzzy add merges into the exact one), got %d (err %v)", len(shared), err)
	}
	if strings.Contains(shared[0].Content, "PERSONAL-TEXT") || !strings.Contains(shared[0].Content, "SHARED-TEXT Deploy checklist staging steps") {
		t.Errorf("shared note content = %q", shared[0].Content)
	}

	// The other direction: personal text never lands in a shared note
	sharedRelease := addNoteResult(t, store, "ops", "Release notes template", "SHARED-BODY", "vis-shared")
	_, sharedBefore := readStoreFile(t, store, sharedRelease.NoteID)
	for _, title := range []string{"Release notes template", "Release notes template draft"} {
		for _, tags := range [][]string{nil, {"vis-personal"}} {
			if res := addNoteResult(t, store, "ops", title, "PRIVATE-BODY", tags...); res.NoteID == sharedRelease.NoteID {
				t.Fatalf("a personal add of %q (tags %v) merged into the shared note", title, tags)
			}
		}
	}
	if _, after := readStoreFile(t, store, sharedRelease.NoteID); string(after) != string(sharedBefore) {
		t.Errorf("the shared note changed:\nbefore:\n%s\nafter:\n%s", sharedBefore, after)
	}

	// Unlabelled and vis-personal are the same side: they still merge
	plain := addNoteResult(t, store, "life", "Weekend plans list", "first")
	if res := addNoteResult(t, store, "life", "Weekend plans list", "second", "vis-personal"); res.Action != "merged" || res.NoteID != plain.NoteID {
		t.Errorf("unlabelled + vis-personal should merge: %+v", res)
	}
	if res := addNoteResult(t, store, "ops", "Release notes template", "MORE-SHARED", "vis-shared"); res.Action != "merged" || res.NoteID != sharedRelease.NoteID {
		t.Errorf("shared + shared should merge: %+v", res)
	}
}

// A stale index row that still says "not shared" must not lead into a merge with a file that is shared.
func TestAddNote_DedupChecksTheFileLabel(t *testing.T) {
	store := setupTestStore(t)
	shared := addNoteResult(t, store, "work", "Index drift note", "SHARED", "vis-shared")
	if _, err := store.db.Exec(`UPDATE entries SET tags = '' WHERE id = ?`, shared.NoteID); err != nil {
		t.Fatal(err)
	}
	if res := addNoteResult(t, store, "work", "Index drift note", "PRIVATE"); res.NoteID == shared.NoteID {
		t.Fatal("merged into a note whose file carries vis-shared")
	}
	note, _ := store.GetNote(shared.NoteID)
	if strings.Contains(note.Content, "PRIVATE") {
		t.Errorf("personal text reached the shared note: %q", note.Content)
	}
}

func saveSessionWithID(t *testing.T, store *FileStore, sessionID, title, summary string, tags ...string) (*SaveSessionResult, error) {
	t.Helper()
	s := models.NewSession(title, "main", "proj", sessionID)
	s.Summary = summary
	s.Tags = tags
	return store.SaveSession(s)
}

func TestSaveSession_MergeNeverCrossesTheSharedLabel(t *testing.T) {
	store := setupTestStore(t)

	personal, err := saveSessionWithID(t, store, "conv-1", "Private planning session", "PERSONAL-SUMMARY")
	if err != nil {
		t.Fatal(err)
	}
	_, personalBefore := readStoreFile(t, store, personal.SessionID)
	_, err = saveSessionWithID(t, store, "conv-1", "Company wrapup", "SHARED-SUMMARY", "vis-shared")
	if err == nil {
		t.Fatal("a vis-shared save merged into a personal session with the same session id")
	}
	if strings.Contains(err.Error(), personal.SessionID) || strings.Contains(err.Error(), personal.SessionID[:8]) || strings.Contains(err.Error(), "Private planning") {
		t.Errorf("the refusal names the other session: %v", err)
	}
	if _, after := readStoreFile(t, store, personal.SessionID); string(after) != string(personalBefore) {
		t.Error("the refused save changed the personal session")
	}

	shared, err := saveSessionWithID(t, store, "conv-2", "Shared release review", "SHARED-SUMMARY", "vis-shared")
	if err != nil {
		t.Fatal(err)
	}
	_, sharedBefore := readStoreFile(t, store, shared.SessionID)
	for _, tags := range [][]string{nil, {"vis-personal"}} {
		if _, err := saveSessionWithID(t, store, "conv-2", "Personal notes", "PRIVATE-SUMMARY", tags...); err == nil {
			t.Fatalf("a personal save (tags %v) merged into a shared session", tags)
		}
	}
	if _, after := readStoreFile(t, store, shared.SessionID); string(after) != string(sharedBefore) {
		t.Error("the refused saves changed the shared session")
	}

	// Same side still merges, as before
	again, err := saveSessionWithID(t, store, "conv-2", "Shared release review", "MORE", "vis-shared")
	if err != nil || again.Action != "updated" || again.SessionID != shared.SessionID {
		t.Errorf("shared + shared: %+v %v", again, err)
	}
	again, err = saveSessionWithID(t, store, "conv-1", "Private planning session", "MORE", "vis-personal")
	if err != nil || again.Action != "updated" || again.SessionID != personal.SessionID {
		t.Errorf("unlabelled + vis-personal: %+v %v", again, err)
	}
}

// Legacy saves without a session id dedup by date+project+branch; the label splits them too.
func TestSaveSession_EmptySessionIDFallbackRespectsTheLabel(t *testing.T) {
	store := setupTestStore(t)
	personal, err := saveSessionWithID(t, store, "", "Legacy personal", "PERSONAL-SUMMARY")
	if err != nil {
		t.Fatal(err)
	}
	_, personalBefore := readStoreFile(t, store, personal.SessionID)

	shared, err := saveSessionWithID(t, store, "", "Legacy shared", "SHARED-SUMMARY", "vis-shared")
	if err != nil || shared.Action != "created" || shared.SessionID == personal.SessionID {
		t.Fatalf("a vis-shared fallback save should become its own session: %+v %v", shared, err)
	}
	if _, after := readStoreFile(t, store, personal.SessionID); string(after) != string(personalBefore) {
		t.Error("the shared save changed the personal session")
	}
	if again, err := saveSessionWithID(t, store, "", "Legacy shared", "MORE-SHARED", "vis-shared"); err != nil || again.SessionID != shared.SessionID {
		t.Errorf("the second shared fallback save should merge into the shared one: %+v %v", again, err)
	}
	if again, err := saveSessionWithID(t, store, "", "Legacy personal", "MORE-PERSONAL"); err != nil || again.SessionID != personal.SessionID {
		t.Errorf("the second personal fallback save should merge into the personal one: %+v %v", again, err)
	}
	got, _ := store.GetSession(personal.SessionID)
	if strings.Contains(got.Summary, "SHARED") {
		t.Errorf("shared text reached the personal session: %q", got.Summary)
	}
}

func TestResolveSessionID_RespectsTheSharedLabel(t *testing.T) {
	store := setupTestStore(t)
	if _, err := saveSessionWithID(t, store, "personal-conv", "Personal work", "x"); err != nil {
		t.Fatal(err)
	}

	id, existing, err := store.ResolveSessionID("proj", 4*time.Hour, nil)
	if err != nil || !existing || id != "personal-conv" {
		t.Errorf("personal resolve = %q existing=%v err=%v, want personal-conv", id, existing, err)
	}
	id, existing, err = store.ResolveSessionID("proj", 4*time.Hour, []string{"vis-shared"})
	if err != nil || existing || id == "personal-conv" {
		t.Errorf("a vis-shared resolve resumed the personal session: %q existing=%v err=%v", id, existing, err)
	}

	time.Sleep(1100 * time.Millisecond) // `updated` has second precision; make the shared session the newer one
	if _, err := saveSessionWithID(t, store, "shared-conv", "Shared work", "x", "vis-shared"); err != nil {
		t.Fatal(err)
	}
	if id, existing, _ := store.ResolveSessionID("proj", 4*time.Hour, []string{"vis-shared"}); !existing || id != "shared-conv" {
		t.Errorf("vis-shared resolve = %q existing=%v, want shared-conv", id, existing)
	}
	if id, existing, _ := store.ResolveSessionID("proj", 4*time.Hour, nil); !existing || id != "personal-conv" {
		t.Errorf("personal resolve after a newer shared session = %q existing=%v, want personal-conv", id, existing)
	}
	if id, existing, _ := store.ResolveSessionID("proj", 4*time.Hour, []string{"vis-shared", "other"}); existing {
		t.Errorf("resolve with a tag no session carries resumed %q", id)
	}
}

func TestLabelWhere(t *testing.T) {
	store := setupTestStore(t)
	shared := addTaggedNote(t, store, "c1", "Shared", "x", "VIS-SHARED")
	addTaggedNote(t, store, "c2", "Draft", "x", "vis-shared-draft")
	addTaggedNote(t, store, "c3", "Plain", "x")
	count := func(tags []string) []string {
		filter, args := labelWhere("tags", tags)
		rows, err := store.db.Query(`SELECT id FROM entries WHERE 1 = 1`+filter, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			rows.Scan(&id)
			ids = append(ids, id)
		}
		return ids
	}
	if got := count([]string{"vis-shared", "x"}); !sameSet(got, []string{shared.ID}) {
		t.Errorf("shared side = %v", got)
	}
	if got := count(nil); len(got) != 2 {
		t.Errorf("not-shared side = %v, want the draft-tagged and the plain note", got)
	}
	if !isShared([]string{"Vis-Shared"}) || isShared([]string{"vis-shared-draft"}) {
		t.Error("isShared must match the whole tag, ignoring case")
	}
}
