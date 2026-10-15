package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
	"github.com/zelinewang/claudemem/pkg/vectors"
)

func addTaggedNote(t *testing.T, store *FileStore, category, title, content string, tags ...string) *models.Note {
	t.Helper()
	note := models.NewNote(category, title, content)
	note.Tags = tags
	if _, err := store.AddNote(note); err != nil {
		t.Fatalf("AddNote(%q): %v", title, err)
	}
	return note
}

func saveTaggedSession(t *testing.T, store *FileStore, title, summary string, tags ...string) *models.Session {
	t.Helper()
	s := models.NewSession(title, "main", "proj", "sid-"+strings.ReplaceAll(title, " ", "-"))
	s.Summary = summary
	s.Tags = tags
	if _, err := store.SaveSession(s); err != nil {
		t.Fatalf("SaveSession(%q): %v", title, err)
	}
	return s
}

func noteIDs(notes []*models.Note) []string {
	var ids []string
	for _, n := range notes {
		ids = append(ids, n.ID)
	}
	return ids
}

func sessionIDs(sessions []*models.Session) []string {
	var ids []string
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

func resultIDs(results []SearchResult) []string {
	var ids []string
	for _, r := range results {
		ids = append(ids, r.ID)
	}
	return ids
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func readStoreFile(t *testing.T, store *FileStore, id string) (string, []byte) {
	t.Helper()
	var rel string
	if err := store.db.QueryRow(`SELECT filepath FROM entries WHERE id = ?`, id).Scan(&rel); err != nil {
		t.Fatalf("filepath of %s: %v", id, err)
	}
	data, err := os.ReadFile(filepath.Join(store.baseDir, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return filepath.Join(store.baseDir, rel), data
}

func TestHasAllTags(t *testing.T) {
	cases := []struct {
		have, required []string
		want           bool
	}{
		{[]string{"a", "b"}, nil, true},
		{[]string{"a", "b"}, []string{"a"}, true},
		{[]string{"a", "b"}, []string{"A", "b"}, true},
		{[]string{"a", "b"}, []string{"a", "c"}, false},
		{[]string{"team-old"}, []string{"team"}, false},
		{nil, []string{"a"}, false},
	}
	for _, c := range cases {
		if got := HasAllTags(c.have, c.required); got != c.want {
			t.Errorf("HasAllTags(%v, %v) = %v, want %v", c.have, c.required, got, c.want)
		}
	}
}

func TestEditTagList(t *testing.T) {
	got := EditTagList([]string{"a", "Personal", "b"}, []string{"team", "A"}, []string{"personal"})
	if want := []string{"a", "b", "team"}; !equalStrings(got, want) {
		t.Errorf("EditTagList = %v, want %v", got, want)
	}
}

// The SQL filter matches whole tags: no substrings, LIKE wildcards escaped, ASCII case ignored.
func TestListNotesWithTags_WholeTagsOnly(t *testing.T) {
	store := setupTestStore(t)
	// one category each: AddNote merges notes whose titles share words within a category
	shared := addTaggedNote(t, store, "c1", "Shared", "x", "team")
	upper := addTaggedNote(t, store, "c2", "Upper", "x", "TEAM", "other")
	addTaggedNote(t, store, "c3", "Old", "x", "team-old")
	addTaggedNote(t, store, "c4", "Prefixed", "x", "xteam")
	underscore := addTaggedNote(t, store, "c5", "Underscore", "x", "a_b")
	addTaggedNote(t, store, "c6", "Lookalike", "x", "axb")
	percent := addTaggedNote(t, store, "c7", "Percent", "x", "100%")
	addTaggedNote(t, store, "c8", "Hundred", "x", "1000")

	for _, c := range []struct {
		required []string
		want     []string
	}{
		{[]string{"team"}, []string{shared.ID, upper.ID}},
		{[]string{"team", "other"}, []string{upper.ID}},
		{[]string{"a_b"}, []string{underscore.ID}},
		{[]string{"100%"}, []string{percent.ID}},
		{[]string{"no-such-tag"}, nil},
	} {
		notes, err := store.ListNotesWithTags("", c.required)
		if err != nil {
			t.Fatalf("ListNotesWithTags(%v): %v", c.required, err)
		}
		if got := noteIDs(notes); !sameSet(got, c.want) {
			t.Errorf("ListNotesWithTags(%v) = %v, want %v", c.required, got, c.want)
		}
	}

	all, err := store.ListNotes("")
	if err != nil || len(all) != 8 {
		t.Errorf("ListNotes without a filter = %d notes (err %v), want all 8", len(all), err)
	}
}

// search --tag used a substring LIKE: --tag team also returned notes tagged team-old.
func TestSearchWithOpts_TagMatchesWholeTags(t *testing.T) {
	store := setupTestStore(t)
	// one category each: AddNote merges notes whose titles share words within a category
	shared := addTaggedNote(t, store, "c1", "Shared", "wholetagkw body", "team")
	addTaggedNote(t, store, "c2", "Stale", "wholetagkw body", "team-old")
	addTaggedNote(t, store, "c3", "Lookalike", "wholetagkw body", "not-team")
	addTaggedNote(t, store, "c4", "Draft", "wholetagkw body", "team-draft")

	results, err := store.SearchWithOpts(SearchOpts{Query: "wholetagkw", Tags: []string{"team"}, Limit: 10})
	if err != nil {
		t.Fatalf("SearchWithOpts: %v", err)
	}
	if got := resultIDs(results); !sameSet(got, []string{shared.ID}) {
		t.Errorf("search --tag team returned %v, want only %s", got, shared.ID)
	}
}

// Semantic hits are filtered by the same facets as keyword hits: the type, and whole tags.
func TestHybridSearch_TypeAndTagFacetsApplyToSemanticHits(t *testing.T) {
	store := setupTestStore(t)
	vs, err := vectors.NewVectorStore(store.db, &storageCountingEmbedder{name: "fake-cloud", model: "test-v1", dim: 4})
	if err != nil {
		t.Fatalf("NewVectorStore: %v", err)
	}
	store.vectorStore = vs // every document is semantically close to every query

	session := saveTaggedSession(t, store, "Deploy review", "hybridfacetkw rollout", "team")
	addTaggedNote(t, store, "c1", "Shared", "unrelated words", "team")
	addTaggedNote(t, store, "c2", "Old", "unrelated words", "team-old")

	results, err := store.HybridSearch("hybridfacetkw", SearchOpts{Query: "hybridfacetkw", Type: "session", Limit: 10})
	if err != nil {
		t.Fatalf("HybridSearch --type session: %v", err)
	}
	for _, r := range results {
		if r.Type != "session" {
			t.Errorf("--type session returned a %s (%s) from the semantic side", r.Type, r.Title)
		}
	}

	results, err = store.HybridSearch("hybridfacetkw", SearchOpts{Query: "hybridfacetkw", Tags: []string{"team"}, Limit: 10})
	if err != nil {
		t.Fatalf("HybridSearch --tag: %v", err)
	}
	for _, r := range results {
		if !HasAllTags(r.Tags, []string{"team"}) {
			t.Errorf("--tag team returned %q tagged %v from the semantic side", r.Title, r.Tags)
		}
	}

	results, err = store.HybridSearch("hybridfacetkw", SearchOpts{Query: "hybridfacetkw", Type: "session", Tags: []string{"team"}, Limit: 10})
	if err != nil {
		t.Fatalf("HybridSearch --type session --tag: %v", err)
	}
	if got := resultIDs(results); !sameSet(got, []string{session.ID}) {
		t.Errorf("--type session --tag team = %v, want only %s", got, session.ID)
	}
}

func TestSessionTags_StoredListedSearched(t *testing.T) {
	store := setupTestStore(t)
	shared := saveTaggedSession(t, store, "Shared session", "sesstagkw shared work", "team", "proj")
	personal := saveTaggedSession(t, store, "Personal session", "sesstagkw private work", "personal")

	// stored: in the file's frontmatter and in the index
	got, err := store.GetSession(shared.ID)
	if err != nil || !equalStrings(got.Tags, []string{"team", "proj"}) {
		t.Fatalf("stored tags = %v (err %v), want [team proj]", got.Tags, err)
	}
	var indexed string
	store.db.QueryRow(`SELECT tags FROM entries WHERE id = ?`, shared.ID).Scan(&indexed)
	if indexed != "team proj" {
		t.Errorf("entries.tags = %q", indexed)
	}

	listed, err := store.ListSessions(SessionListOpts{Tags: []string{"team"}})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if ids := sessionIDs(listed); !sameSet(ids, []string{shared.ID}) {
		t.Errorf("ListSessions --tag team = %v, want [%s]", ids, shared.ID)
	}
	all, _ := store.ListSessions(SessionListOpts{})
	if len(all) != 2 {
		t.Errorf("ListSessions without a filter = %d sessions, want 2", len(all))
	}

	found, err := store.SearchSessions("sesstagkw", SessionListOpts{Tags: []string{"personal"}})
	if err != nil {
		t.Fatalf("SearchSessions: %v", err)
	}
	if ids := sessionIDs(found); !sameSet(ids, []string{personal.ID}) {
		t.Errorf("SearchSessions --tag personal = %v, want [%s]", ids, personal.ID)
	}

	results, err := store.SearchWithOpts(SearchOpts{Query: "sesstagkw", Type: "session", Tags: []string{"team"}})
	if err != nil {
		t.Fatalf("SearchWithOpts: %v", err)
	}
	if ids := resultIDs(results); !sameSet(ids, []string{shared.ID}) {
		t.Errorf("search --type session --tag team = %v, want [%s]", ids, shared.ID)
	}
}

// A get on an entry without the required tags must read exactly like a get on an absent id.
func TestGetWithTags_UntaggedReadsAsAbsent(t *testing.T) {
	store := setupTestStore(t)
	note := addTaggedNote(t, store, "c", "Private note title", "x", "personal")
	session := saveTaggedSession(t, store, "Private session title", "x", "personal")

	if _, err := store.GetNoteWithTags(note.ID, []string{"personal"}); err != nil {
		t.Errorf("tagged note: %v", err)
	}
	_, untagged := store.GetNoteWithTags(note.ID, []string{"team"})
	_, absent := store.GetNote(note.ID)
	if untagged == nil || absent != nil {
		t.Fatalf("untagged = %v, absent lookup of an existing note = %v", untagged, absent)
	}
	_, missing := store.GetNoteWithTags(note.ID, nil)
	if missing != nil {
		t.Fatalf("no filter: %v", missing)
	}
	if want := fmt.Sprintf("note not found: %s", note.ID); untagged.Error() != want {
		t.Errorf("untagged note error = %q, want %q (the absent-id error)", untagged.Error(), want)
	}
	if strings.Contains(untagged.Error(), "Private") {
		t.Errorf("error names the title: %q", untagged.Error())
	}

	_, untaggedSession := store.GetSessionWithTags(session.ID[:8], []string{"team"})
	_, absentSession := store.GetSession("00000000")
	if untaggedSession == nil || absentSession == nil {
		t.Fatalf("untagged session = %v, absent = %v", untaggedSession, absentSession)
	}
	if want := strings.Replace(absentSession.Error(), "00000000", session.ID[:8], 1); untaggedSession.Error() != want {
		t.Errorf("untagged session error = %q, want %q", untaggedSession.Error(), want)
	}
	if got, err := store.GetSessionWithTags(session.ID[:8], []string{"PERSONAL"}); err != nil || got.ID != session.ID {
		t.Errorf("tagged session by prefix: %v (err %v)", got, err)
	}
}

// The file is the source of truth: a stale index row listing a tag the file lacks does not pass.
func TestTagFilters_FileWinsOverStaleIndex(t *testing.T) {
	store := setupTestStore(t)
	note := addTaggedNote(t, store, "c", "Stale index note", "x", "personal")
	session := saveTaggedSession(t, store, "Stale index session", "x", "personal")
	if _, err := store.db.Exec(`UPDATE entries SET tags = 'team'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetNoteWithTags(note.ID, []string{"team"}); err == nil {
		t.Error("GetNoteWithTags returned a note whose file lacks the tag")
	}
	if notes, _ := store.ListNotesWithTags("", []string{"team"}); len(notes) != 0 {
		t.Errorf("ListNotesWithTags returned %d notes whose file lacks the tag", len(notes))
	}
	if _, err := store.GetSessionWithTags(session.ID, []string{"team"}); err == nil {
		t.Error("GetSessionWithTags returned a session whose file lacks the tag")
	}
	if sessions, _ := store.ListSessions(SessionListOpts{Tags: []string{"team"}}); len(sessions) != 0 {
		t.Errorf("ListSessions returned %d sessions whose file lacks the tag", len(sessions))
	}
}

func TestTaggedStats_CountsOnlyTheTaggedSet(t *testing.T) {
	store := setupTestStore(t)
	addTaggedNote(t, store, "work", "Alpha deploy guide", "x", "team", "api")
	addTaggedNote(t, store, "work", "Bravo billing rules", "x", "team")
	addTaggedNote(t, store, "ops", "Charlie backup runbook", "x", "team", "api")
	addTaggedNote(t, store, "life", "Delta holiday plans", "x", "personal", "api")
	saveTaggedSession(t, store, "Shared session", "x", "team")
	saveTaggedSession(t, store, "Personal session", "x", "personal")

	stats, err := store.TaggedStats([]string{"team"})
	if err != nil {
		t.Fatalf("TaggedStats: %v", err)
	}
	if stats.TotalNotes != 3 || stats.TotalSessions != 1 {
		t.Errorf("counts = %d notes, %d sessions; want 3, 1", stats.TotalNotes, stats.TotalSessions)
	}
	if want := []CategoryInfo{{"work", 2}, {"ops", 1}}; fmt.Sprint(stats.Categories) != fmt.Sprint(want) {
		t.Errorf("categories = %v, want %v", stats.Categories, want)
	}
	if want := []TagInfo{{"team", 4}, {"api", 2}}; fmt.Sprint(stats.TopTags) != fmt.Sprint(want) {
		t.Errorf("top tags = %v, want %v", stats.TopTags, want)
	}
}

func TestEditTags_ChangesOnlyTheTags(t *testing.T) {
	store := setupTestStore(t)
	embedder := &storageCountingEmbedder{name: "fake-cloud", model: "test-v1", dim: 4}
	vs, err := vectors.NewVectorStore(store.db, embedder)
	if err != nil {
		t.Fatal(err)
	}
	store.vectorStore = vs

	note := addTaggedNote(t, store, "work", "Deploy checklist", "Step one.\n\nStep two, with  two spaces.\n", "deploy", "personal")
	path, before := readStoreFile(t, store, note.ID)
	var updatedBefore string
	store.db.QueryRow(`SELECT updated FROM entries WHERE id = ?`, note.ID).Scan(&updatedBefore)
	embeddedBefore := embedder.embedded

	res, err := store.EditTags("note", note.ID, []string{"team"}, []string{"PERSONAL"}, false)
	if err != nil {
		t.Fatalf("EditTags: %v", err)
	}
	if !res.Changed || !equalStrings(res.Before, []string{"deploy", "personal"}) || !equalStrings(res.After, []string{"deploy", "team"}) {
		t.Fatalf("result = %+v", res)
	}

	// Byte for byte: only the tags line changed, so the file's `updated` and the body are as they were
	after, _ := os.ReadFile(path)
	if strings.Replace(string(before), "    - personal\n", "    - team\n", 1) != string(after) {
		t.Errorf("only the tags line should change.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	var updatedAfter, indexed string
	store.db.QueryRow(`SELECT updated, tags FROM entries WHERE id = ?`, note.ID).Scan(&updatedAfter, &indexed)
	if updatedAfter != updatedBefore || indexed != "deploy team" {
		t.Errorf("entries row: updated %q -> %q, tags %q", updatedBefore, updatedAfter, indexed)
	}
	if embedder.embedded != embeddedBefore {
		t.Errorf("a tag edit re-embedded the note (%d embeddings)", embedder.embedded-embeddedBefore)
	}
	if results, _ := store.SearchWithOpts(SearchOpts{Query: "checklist", Tags: []string{"team"}}); len(results) != 1 {
		t.Errorf("search by the new tag found %d results", len(results))
	}
	if results, _ := store.SearchWithOpts(SearchOpts{Query: "checklist", Tags: []string{"personal"}}); len(results) != 0 {
		t.Errorf("search by the removed tag found %d results", len(results))
	}

	// Idempotent: the same edit again writes nothing
	stat1, _ := os.Stat(path)
	res, err = store.EditTags("note", note.ID, []string{"team"}, []string{"personal"}, false)
	if err != nil || res.Changed {
		t.Fatalf("second edit: changed=%v err=%v", res != nil && res.Changed, err)
	}
	stat2, _ := os.Stat(path)
	if again, _ := os.ReadFile(path); string(again) != string(after) || !stat1.ModTime().Equal(stat2.ModTime()) {
		t.Error("an edit that changes nothing rewrote the file")
	}
}

// The index rows after an edit are exactly what a reindex builds from the files.
func TestEditTags_IndexMatchesReindex(t *testing.T) {
	store := setupTestStore(t)
	note := addTaggedNote(t, store, "work", "Index parity note", "body text", "a")
	session := saveTaggedSession(t, store, "Index parity session", "summary text", "a")
	if _, err := store.EditTags("note", note.ID, []string{"team"}, nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EditTags("session", session.ID, []string{"team"}, []string{"a"}, false); err != nil {
		t.Fatal(err)
	}

	snapshot := func() string {
		var sb strings.Builder
		rows, err := store.db.Query(`SELECT e.id, e.tags, f.title, f.content, f.tags FROM entries e JOIN memory_fts f ON f.id = e.id ORDER BY e.id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, tags, title, content, ftsTags string
			rows.Scan(&id, &tags, &title, &content, &ftsTags)
			fmt.Fprintf(&sb, "%s|%s|%s|%s|%s\n", id, tags, title, content, ftsTags)
		}
		return sb.String()
	}
	edited := snapshot()
	if _, err := store.Reindex(); err != nil {
		t.Fatal(err)
	}
	if rebuilt := snapshot(); rebuilt != edited {
		t.Errorf("index after EditTags differs from a reindex:\nedited:\n%s\nreindexed:\n%s", edited, rebuilt)
	}
	if results, _ := store.SearchWithOpts(SearchOpts{Query: "parity", Type: "session", Tags: []string{"team"}}); len(results) != 1 {
		t.Errorf("session not found by its new tag after reindex (%d results)", len(results))
	}
}

func TestEditTags_SessionBodyKeptByteForByte(t *testing.T) {
	store := setupTestStore(t)
	s := models.NewSession("Wrapup with custom sections", "main", "proj", "sid-custom")
	s.Summary = "Line one.\n\nLine two."
	s.Decisions = []string{"**Keep** the file"}
	s.ExtraSections = []models.ExtraSection{{Name: "Zeta Notes", Content: "z"}, {Name: "Alpha Notes", Content: "a"}}
	if _, err := store.SaveSession(s); err != nil {
		t.Fatal(err)
	}
	path, before := readStoreFile(t, store, s.ID)
	res, err := store.EditTags("session", s.ID, []string{"team"}, nil, false)
	if err != nil || !res.Changed {
		t.Fatalf("EditTags: %+v %v", res, err)
	}
	after, _ := os.ReadFile(path)
	_, bodyBefore, _, _ := splitFrontmatter(string(before))
	_, bodyAfter, _, _ := splitFrontmatter(string(after))
	if bodyBefore != bodyAfter {
		t.Errorf("session body changed:\nbefore:\n%s\nafter:\n%s", bodyBefore, bodyAfter)
	}
	if strings.Index(string(after), "## Zeta Notes") > strings.Index(string(after), "## Alpha Notes") {
		t.Error("custom section order changed")
	}
	got, err := store.GetSessionWithTags(s.ID, []string{"team"})
	if err != nil || got.Summary != s.Summary {
		t.Errorf("session after edit: %v %v", got, err)
	}
}

func TestEditTags_ErrorsAndDryRun(t *testing.T) {
	store := setupTestStore(t)
	note := addTaggedNote(t, store, "c", "Error cases", "x", "a")
	session := saveTaggedSession(t, store, "Error session", "x")

	if _, err := store.EditTags("note", "no-such-id", []string{"b"}, nil, false); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	if _, err := store.EditTags("note", note.ID[:8], []string{"b"}, nil, false); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("a prefix must not match: %v", err)
	}
	if _, err := store.EditTags("session", note.ID, []string{"b"}, nil, false); !errors.Is(err, ErrWrongKind) {
		t.Errorf("note id as session: %v", err)
	}
	if _, err := store.EditTags("page", note.ID, []string{"b"}, nil, false); err == nil {
		t.Error("unknown kind accepted")
	}
	for _, bad := range [][2][]string{
		{{"two words"}, nil},
		{{"a,b"}, nil},
		{{""}, nil},
		{{"x"}, {"X"}},
		{nil, {" "}},
	} {
		if _, err := store.EditTags("note", note.ID, bad[0], bad[1], false); err == nil {
			t.Errorf("EditTags(add %q, remove %q) accepted", bad[0], bad[1])
		}
	}

	path, before := readStoreFile(t, store, session.ID)
	res, err := store.EditTags("session", session.ID, []string{"team"}, nil, true)
	if err != nil || !res.Changed || !equalStrings(res.After, []string{"team"}) {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Error("dry run wrote the file")
	}
	if sessions, _ := store.ListSessions(SessionListOpts{Tags: []string{"team"}}); len(sessions) != 0 {
		t.Error("dry run changed the index")
	}
}

// Files not written by claudemem keep their extra keys; a list-less or null tags key is handled; a
// tags value that is not a list is refused and the file left alone.
func TestEditTags_HandWrittenFrontmatter(t *testing.T) {
	store := setupTestStore(t)
	files := map[string]string{
		"extra.md":  "---\nid: 11111111-0000-0000-0000-000000000001\ntype: note\ncategory: c\ntitle: Extra keys\nsource: braindump\naliases: [one, two]\ntags: [alpha, 2026]\ncreated: \"2026-01-02T03:04:05Z\"\nupdated: \"2026-01-02T03:04:05Z\"\n---\n\nBody stays.",
		"notags.md": "---\nid: 11111111-0000-0000-0000-000000000002\ntype: note\ncategory: c\ntitle: No tags key\ncreated: \"2026-01-02T03:04:05Z\"\nupdated: \"2026-01-02T03:04:05Z\"\n---\n\nBody.\n",
		"null.md":   "---\nid: 11111111-0000-0000-0000-000000000003\ntype: note\ncategory: c\ntitle: Null tags\ntags:\ncreated: \"2026-01-02T03:04:05Z\"\nupdated: \"2026-01-02T03:04:05Z\"\n---\n\nBody.\n",
		"scalar.md": "---\nid: 11111111-0000-0000-0000-000000000004\ntype: note\ncategory: c\ntitle: Scalar tags\ntags: alpha beta\ncreated: \"2026-01-02T03:04:05Z\"\nupdated: \"2026-01-02T03:04:05Z\"\n---\n\nBody.\n",
	}
	dir := filepath.Join(store.notesDir, "c")
	os.MkdirAll(dir, 0700)
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Reindex(); err != nil {
		t.Fatal(err)
	}

	res, err := store.EditTags("note", "11111111-0000-0000-0000-000000000001", []string{"team"}, []string{"alpha"}, false)
	if err != nil || !equalStrings(res.After, []string{"team"}) {
		t.Fatalf("extra keys: %+v %v", res, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "extra.md"))
	for _, keep := range []string{"source: braindump\n", "aliases: [one, two]\n", "tags: [2026, team]\n", "\n---\n\nBody stays."} {
		if !strings.Contains(string(data), keep) {
			t.Errorf("extra.md lost %q:\n%s", keep, data)
		}
	}
	if strings.HasSuffix(string(data), "\n") {
		t.Error("extra.md gained a final newline it did not have")
	}

	for _, id := range []string{"11111111-0000-0000-0000-000000000002", "11111111-0000-0000-0000-000000000003"} {
		res, err := store.EditTags("note", id, []string{"team"}, nil, false)
		if err != nil || !res.Changed {
			t.Errorf("%s: %+v %v", id, res, err)
		}
		if _, err := store.GetNoteWithTags(id, []string{"team"}); err != nil {
			t.Errorf("%s not found by its new tag: %v", id, err)
		}
	}

	scalarBefore, _ := os.ReadFile(filepath.Join(dir, "scalar.md"))
	if _, err := store.EditTags("note", "11111111-0000-0000-0000-000000000004", []string{"team"}, nil, false); err == nil {
		t.Error("a scalar tags value was rewritten")
	}
	if scalarAfter, _ := os.ReadFile(filepath.Join(dir, "scalar.md")); string(scalarAfter) != string(scalarBefore) {
		t.Error("the refused file was modified")
	}
}

// Files claudemem writes change in their tags lines only, whatever the titles and metadata hold.
func TestRewriteFrontmatterTags_OnlyTagLinesChange(t *testing.T) {
	long := strings.Repeat("a long title word ", 20)
	titles := []string{
		"Plain title", "Colon: inside", "'single' and \"double\" quotes", "中文标题：说明", "true", "1234",
		"  leading spaces", "trailing #hash", "- dash start", long, "multi\nline title",
	}
	stripTags := func(text string) string {
		var out []string
		inTags := false
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "tags:") {
				inTags = true
				continue
			}
			if inTags && strings.HasPrefix(line, "    - ") {
				continue
			}
			inTags = false
			out = append(out, line)
		}
		return strings.Join(out, "\n")
	}
	add := []string{"team", "true", "on", "123"}
	for _, title := range titles {
		note := models.NewNote("cat", title, "content: with colon\n---\nnot frontmatter")
		note.Tags = []string{"a", "b"}
		note.Metadata = map[string]string{"session_id": "sid: odd", "source": "x\ny"}
		session := models.NewSession(title, "feat/x: y", "/path/with spaces", "sid")
		session.Summary = "s"

		noteData := []byte(FormatNoteMarkdown(note))
		sessionData := []byte(FormatSessionMarkdown(session))
		note.Tags = append([]string{"a"}, add...)
		session.Tags = add
		cases := []struct {
			kind       string
			data, want []byte
		}{
			{"note", noteData, []byte(FormatNoteMarkdown(note))},
			{"session", sessionData, []byte(FormatSessionMarkdown(session))},
		}
		for _, c := range cases {
			out, changed, err := rewriteFrontmatterTags(c.data, add, []string{"b"})
			if err != nil || !changed {
				t.Fatalf("%s %q: changed=%v err=%v", c.kind, title, changed, err)
			}
			if stripTags(string(out)) != stripTags(string(c.data)) {
				t.Errorf("%s %q: lines other than tags changed:\nbefore:\n%s\nafter:\n%s", c.kind, title, c.data, out)
			}
			// Exactly the file claudemem itself writes for the new tags
			if string(out) != string(c.want) {
				t.Errorf("%s %q: rewrite differs from FormatMarkdown with the new tags:\ngot:\n%s\nwant:\n%s", c.kind, title, out, c.want)
			}
			if err := verifyTagRewrite(c.data, out); err != nil {
				t.Errorf("%s %q: %v", c.kind, title, err)
			}
		}
	}
}

