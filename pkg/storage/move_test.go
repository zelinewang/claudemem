package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zelinewang/claudemem/pkg/models"
	"github.com/zelinewang/claudemem/pkg/vectors"
)

func TestParseMoveIDs(t *testing.T) {
	ids, err := ParseMoveIDs(strings.NewReader("# notes for the team store\n\naaa-1  # Deploy runbook\n  bbb-2\n#ccc-3\n"))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids) != "[{3 aaa-1} {4 bbb-2}]" {
		t.Errorf("ids = %v", ids)
	}
	_, err = ParseMoveIDs(strings.NewReader("aaa-1\nbbb-2 ccc-3\naaa-1\n"))
	if err == nil || !strings.Contains(err.Error(), "2 invalid lines, nothing moved") ||
		!strings.Contains(err.Error(), "line 2: one full id per line") || !strings.Contains(err.Error(), "line 3: aaa-1 is already on line 1") {
		t.Errorf("bad file: %v", err)
	}
	if ids, err := ParseMoveIDs(strings.NewReader("\n# nothing\n")); err != nil || len(ids) != 0 {
		t.Errorf("an empty file: %v %v", ids, err)
	}
}

// moveStores is two stores with vector tables, as stores with semantic search have.
func moveStores(t *testing.T) (from, to *FileStore) {
	t.Helper()
	from, to = setupTestStore(t), setupTestStore(t)
	for _, store := range []*FileStore{from, to} {
		if _, err := vectors.NewVectorStore(store.db, &storageCountingEmbedder{name: "fake-cloud", model: "test-v1", dim: 4}); err != nil {
			t.Fatal(err)
		}
	}
	return from, to
}

func moveIDs(ids ...string) []MoveID {
	var list []MoveID
	for i, id := range ids {
		list = append(list, MoveID{Line: i + 1, ID: id})
	}
	return list
}

