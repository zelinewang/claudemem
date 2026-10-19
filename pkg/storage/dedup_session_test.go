package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
)

// ========== Note dedup and session ownership ==========
//
// A note's metadata.session_id names the conversation that wrote it (/wrapup lists a conversation's
// notes by it). The dedup merge in AddNote must not hand one conversation's note to another, so a note
// only merges into an existing note whose session_id is empty or the same as its own.

func addNoteWithSession(t *testing.T, store *FileStore, category, title, content, sessionID string) *AddNoteResult {
	t.Helper()
	note := models.NewNote(category, title, content)
	if sessionID != "" {
		note.Metadata["session_id"] = sessionID
	}
	result, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote(%q, session %q) failed: %v", title, sessionID, err)
	}
	return result
}

func sessionIDOf(t *testing.T, store *FileStore, id string) string {
	t.Helper()
	note, err := store.GetNote(id)
	if err != nil {
		t.Fatalf("GetNote(%s) failed: %v", id, err)
	}
	return note.Metadata["session_id"]
}

func TestNoteDedup_DifferentSessionsKeepSeparateNotes(t *testing.T) {
	store := setupTestStore(t)

	first := addNoteWithSession(t, store, "docs", "API Limits", "100 requests per minute", "sess-a")
	second := addNoteWithSession(t, store, "docs", "API Limits", "200 requests per minute", "sess-b")

	if second.Action != "created" {
		t.Fatalf("same title from another session: Action = %q, want 'created'", second.Action)
	}
	if second.NoteID == first.NoteID {
		t.Fatalf("another session's note merged into %s", first.NoteID)
	}
	if got := sessionIDOf(t, store, first.NoteID); got != "sess-a" {
		t.Errorf("first note's session_id = %q, want 'sess-a' (it must not move)", got)
	}
	if got := sessionIDOf(t, store, second.NoteID); got != "sess-b" {
		t.Errorf("second note's session_id = %q, want 'sess-b'", got)
	}
	first1, err := store.GetNote(first.NoteID)
	if err != nil {
		t.Fatalf("GetNote failed: %v", err)
	}
	if strings.Contains(first1.Content, "200 requests") {
		t.Errorf("first note received the other session's content: %q", first1.Content)
	}

	for ref, want := range map[string]string{"sess-a": first.NoteID, "sess-b": second.NoteID} {
		linked, err := store.FindNotesBySessionRef(ref)
		if err != nil {
			t.Fatalf("FindNotesBySessionRef(%s) failed: %v", ref, err)
		}
		if len(linked) != 1 || linked[0].ID != want {
			t.Errorf("FindNotesBySessionRef(%s) = %+v, want only %s", ref, linked, want)
		}
	}
}

func TestNoteDedup_SimilarTitleFromAnotherSessionStaysSeparate(t *testing.T) {
	store := setupTestStore(t)

	// Titles that differ only by an id: the fuzzy layer sees 50% overlap ("note" shared).
	first := addNoteWithSession(t, store, "e2e", "note from codex-01a1aaaa", "written by one conversation", "codex-01a1aaaa")
	second := addNoteWithSession(t, store, "e2e", "note from grok-01a1cccc", "written by another conversation", "grok-01a1cccc")

	if second.Action != "created" || second.NoteID == first.NoteID {
		t.Fatalf("similar title from another session: Action = %q, id %s (first %s), want a new note",
			second.Action, second.NoteID, first.NoteID)
	}
	if got := sessionIDOf(t, store, first.NoteID); got != "codex-01a1aaaa" {
		t.Errorf("first note's session_id = %q, want 'codex-01a1aaaa'", got)
	}
}

func TestNoteDedup_SameSessionStillMerges(t *testing.T) {
	store := setupTestStore(t)

	first := addNoteWithSession(t, store, "docs", "API Limits", "100 requests per minute", "sess-a")
	second := addNoteWithSession(t, store, "docs", "API Limits", "200 requests per minute", "sess-a")

	if second.Action != "merged" || second.NoteID != first.NoteID {
		t.Fatalf("same session, same title: Action = %q, id %s, want merged into %s", second.Action, second.NoteID, first.NoteID)
	}
	if got := sessionIDOf(t, store, first.NoteID); got != "sess-a" {
		t.Errorf("session_id = %q, want 'sess-a'", got)
	}
}

