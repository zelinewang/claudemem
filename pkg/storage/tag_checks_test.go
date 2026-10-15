package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
	"github.com/zelinewang/claudemem/pkg/vectors"
)

// writeLegacyNote writes a note file by hand (as an older claudemem, an editor or a sync could) and
// reindexes, so the store holds a tag that today's write paths refuse.
func writeLegacyNote(t *testing.T, store *FileStore, id, title, tagsYAML string) {
	t.Helper()
	dir := filepath.Join(store.notesDir, "c")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	content := "---\nid: " + id + "\ntype: note\ncategory: c\ntitle: " + title + "\ntags:\n" + tagsYAML +
		"created: \"2026-01-02T03:04:05Z\"\nupdated: \"2026-01-02T03:04:05Z\"\n---\n\nlegacykw body\n"
	if err := os.WriteFile(filepath.Join(dir, Slugify(title)), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Reindex(); err != nil {
		t.Fatal(err)
	}
}

// A new tag is one word: the index joins an entry's tags with spaces, so "machine learning" would
// read back as two tags and match a filter on either.
func TestNewTags_MustBeOneWord(t *testing.T) {
	store := setupTestStore(t)
	for _, tag := range []string{"machine learning", "a,b", "tab\there", "line\nbreak"} {
		note := models.NewNote("c", "Refused", "x")
		note.Tags = []string{"ok", tag}
		if _, err := store.AddNote(note); err == nil || !strings.Contains(err.Error(), "a tag is one word") {
			t.Errorf("AddNote with tag %q: %v", tag, err)
		}
	}
	note := models.NewNote("c", "Refused", "x")
	note.Tags = []string{"machine learning"}
	if _, err := store.AddNote(note); err == nil || !strings.Contains(err.Error(), `"machine-learning"`) {
		t.Errorf("the error should suggest a one-word tag: %v", err)
	}

	session := models.NewSession("Refused session", "main", "proj", "sid-refused")
	session.Tags = []string{"two words"}
	if _, err := store.SaveSession(session); err == nil || !strings.Contains(err.Error(), "a tag is one word") {
		t.Errorf("SaveSession with tag %q: %v", "two words", err)
	}

	kept := addTaggedNote(t, store, "c", "Plain", "x", "ok")
	edited, _ := store.GetNote(kept.ID)
	edited.Tags = append(edited.Tags, "two words")
	if err := store.UpdateNote(edited); err == nil || !strings.Contains(err.Error(), "a tag is one word") {
		t.Errorf("UpdateNote adding %q: %v", "two words", err)
	}
	if _, err := store.EditTags("note", kept.ID, []string{"two words"}, nil, false); err == nil {
		t.Error("EditTags added a tag with a space")
	}
	if got, _ := store.GetNote(kept.ID); !equalStrings(got.Tags, []string{"ok"}) {
		t.Errorf("a refused write changed the tags: %v", got.Tags)
	}
}

// A tag written before the check stays on its entry, and the entry stays editable.
func TestNewTags_OldTagsStayEditable(t *testing.T) {
	store := setupTestStore(t)
	const id = "22222222-0000-0000-0000-000000000001"
	writeLegacyNote(t, store, id, "Old note", "    - machine learning\n")

	note, err := store.GetNote(id)
	if err != nil {
		t.Fatal(err)
	}
	note.Content = "edited"
	if err := store.UpdateNote(note); err != nil {
		t.Fatalf("UpdateNote of a note with an old tag: %v", err)
	}
	again := models.NewNote("c", "Old note", "more")
	again.Tags = []string{"ok"}
	if res, err := store.AddNote(again); err != nil || res.Action != "merged" {
		t.Fatalf("AddNote merging into a note with an old tag: %+v %v", res, err)
	}
	if res, err := store.EditTags("note", id, []string{"team"}, nil, false); err != nil || !res.Changed {
		t.Fatalf("EditTags on a note with an old tag: %+v %v", res, err)
	}
	got, _ := store.GetNote(id)
	if !equalStrings(got.Tags, []string{"machine learning", "ok", "team"}) {
		t.Errorf("tags = %v, want the old tag kept", got.Tags)
	}

	session := models.NewSession("Old session", "main", "proj", "sid-old")
	session.Tags = []string{"old tag"}
	session.Summary = "first"
	path := filepath.Join(store.sessionsDir, "old-session.md")
	if err := os.WriteFile(path, []byte(FormatSessionMarkdown(session)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Reindex(); err != nil {
		t.Fatal(err)
	}
	resave := models.NewSession("Old session", "main", "proj", "sid-old")
	resave.Summary = "second"
	resave.Tags = []string{"ok"}
	res, err := store.SaveSession(resave)
	if err != nil || res.Action != "updated" {
		t.Fatalf("SaveSession merging into a session with an old tag: %+v %v", res, err)
	}
	if merged, err := store.GetSession(res.SessionID); err != nil || !equalStrings(merged.Tags, []string{"ok", "old tag"}) {
		t.Errorf("merged session tags = %v (%v), want the old tag kept", merged.Tags, err)
	}
}

// With --tag, a search hit counts only when its file carries the tags: the index row can be stale,
// and an old tag with a space reads as two words in the index.
func TestTagFilters_SearchChecksTheFile(t *testing.T) {
	store := setupTestStore(t)
	vs, err := vectors.NewVectorStore(store.db, &storageCountingEmbedder{name: "fake-cloud", model: "test-v1", dim: 4})
	if err != nil {
		t.Fatal(err)
	}
	store.vectorStore = vs

	team := addTaggedNote(t, store, "c1", "Team note", "filecheckkw body", "team")
	stale := addTaggedNote(t, store, "c2", "Stale note", "filecheckkw body", "personal")
	writeLegacyNote(t, store, "22222222-0000-0000-0000-000000000002", "Spaced tag", "    - draft team\n")
	// the stale note's file says personal, its index row team (as after a hand edit, before a reindex)
	if _, err := store.db.Exec(`UPDATE entries SET tags = 'team' WHERE id = ?`, stale.ID); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"filecheckkw", "legacykw"} {
		results, err := store.SearchWithOpts(SearchOpts{Query: query, Tags: []string{"team"}, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range results {
			if r.ID != team.ID {
				t.Errorf("search %s --tag team returned %q, whose file lacks the tag", query, r.Title)
			}
		}
	}
	if results, _ := store.SearchWithOpts(SearchOpts{Query: "legacykw", Tags: []string{"draft"}, Limit: 10}); len(results) != 0 {
		t.Errorf("search --tag draft matched a note whose only tag is %q", "draft team")
	}

	results, err := store.HybridSearch("filecheckkw", SearchOpts{Query: "filecheckkw", Tags: []string{"team"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := resultIDs(results); !sameSet(got, []string{team.ID}) {
		t.Errorf("hybrid search --tag team = %v, want only %s", got, team.ID)
	}
}

// A tag edit that finds the file already as requested still brings stale index rows in line with
// the file, so tag apply repairs what search reads.
func TestEditTags_RepairsStaleIndexRows(t *testing.T) {
	store := setupTestStore(t)
	note := addTaggedNote(t, store, "c", "Hand edited", "staleedit body", "personal")
	// the file was relabelled by hand, the index still says team
	if _, err := store.db.Exec(`UPDATE entries SET tags = 'team' WHERE id = ?`, note.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE memory_fts SET tags = 'team' WHERE id = ?`, note.ID); err != nil {
		t.Fatal(err)
	}

	dry := store.ApplyTagEdits([]TagEdit{{Line: 1, ID: note.ID, Kind: "note", Remove: []string{"team"}}}, true, nil)
	if dry.Changed != 0 || dry.Unchanged != 1 || dry.IndexRefreshed != 1 {
		t.Fatalf("dry run report %+v", dry)
	}
	var indexed string
	store.db.QueryRow(`SELECT tags FROM entries WHERE id = ?`, note.ID).Scan(&indexed)
	if indexed != "team" {
		t.Fatalf("the dry run wrote the index: %q", indexed)
	}

	report := store.ApplyTagEdits([]TagEdit{{Line: 1, ID: note.ID, Kind: "note", Remove: []string{"team"}}}, false, nil)
	if report.Changed != 0 || report.Unchanged != 1 || report.IndexRefreshed != 1 {
		t.Fatalf("report %+v", report)
	}
	var ftsTags string
	store.db.QueryRow(`SELECT tags FROM entries WHERE id = ?`, note.ID).Scan(&indexed)
	store.db.QueryRow(`SELECT tags FROM memory_fts WHERE id = ?`, note.ID).Scan(&ftsTags)
	if indexed != "personal" || ftsTags != "personal" {
		t.Errorf("index rows after the repair: entries %q, fts %q", indexed, ftsTags)
	}
	assertOneFTSRowPerEntry(t, store)
	if results, _ := store.SearchWithOpts(SearchOpts{Query: "staleedit", Tags: []string{"personal"}, Limit: 10}); len(results) != 1 {
		t.Errorf("search --tag personal after the repair returned %d results", len(results))
	}

	again := store.ApplyTagEdits([]TagEdit{{Line: 1, ID: note.ID, Kind: "note", Remove: []string{"team"}}}, false, nil)
	if again.IndexRefreshed != 0 || again.Unchanged != 1 {
		t.Errorf("a second run refreshed again: %+v", again)
	}
	res, err := store.EditTags("note", note.ID, nil, []string{"team"}, false)
	if err != nil || res.Changed || res.IndexRefreshed {
		t.Errorf("an edit of an index in line with its file: %+v %v", res, err)
	}
}
