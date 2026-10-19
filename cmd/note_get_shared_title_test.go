package cmd

import (
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
	"github.com/zelinewang/claudemem/pkg/storage"
)

// Notes of two sessions may share a category and a title. `note get <category> "<title>"` then lists
// both and says to use the note id: the full title, which the caller already typed, picks neither.
func TestNoteGet_SharedTitleSaysToUseTheNoteID(t *testing.T) {
	storePath := setupSessionSaveTest(t, "")
	fs, err := storage.NewFileStore(storePath)
	if err != nil {
		t.Fatalf("NewFileStore() failed: %v", err)
	}
	for _, sid := range []string{"sess-a", "sess-b"} {
		note := models.NewNote("docs", "API Limits", "limits seen in "+sid)
		note.Metadata["session_id"] = sid
		if _, err := fs.AddNote(note); err != nil {
			t.Fatalf("AddNote(%s) failed: %v", sid, err)
		}
	}
	fs.Close()
	origFormat := outputFormat
	outputFormat = "text"
	t.Cleanup(func() { outputFormat = origFormat })

	out := captureStdout(t, func() {
		if err := noteGetCmd.RunE(noteGetCmd, []string{"docs", "API Limits"}); err != nil {
			t.Errorf("note get failed: %v", err)
		}
	})
	if strings.Count(out, "] API Limits") != 2 {
		t.Errorf("note get does not list both notes:\n%s", out)
	}
	if !strings.Contains(out, "use the note ID") {
		t.Errorf("note get does not say to use the note ID:\n%s", out)
	}
}