func TestNoteDedup_UnlinkedAddKeepsTheOwner(t *testing.T) {
	store := setupTestStore(t)

	first := addNoteWithSession(t, store, "docs", "API Limits", "100 requests per minute", "sess-a")
	second := addNoteWithSession(t, store, "docs", "API Limits", "200 requests per minute", "")

	if second.Action != "merged" || second.NoteID != first.NoteID {
		t.Fatalf("unlinked add: Action = %q, id %s, want merged into %s", second.Action, second.NoteID, first.NoteID)
	}
	if got := sessionIDOf(t, store, first.NoteID); got != "sess-a" {
		t.Errorf("session_id = %q, want 'sess-a' (an unlinked add must not clear it)", got)
	}
}

func TestNoteDedup_LinkedAddFillsAnUnlinkedNote(t *testing.T) {
	store := setupTestStore(t)

	first := addNoteWithSession(t, store, "docs", "API Limits", "100 requests per minute", "")
	second := addNoteWithSession(t, store, "docs", "API Limits", "200 requests per minute", "sess-b")

	if second.Action != "merged" || second.NoteID != first.NoteID {
		t.Fatalf("linked add onto an unlinked note: Action = %q, id %s, want merged into %s",
			second.Action, second.NoteID, first.NoteID)
	}
	// An empty owner is filled, as before; only a non-empty one is never replaced.
	if got := sessionIDOf(t, store, first.NoteID); got != "sess-b" {
		t.Errorf("session_id = %q, want 'sess-b'", got)
	}
}

func TestNoteDedup_MergesIntoItsOwnSessionsNote(t *testing.T) {
	store := setupTestStore(t)

	a := addNoteWithSession(t, store, "docs", "API Limits", "written in session a", "sess-a")
	b := addNoteWithSession(t, store, "docs", "API Limits", "written in session b", "sess-b")
	again := addNoteWithSession(t, store, "docs", "API Limits", "more from session b", "sess-b")

	if again.Action != "merged" || again.NoteID != b.NoteID {
		t.Fatalf("third add from sess-b: Action = %q, id %s, want merged into sess-b's note %s (not %s)",
			again.Action, again.NoteID, b.NoteID, a.NoteID)
	}
	noteA, err := store.GetNote(a.NoteID)
	if err != nil {
		t.Fatalf("GetNote failed: %v", err)
	}
	if strings.Contains(noteA.Content, "session b") {
		t.Errorf("sess-a's note received sess-b's content: %q", noteA.Content)
	}
}

// The index can lag behind a note file (a frontmatter edit before the next reindex). The file is the
// source of truth, so a session_id the file carries is never replaced either.
func TestNoteDedup_FileSessionWinsOverStaleIndex(t *testing.T) {
	store := setupTestStore(t)

	first := addNoteWithSession(t, store, "docs", "API Limits", "100 requests per minute", "sess-a")
	if _, err := store.db.Exec(`UPDATE entries SET session_id = '' WHERE id = ?`, first.NoteID); err != nil {
		t.Fatalf("stale the index row: %v", err)
	}

	second := addNoteWithSession(t, store, "docs", "API Limits", "200 requests per minute", "sess-b")

	if second.Action != "created" || second.NoteID == first.NoteID {
		t.Fatalf("stale index: Action = %q, id %s, want a new note (the file says sess-a)", second.Action, second.NoteID)
	}
	var fpath string
	if err := store.db.QueryRow(`SELECT filepath FROM entries WHERE id = ?`, first.NoteID).Scan(&fpath); err != nil {
		t.Fatalf("filepath lookup: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(store.baseDir, fpath))
	if err != nil {
		t.Fatalf("read note file: %v", err)
	}
	if !strings.Contains(string(data), "sess-a") || strings.Contains(string(data), "sess-b") {
		t.Errorf("first note's file lost its owner:\n%s", data)
	}
}

// Notes of two sessions may now share a category and a title. An exact title then names no single
// note: the lookup must not hand back one of them as if it were the only one.
func TestGetNoteByTitle_TitleSharedBySessionsIsNotOneNote(t *testing.T) {
	store := setupTestStore(t)
	addNoteWithSession(t, store, "docs", "API Limits", "100 requests per minute", "sess-a")
	addNoteWithSession(t, store, "docs", "API Limits", "200 requests per minute", "sess-b")

	if note, err := store.GetNoteByTitle("docs", "API Limits"); err == nil {
		t.Fatalf("GetNoteByTitle returned the note of session %q although two notes have this title", note.Metadata["session_id"])
	}

	addNoteWithSession(t, store, "docs", "Deploy Checklist", "only one", "sess-a")
	if note, err := store.GetNoteByTitle("docs", "Deploy Checklist"); err != nil || note.Content != "only one" {
		t.Fatalf("GetNoteByTitle(unique title) = %v, %v", note, err)
	}
}