// Parallel edits of one note all land: the store lock serialises the read-modify-write.
func TestEditTags_ConcurrentEditsAllLand(t *testing.T) {
	store := setupTestStore(t)
	note := addTaggedNote(t, store, "c", "Concurrent tags", "x")
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := store.EditTags("note", note.ID, []string{fmt.Sprintf("t%02d", i)}, nil, false); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	got, err := store.GetNote(note.ID)
	if err != nil || len(got.Tags) != 20 {
		t.Errorf("after 20 parallel edits the note has %d tags (err %v): %v", len(got.Tags), err, got.Tags)
	}
}

func TestParseTagEdits(t *testing.T) {
	good := `{"id": "n1", "kind": "note", "add": ["team"]}

{"id": "s1", "kind": "session", "add": ["team"], "remove": ["personal"]}
{"id": "n2", "kind": "note", "remove": ["personal"]}
`
	edits, err := ParseTagEdits(strings.NewReader(good))
	if err != nil {
		t.Fatalf("ParseTagEdits: %v", err)
	}
	if len(edits) != 3 || edits[1].Line != 3 || edits[1].Kind != "session" || !equalStrings(edits[1].Remove, []string{"personal"}) {
		t.Errorf("edits = %+v", edits)
	}

	bad := []string{
		`{"id": "n1", "kind": "note", "add": ["a"]`,
		`{"id": "n1", "kind": "note", "ad": ["a"]}`,
		`{"kind": "note", "add": ["a"]}`,
		`{"id": "n1 n2", "kind": "note", "add": ["a"]}`,
		`{"id": "n1", "kind": "page", "add": ["a"]}`,
		`{"id": "n1", "kind": "note", "add": ["two words"]}`,
		`{"id": "n1", "kind": "note", "add": ["a"], "remove": ["A"]}`,
		`{"id": "n1", "kind": "note", "add": ["a"]} {"id": "n2"}`,
	}
	for _, line := range bad {
		if _, err := ParseTagEdits(strings.NewReader(line + "\n")); err == nil {
			t.Errorf("accepted %s", line)
		}
	}
	dup := `{"id": "n1", "kind": "note", "add": ["a"]}` + "\n" + `{"id": "n1", "kind": "note", "add": ["b"]}` + "\n"
	if _, err := ParseTagEdits(strings.NewReader(dup)); err == nil || !strings.Contains(err.Error(), "already on line 1") {
		t.Errorf("duplicate entry: %v", err)
	}
	mixed := good + bad[0] + "\n" + bad[4] + "\n"
	if _, err := ParseTagEdits(strings.NewReader(mixed)); err == nil || !strings.HasPrefix(err.Error(), "2 invalid lines, nothing applied") {
		t.Errorf("mixed file: %v", err)
	}
}