func mustMove(t *testing.T, from, to *FileStore, dryRun bool, ids ...string) *MoveReport {
	t.Helper()
	report, err := MoveEntries(from, to, moveIDs(ids...), dryRun, nil)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// rowsOf is an entry's rows in a store, as text, for comparing two stores.
func rowsOf(t *testing.T, store *FileStore, id string) map[string]string {
	t.Helper()
	rows := map[string]string{}
	var entry, fts string
	if err := store.db.QueryRow(`SELECT type || '|' || title || '|' || category || '|' || branch || '|' || project || '|' || session_id || '|' ||
		date_str || '|' || tags || '|' || filepath || '|' || created || '|' || updated FROM entries WHERE id = ?`, id).Scan(&entry); err == nil {
		rows["entries"] = entry
	}
	if err := store.db.QueryRow(`SELECT title || '|' || content || '|' || tags FROM memory_fts WHERE id = ?`, id).Scan(&fts); err == nil {
		rows["fts"] = fts
	}
	var accesses, vecs []string
	r, err := store.db.Query(`SELECT accessed_at || '|' || access_type FROM access_log WHERE entry_id = ? ORDER BY rowid`, id)
	if err != nil {
		t.Fatal(err)
	}
	for r.Next() {
		var a string
		r.Scan(&a)
		accesses = append(accesses, a)
	}
	r.Close()
	r, err = store.db.Query(`SELECT chunk || '|' || backend || '|' || model || '|' || dim || '|' || hex(vector) || '|' || created_at FROM vectors WHERE doc_id = ? ORDER BY backend, chunk`, id)
	if err != nil {
		t.Fatal(err)
	}
	for r.Next() {
		var v string
		r.Scan(&v)
		vecs = append(vecs, v)
	}
	r.Close()
	if len(accesses) > 0 {
		rows["access_log"] = strings.Join(accesses, ";")
	}
	if len(vecs) > 0 {
		rows["vectors"] = strings.Join(vecs, ";")
	}
	return rows
}

func addVector(t *testing.T, store *FileStore, id, backend string) {
	t.Helper()
	if _, err := store.db.Exec(`INSERT INTO vectors (doc_id, chunk, backend, model, dim, vector, created_at) VALUES (?, 0, ?, ?, 4, ?, ?)`,
		id, backend, backend+"-model", []byte{1, 2, 3, 4}, "2026-01-02T03:04:05Z"); err != nil {
		t.Fatal(err)
	}
}

func entryFile(t *testing.T, store *FileStore, id string) string {
	t.Helper()
	var rel string
	if err := store.db.QueryRow(`SELECT filepath FROM entries WHERE id = ?`, id).Scan(&rel); err != nil {
		t.Fatalf("no index row for %s: %v", id, err)
	}
	return rel
}

func readRelFile(t *testing.T, store *FileStore, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(store.baseDir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A moved entry keeps its id, its file (same name, same bytes) and its index rows, built from the
// file, with the vectors of the backend the target embeds with; TF-IDF vectors and the access log
// stay behind, and this store keeps nothing of it. A second run changes nothing.
func TestMoveEntries_KeepsIDFileAndRows(t *testing.T) {
	from, to := moveStores(t)
	writeStoreSettings(t, to, `{"features.semantic_search": "true", "embedding.backend": "openai", "embedding.dimensions": "4"}`)
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "movekw how to deploy", "team", "ops")
	session := saveTaggedSession(t, from, "Release review", "movekw shipped", "team")
	from.LogAccess(note.ID, "get")
	from.LogAccess(note.ID, "search_hit")
	addVectorRow(t, from, note.ID, "openai", "text-embedding-3-small", 4)
	addVector(t, from, note.ID, "tfidf")
	stay := addTaggedNote(t, from, "life", "Holiday plan", "stays here", "personal")

	noteRel, sessionRel := entryFile(t, from, note.ID), entryFile(t, from, session.ID)
	noteData, sessionData := readRelFile(t, from, noteRel), readRelFile(t, from, sessionRel)
	noteRows, sessionRows := rowsOf(t, from, note.ID), rowsOf(t, from, session.ID)

	report := mustMove(t, from, to, false, session.ID, note.ID)
	if len(report.Moved) != 2 || report.Moved[0].ID != note.ID || report.Moved[1].ID != session.ID ||
		len(report.Missing)+len(report.Refused)+len(report.AlreadyInTarget) != 0 {
		t.Fatalf("report %+v", report)
	}
	if report.VectorsToEmbed != 1 || report.Moved[0].File != noteRel || report.Moved[0].Note != "" {
		t.Errorf("report %+v", report)
	}

	wantNote := map[string]string{}
	for k, v := range noteRows {
		wantNote[k] = v
	}
	if noteRows["access_log"] == "" {
		t.Fatalf("no access log before the move: %v", noteRows)
	}
	// what a person read stays out of the target, and so does the tfidf vector
	delete(wantNote, "access_log")
	wantNote["vectors"] = strings.Split(noteRows["vectors"], ";")[0]
	if !strings.Contains(wantNote["vectors"], "openai") {
		t.Fatalf("vectors before the move: %q", noteRows["vectors"])
	}
	if got := rowsOf(t, to, note.ID); fmt.Sprint(got) != fmt.Sprint(wantNote) {
		t.Errorf("note rows in the target:\n got %v\nwant %v", got, wantNote)
	}
	if got := rowsOf(t, to, session.ID); fmt.Sprint(got) != fmt.Sprint(sessionRows) {
		t.Errorf("session rows in the target:\n got %v\nwant %v", got, sessionRows)
	}
	if string(readRelFile(t, to, noteRel)) != string(noteData) || string(readRelFile(t, to, sessionRel)) != string(sessionData) {
		t.Error("a moved file changed")
	}
	for _, id := range []string{note.ID, session.ID} {
		if rows := rowsOf(t, from, id); len(rows) != 0 {
			t.Errorf("rows of %s left in this store: %v", id, rows)
		}
	}
	for _, rel := range []string{noteRel, sessionRel} {
		if _, err := os.Stat(filepath.Join(from.baseDir, rel)); !os.IsNotExist(err) {
			t.Errorf("%s left in this store: %v", rel, err)
		}
	}
	if got, err := to.GetNote(note.ID); err != nil || got.Content != "movekw how to deploy" {
		t.Errorf("GetNote in the target: %v %v", got, err)
	}
	if got, err := to.GetSession(session.ID); err != nil || got.Title != "Release review" {
		t.Errorf("GetSession in the target: %v %v", got, err)
	}
	if results, _ := to.SearchWithOpts(SearchOpts{Query: "movekw", Tags: []string{"team"}, Limit: 10}); len(results) != 2 {
		t.Errorf("search in the target found %d entries", len(results))
	}
	if _, err := from.GetNote(stay.ID); err != nil {
		t.Errorf("an entry not listed moved: %v", err)
	}
	assertOneFTSRowPerEntry(t, to)

	before := rowsOf(t, to, note.ID) // the search above logged accesses in the target
	again := mustMove(t, from, to, false, session.ID, note.ID)
	if len(again.Moved) != 0 || len(again.AlreadyInTarget) != 2 || len(again.Warnings) != 0 {
		t.Errorf("second run %+v", again)
	}
	if got := rowsOf(t, to, note.ID); fmt.Sprint(got) != fmt.Sprint(before) {
		t.Errorf("the second run changed the target's rows:\n got %v\nwant %v", got, before)
	}
}

func storeSnapshot(t *testing.T, stores ...*FileStore) string {
	t.Helper()
	var lines []string
	for _, store := range stores {
		filepath.Walk(store.baseDir, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(path, ".md") {
				data, _ := os.ReadFile(path)
				lines = append(lines, path+"="+fmt.Sprint(len(data)))
			}
			return nil
		})
		for _, table := range []string{"entries", "memory_fts", "access_log", "vectors"} {
			var n int
			store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
			lines = append(lines, fmt.Sprintf("%s %s=%d", store.baseDir, table, n))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestMoveEntries_DryRunChangesNothing(t *testing.T) {
	from, to := moveStores(t)
	writeStoreSettings(t, to, `{"embedding.backend": "openai", "embedding.dimensions": "4"}`)
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "x", "team")
	session := saveTaggedSession(t, from, "Release review", "y", "team")
	addVectorRow(t, from, note.ID, "openai", "text-embedding-3-small", 4)
	before := storeSnapshot(t, from, to)
	report := mustMove(t, from, to, true, note.ID, session.ID, "no-such-id")
	if !report.DryRun || len(report.Moved) != 2 || len(report.Missing) != 1 || report.VectorsToEmbed != 1 {
		t.Errorf("dry run report %+v", report)
	}
	if after := storeSnapshot(t, from, to); after != before {
		t.Errorf("the dry run changed the stores:\n%s\n---\n%s", before, after)
	}
}

func TestMoveEntries_MissingAndSameStore(t *testing.T) {
	from, to := moveStores(t)
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "x", "team")
	report := mustMove(t, from, to, false, note.ID[:8], "ffffffff-0000-0000-0000-000000000000")
	if len(report.Missing) != 2 || len(report.Moved) != 0 {
		t.Errorf("report %+v", report)
	}
	if _, err := from.GetNote(note.ID); err != nil {
		t.Errorf("a prefix moved the note: %v", err)
	}
	if _, err := MoveEntries(from, from, moveIDs(note.ID), false, nil); err == nil {
		t.Error("a move into the store itself was accepted")
	}
}

// Every state a stopped run can leave is finished by the next run, and two different files under
// one id are refused.
func TestMoveEntries_FinishesAStoppedMove(t *testing.T) {
	type setup func(t *testing.T, from, to *FileStore, id, rel string)
	copyFile := func(t *testing.T, from, to *FileStore, rel string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(to.baseDir, rel)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to.baseDir, rel), readRelFile(t, from, rel), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		setup setup
	}{
		{"the file is in the target, its rows are not", func(t *testing.T, from, to *FileStore, id, rel string) {
			copyFile(t, from, to, rel)
		}},
		{"the target is complete, this store too", func(t *testing.T, from, to *FileStore, id, rel string) {
			copyFile(t, from, to, rel)
			if _, err := to.Reindex(); err != nil {
				t.Fatal(err)
			}
		}},
		{"the target has the rows, not the file", func(t *testing.T, from, to *FileStore, id, rel string) {
			copyFile(t, from, to, rel)
			if _, err := to.Reindex(); err != nil {
				t.Fatal(err)
			}
			os.Remove(filepath.Join(to.baseDir, rel))
		}},
		{"the target is complete, this store has rows without the file", func(t *testing.T, from, to *FileStore, id, rel string) {
			copyFile(t, from, to, rel)
			if _, err := to.Reindex(); err != nil {
				t.Fatal(err)
			}
			os.Remove(filepath.Join(from.baseDir, rel))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			from, to := moveStores(t)
			note := addTaggedNote(t, from, "ops", "Deploy runbook", "stopkw body", "team")
			rel := entryFile(t, from, note.ID)
			data := readRelFile(t, from, rel)
			tc.setup(t, from, to, note.ID, rel)

			report := mustMove(t, from, to, false, note.ID)
			if len(report.Moved) != 1 || report.Moved[0].Note != "finished an earlier move" || len(report.Refused) != 0 {
				t.Fatalf("report %+v", report)
			}
			if string(readRelFile(t, to, rel)) != string(data) {
				t.Error("the target file differs from the moved one")
			}
			if got, err := to.GetNote(note.ID); err != nil || got.Content != "stopkw body" {
				t.Errorf("GetNote in the target: %v %v", got, err)
			}
			if rows := rowsOf(t, from, note.ID); len(rows) != 0 {
				t.Errorf("rows left in this store: %v", rows)
			}
			if _, err := os.Stat(filepath.Join(from.baseDir, rel)); !os.IsNotExist(err) {
				t.Errorf("the file is left in this store: %v", err)
			}
			assertOneFTSRowPerEntry(t, to)
		})
	}

	t.Run("two different files under one id", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Deploy runbook", "original body", "team")
		rel := entryFile(t, from, note.ID)
		changed := strings.Replace(string(readRelFile(t, from, rel)), "original body", "edited elsewhere", 1)
		if err := os.MkdirAll(filepath.Dir(filepath.Join(to.baseDir, rel)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to.baseDir, rel), []byte(changed), 0600); err != nil {
			t.Fatal(err)
		}
		for _, reindexTarget := range []bool{false, true} {
			if reindexTarget {
				if _, err := to.Reindex(); err != nil {
					t.Fatal(err)
				}
			}
			report := mustMove(t, from, to, false, note.ID)
			if len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "different file") {
				t.Errorf("reindexed=%v: report %+v", reindexTarget, report)
			}
			if got, err := from.GetNote(note.ID); err != nil || got.Content != "original body" {
				t.Errorf("the refused note changed here: %v %v", got, err)
			}
			if string(readRelFile(t, to, rel)) != changed {
				t.Error("the target's file changed")
			}
		}
	})
}