func TestApplyTagEdits_DryRunThenApplyIsIdempotent(t *testing.T) {
	store := setupTestStore(t)
	n1 := addTaggedNote(t, store, "work", "Alpha", "bulkkw", "api")
	n2 := addTaggedNote(t, store, "work", "Bravo", "bulkkw", "personal")
	n3 := addTaggedNote(t, store, "work", "Charlie", "bulkkw", "team")
	s1 := saveTaggedSession(t, store, "Bulk session one", "bulkkw", "personal")
	s2 := saveTaggedSession(t, store, "Bulk session two", "bulkkw")

	jsonl := fmt.Sprintf(`{"id": %q, "kind": "note", "add": ["team"]}
{"id": %q, "kind": "note", "add": ["team"], "remove": ["personal"]}
{"id": %q, "kind": "note", "add": ["team"]}
{"id": %q, "kind": "session", "add": ["team"], "remove": ["personal"]}
{"id": %q, "kind": "session", "add": ["team"]}
{"id": "00000000-dead-beef-0000-000000000000", "kind": "note", "add": ["team"]}
{"id": %q, "kind": "session", "add": ["team"]}
`, n1.ID, n2.ID, n3.ID, s1.ID, s2.ID, n1.ID)
	edits, err := ParseTagEdits(strings.NewReader(jsonl))
	if err != nil {
		t.Fatal(err)
	}

	dry := store.ApplyTagEdits(edits, true, nil)
	if dry.Entries != 7 || dry.Changed != 4 || dry.NotesChanged != 2 || dry.SessionsChanged != 2 || dry.Unchanged != 1 || len(dry.Unknown) != 2 || len(dry.Failed) != 0 {
		t.Fatalf("dry run report = %+v", dry)
	}
	if dry.Unknown[0].Line != 6 || dry.Unknown[1].Line != 7 || !strings.Contains(dry.Unknown[1].Reason, "is a note, not a session") {
		t.Errorf("unknown = %+v", dry.Unknown)
	}
	if results, _ := store.SearchWithOpts(SearchOpts{Query: "bulkkw", Tags: []string{"team"}}); len(results) != 1 {
		t.Errorf("dry run changed the index: %d entries tagged", len(results))
	}

	var calls int
	real := store.ApplyTagEdits(edits, false, func(done, total int) { calls++ })
	if real.Changed != 4 || real.Unchanged != 1 || len(real.Unknown) != 2 || calls != 7 {
		t.Fatalf("real run report = %+v (progress calls %d)", real, calls)
	}
	results, _ := store.SearchWithOpts(SearchOpts{Query: "bulkkw", Tags: []string{"team"}})
	if got := resultIDs(results); !sameSet(got, []string{n1.ID, n2.ID, n3.ID, s1.ID, s2.ID}) {
		t.Errorf("tagged after apply = %v", got)
	}
	if results, _ := store.SearchWithOpts(SearchOpts{Query: "bulkkw", Tags: []string{"personal"}}); len(results) != 0 {
		t.Errorf("personal still on %d entries", len(results))
	}

	again := store.ApplyTagEdits(edits, false, nil)
	if again.Changed != 0 || again.Unchanged != 5 || len(again.Unknown) != 2 {
		t.Errorf("second run report = %+v", again)
	}
	assertOneFTSRowPerEntry(t, store)
}

func assertOneFTSRowPerEntry(t *testing.T, store *FileStore) {
	t.Helper()
	var entries, fts, worst int
	store.db.QueryRow(`SELECT COUNT(*) FROM entries`).Scan(&entries)
	store.db.QueryRow(`SELECT COUNT(*) FROM memory_fts`).Scan(&fts)
	store.db.QueryRow(`SELECT COALESCE(MAX(n), 0) FROM (SELECT COUNT(*) AS n FROM memory_fts GROUP BY id)`).Scan(&worst)
	if entries != fts || worst != 1 {
		t.Errorf("%d entries, %d FTS rows, up to %d FTS rows for one id; want one row per entry", entries, fts, worst)
	}
}

// The bulk path deletes FTS rows by a rowid read up front. A rowid that went stale, or that now
// belongs to another entry, must never remove that other entry's row.
func TestEditTags_StaleFTSRowIDHintIsHarmless(t *testing.T) {
	store := setupTestStore(t)
	a := addTaggedNote(t, store, "c1", "Alpha", "alphakw")
	b := addTaggedNote(t, store, "c2", "Bravo", "bravokw")
	hints := store.ftsRowIDs()
	if hints[a.ID] == 0 || hints[b.ID] == 0 || len(hints) != 2 {
		t.Fatalf("ftsRowIDs = %v", hints)
	}

	if _, err := store.editTagsLocking("note", a.ID, []string{"x"}, nil, false, hints[b.ID]); err != nil {
		t.Fatalf("edit with another entry's rowid: %v", err)
	}
	if _, err := store.editTagsLocking("note", b.ID, []string{"x"}, nil, false, 999999); err != nil {
		t.Fatalf("edit with a rowid that does not exist: %v", err)
	}
	if _, err := store.editTagsLocking("note", a.ID, []string{"y"}, nil, false, hints[a.ID]); err != nil {
		t.Fatalf("edit with its own stale rowid: %v", err)
	}
	assertOneFTSRowPerEntry(t, store)
	for kw, id := range map[string]string{"alphakw": a.ID, "bravokw": b.ID} {
		results, _ := store.SearchWithOpts(SearchOpts{Query: kw, Tags: []string{"x"}})
		if got := resultIDs(results); !sameSet(got, []string{id}) {
			t.Errorf("search %s --tag x = %v, want [%s]", kw, got, id)
		}
	}
}