func TestMoveEntries_RefusesWhenTheFileIsInNeitherStore(t *testing.T) {
	from, to := moveStores(t)
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "lostkw", "team")
	rel := entryFile(t, from, note.ID)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(to.baseDir, rel)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(to.baseDir, rel), readRelFile(t, from, rel), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := to.Reindex(); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(to.baseDir, rel))
	os.Remove(filepath.Join(from.baseDir, rel))
	report := mustMove(t, from, to, false, note.ID)
	if len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "in neither store") {
		t.Fatalf("report %+v", report)
	}
	if rows := rowsOf(t, from, note.ID); rows["entries"] == "" {
		t.Error("its rows here were removed")
	}
}

// A copy of a moved file that comes back to this store (an add-only sync) is removed by the next run
// when it is the same file; a reindexed copy is moved out again.
func TestMoveEntries_CleansUpACopyThatCameBack(t *testing.T) {
	from, to := moveStores(t)
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "backkw body", "team")
	rel := entryFile(t, from, note.ID)
	data := readRelFile(t, from, rel)
	mustMove(t, from, to, false, note.ID)

	if err := os.WriteFile(filepath.Join(from.baseDir, rel), data, 0600); err != nil {
		t.Fatal(err)
	}
	dry := mustMove(t, from, to, true, note.ID)
	if len(dry.AlreadyInTarget) != 1 || len(dry.Warnings) != 1 || !strings.Contains(dry.Warnings[0], "would be removed") {
		t.Errorf("dry run %+v", dry)
	}
	report := mustMove(t, from, to, false, note.ID)
	if len(report.AlreadyInTarget) != 1 || len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "removed a leftover copy") {
		t.Errorf("report %+v", report)
	}
	if _, err := os.Stat(filepath.Join(from.baseDir, rel)); !os.IsNotExist(err) {
		t.Errorf("the copy is still here: %v", err)
	}

	if err := os.WriteFile(filepath.Join(from.baseDir, rel), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := from.Reindex(); err != nil {
		t.Fatal(err)
	}
	report = mustMove(t, from, to, false, note.ID)
	if len(report.Moved) != 1 || report.Moved[0].Note != "finished an earlier move" {
		t.Errorf("a reindexed copy: %+v", report)
	}
	if _, err := from.GetNote(note.ID); err == nil {
		t.Error("the reindexed copy is still here")
	}

	if err := os.WriteFile(filepath.Join(from.baseDir, rel), []byte(strings.Replace(string(data), "backkw body", "changed", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	report = mustMove(t, from, to, false, note.ID)
	if len(report.NeedsCleanup) != 1 || !strings.Contains(report.NeedsCleanup[0].Reason, "different copy") || len(report.AlreadyInTarget) != 0 {
		t.Errorf("a different copy: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(from.baseDir, rel)); err != nil {
		t.Errorf("a different copy was removed: %v", err)
	}
}

func TestMoveEntries_NameTakenInTarget(t *testing.T) {
	from, to := moveStores(t)
	theirs := addTaggedNote(t, to, "ops", "Deploy runbook", "theirs", "team")
	mine := addTaggedNote(t, from, "ops", "Deploy runbook", "mine", "team")
	rel := entryFile(t, from, mine.ID)
	if entryFile(t, to, theirs.ID) != rel {
		t.Fatalf("the two notes do not share a file name: %s, %s", rel, entryFile(t, to, theirs.ID))
	}
	report := mustMove(t, from, to, false, mine.ID)
	want := strings.TrimSuffix(rel, ".md") + "-2.md"
	if len(report.Moved) != 1 || report.Moved[0].File != want || !strings.Contains(report.Moved[0].Note, "is taken in the target") {
		t.Fatalf("report %+v", report)
	}
	if entryFile(t, to, mine.ID) != want {
		t.Errorf("index row path %s, want %s", entryFile(t, to, mine.ID), want)
	}
	for id, content := range map[string]string{theirs.ID: "theirs", mine.ID: "mine"} {
		if got, err := to.GetNote(id); err != nil || got.Content != content {
			t.Errorf("GetNote(%s) in the target: %v %v", id, got, err)
		}
	}
	if again := mustMove(t, from, to, false, mine.ID); len(again.AlreadyInTarget) != 1 {
		t.Errorf("second run %+v", again)
	}
}

// A session file names its related notes by title, so a session moves only with them.
func TestMoveEntries_SessionsMoveWithTheirRelatedNotes(t *testing.T) {
	newSession := func(t *testing.T, store *FileStore, title string, related ...models.RelatedNote) *models.Session {
		t.Helper()
		s := models.NewSession(title, "main", "proj", "sid-"+strings.ReplaceAll(title, " ", "-"))
		s.Summary = "relkw"
		s.RelatedNotes = related
		if _, err := store.SaveSession(s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	link := func(n *models.Note, id string) models.RelatedNote {
		return models.RelatedNote{ID: id, Title: n.Title, Category: n.Category}
	}

	t.Run("a related note that stays refuses the session", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Private detail", "x", "personal")
		for _, ref := range []string{note.ID, note.ID[:8]} {
			session := newSession(t, from, "Review "+ref[:4], link(note, ref))
			report := mustMove(t, from, to, false, session.ID)
			if len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "stays in this store") {
				t.Fatalf("link %s: report %+v", ref, report)
			}
			if _, err := from.GetSession(session.ID); err != nil {
				t.Errorf("the refused session left: %v", err)
			}
		}
	})

	t.Run("listed together, the note moves first", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Shared detail", "x", "team")
		session := newSession(t, from, "Review", link(note, note.ID[:8]))
		dry := mustMove(t, from, to, true, session.ID, note.ID)
		if len(dry.Moved) != 2 || dry.Moved[0].ID != note.ID || len(dry.Refused) != 0 {
			t.Fatalf("dry run %+v", dry)
		}
		report := mustMove(t, from, to, false, session.ID, note.ID)
		if len(report.Moved) != 2 || report.Moved[0].ID != note.ID || len(report.Refused) != 0 {
			t.Fatalf("report %+v", report)
		}
	})

	t.Run("a related note already in the target, or in neither store", func(t *testing.T) {
		from, to := moveStores(t)
		there := addTaggedNote(t, to, "ops", "Already there", "x", "team")
		gone := &models.Note{ID: "eeeeeeee-0000-0000-0000-000000000000", Title: "Deleted note", Category: "ops"}
		session := newSession(t, from, "Review", link(there, there.ID), link(gone, gone.ID))
		report := mustMove(t, from, to, false, session.ID)
		if len(report.Moved) != 1 || len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "in neither store") {
			t.Fatalf("report %+v", report)
		}
	})

	t.Run("a refused note keeps its session here", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Disputed", "here", "team")
		rel := entryFile(t, from, note.ID)
		if err := os.MkdirAll(filepath.Dir(filepath.Join(to.baseDir, rel)), 0700); err != nil {
			t.Fatal(err)
		}
		other := strings.Replace(string(readRelFile(t, from, rel)), "here", "there", 1)
		if err := os.WriteFile(filepath.Join(to.baseDir, rel), []byte(other), 0600); err != nil {
			t.Fatal(err)
		}
		session := newSession(t, from, "Review", link(note, note.ID))
		report := mustMove(t, from, to, false, note.ID, session.ID)
		if len(report.Refused) != 2 || len(report.Moved) != 0 {
			t.Fatalf("report %+v", report)
		}
	})
}

// Two moves in opposite directions at once neither deadlock nor lose an entry.
func TestMoveEntries_OppositeMovesAtOnce(t *testing.T) {
	a, b := setupTestStore(t), setupTestStore(t)
	var fromA, fromB []string
	for i := 0; i < 15; i++ {
		fromA = append(fromA, addTaggedNote(t, a, fmt.Sprintf("a%02d", i), "Note", "x", "team").ID)
		fromB = append(fromB, addTaggedNote(t, b, fmt.Sprintf("b%02d", i), "Note", "x", "team").ID)
	}
	// each mover opens the stores on its own, as two processes would
	open := func(dir string) *FileStore {
		fs, err := NewFileStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { fs.Close() })
		return fs
	}
	a1, b1, a2, b2 := open(a.baseDir), open(b.baseDir), open(a.baseDir), open(b.baseDir)
	var wg sync.WaitGroup
	reports := make([]*MoveReport, 2)
	errs := make([]error, 2)
	done := make(chan struct{})
	wg.Add(2)
	go func() { defer wg.Done(); reports[0], errs[0] = MoveEntries(a1, b1, moveIDs(fromA...), false, nil) }()
	go func() { defer wg.Done(); reports[1], errs[1] = MoveEntries(b2, a2, moveIDs(fromB...), false, nil) }()
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the two moves did not finish: a deadlock")
	}
	for i, r := range reports {
		if errs[i] != nil || len(r.Moved) != 15 || len(r.Refused) != 0 {
			t.Errorf("mover %d: %v %+v", i, errs[i], r)
		}
	}
	for _, id := range fromA {
		if _, err := b.GetNote(id); err != nil {
			t.Errorf("note %s did not reach b: %v", id, err)
		}
	}
	for _, id := range fromB {
		if _, err := a.GetNote(id); err != nil {
			t.Errorf("note %s did not reach a: %v", id, err)
		}
	}
}