// Near-miss tag names never match, in note search either; and its 50-row cut now comes after the
// tag filter, so a tagged note ranked behind 50 untagged ones is still found.
func TestSearchNotes_WholeTagsBeforeTheLimit(t *testing.T) {
	store := setupTestStore(t)
	for _, tag := range []string{"team-draft", "not-team", "teamx"} {
		addTaggedNote(t, store, "near-"+tag, "Near "+tag, "nearmisskw", tag)
	}
	exact := addTaggedNote(t, store, "exact", "Exact", "nearmisskw", "team")
	notes, err := store.SearchNotes("nearmisskw", "", []string{"team"})
	if err != nil || len(notes) != 1 || notes[0].ID != exact.ID {
		t.Errorf("note search --tag team = %v (err %v), want only the exact tag", noteIDs(notes), err)
	}

	for i := 0; i < 55; i++ {
		addTaggedNote(t, store, "bulk", fmt.Sprintf("Bulk%02d", i), strings.Repeat("limitkw ", 20))
	}
	buried := addTaggedNote(t, store, "bulk", "Buried", "limitkw "+strings.Repeat("filler words to lower the rank ", 40), "team")
	notes, err = store.SearchNotes("limitkw", "", []string{"team"})
	if err != nil || len(notes) != 1 || notes[0].ID != buried.ID {
		t.Errorf("tagged note behind 55 untagged ones: %v (err %v)", noteIDs(notes), err)
	}
}
