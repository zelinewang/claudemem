package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zelinewang/claudemem/pkg/models"
)

// ─── move: links between the stores, stale rows, leftovers, related notes ─────

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
}

func writeRel(t *testing.T, store *FileStore, rel string, data []byte) {
	t.Helper()
	path := filepath.Join(store.baseDir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// A file reached through a symlink between the two stores is never removed: the move is refused, in
// a dry run too, and every copy stays where it was.
func TestMoveEntries_RefusesFilesReachedThroughALinkBetweenTheStores(t *testing.T) {
	t.Run("the target's category is a link to this store's", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Deploy runbook", "linkkw", "team")
		symlinkOrSkip(t, filepath.Join(from.baseDir, "notes", "ops"), filepath.Join(to.baseDir, "notes", "ops"))
		for _, dry := range []bool{true, false} {
			if report := mustMove(t, from, to, dry, note.ID); len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "symlink") {
				t.Errorf("dry run %v: %+v", dry, report)
			}
		}
		if got, err := from.GetNote(note.ID); err != nil || got.Content != "linkkw" {
			t.Errorf("the note is gone: %v %v", got, err)
		}
	})

	t.Run("the target's file is a link to this store's", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Deploy runbook", "linkkw", "team")
		rel := entryFile(t, from, note.ID)
		symlinkOrSkip(t, filepath.Join(from.baseDir, rel), filepath.Join(to.baseDir, rel))
		for _, reindexed := range []bool{false, true} {
			if reindexed {
				if _, err := to.Reindex(); err != nil {
					t.Fatal(err)
				}
			}
			if report := mustMove(t, from, to, false, note.ID); len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "symlink") {
				t.Errorf("target reindexed %v: %+v", reindexed, report)
			}
			if got, err := from.GetNote(note.ID); err != nil || got.Content != "linkkw" {
				t.Fatalf("the note is gone: %v %v", got, err)
			}
		}
	})

	t.Run("this store's category is a link into the target's", func(t *testing.T) {
		from, to := moveStores(t)
		shared := filepath.Join(to.baseDir, "notes", "team")
		if err := os.MkdirAll(shared, 0700); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, shared, filepath.Join(from.baseDir, "notes", "team"))
		note := addTaggedNote(t, from, "team", "Shared runbook", "linkkw", "team") // its file sits in the target
		if report := mustMove(t, from, to, false, note.ID); len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "symlink") {
			t.Errorf("report %+v", report)
		}
		if got, err := from.GetNote(note.ID); err != nil || got.Content != "linkkw" {
			t.Errorf("the note is gone: %v %v", got, err)
		}
	})

	t.Run("the same link, the entry indexed in the target only", func(t *testing.T) {
		from, to := moveStores(t)
		shared := filepath.Join(to.baseDir, "notes", "team")
		if err := os.MkdirAll(shared, 0700); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, shared, filepath.Join(from.baseDir, "notes", "team"))
		theirs := addTaggedNote(t, to, "team", "Their runbook", "linkkw", "team")
		report := mustMove(t, from, to, false, theirs.ID)
		if len(report.AlreadyInTarget) != 1 || len(report.Warnings) != 0 {
			t.Errorf("report %+v", report)
		}
		if got, err := to.GetNote(theirs.ID); err != nil || got.Content != "linkkw" {
			t.Errorf("the target lost its file: %v %v", got, err)
		}
	})
}

// sameResolvedFile decides whether a removal through one path could take the file the other names.
func TestSameResolvedFile(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) string {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(rel), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	note, other := write("a/note.md"), write("b/other.md")
	link := func(name string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.Link(note, path); err != nil {
			t.Skipf("hard links: %v", err)
		}
		return path
	}
	hardElsewhere := link("b/hard.md")
	// One directory reached by two paths is what a bind mount makes, and only root can make one; a
	// second name for the file in its own directory looks the same to the check.
	sameDir := link("a/alias.md")
	symlinkOrSkip(t, filepath.Join(dir, "a"), filepath.Join(dir, "link"))
	for _, c := range []struct {
		name, x, y string
		want       bool
	}{
		{"the same path", note, note, true},
		{"through a symlinked directory", note, filepath.Join(dir, "link", "note.md"), true},
		{"one file in one directory, by two paths", note, sameDir, true},
		{"a hard link in another directory", note, hardElsewhere, false},
		{"two files", note, other, false},
		{"a file that does not exist yet", note, filepath.Join(dir, "b", "none.md"), false},
	} {
		if got := sameResolvedFile(c.x, c.y); got != c.want {
			t.Errorf("%s: sameResolvedFile = %v, want %v", c.name, got, c.want)
		}
	}
}

// The target's rows are built from the moved file, as a reindex builds them: never from rows a stopped
// run left there, nor from this store's rows when the file was edited by hand.
func TestMoveEntries_BuildsTheTargetRowsFromTheFile(t *testing.T) {
	t.Run("rows a stopped move left in the target", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Steps", "old body: step one", "ops")
		rel := entryFile(t, from, note.ID)
		writeRel(t, to, rel, readRelFile(t, from, rel))
		if _, err := to.Reindex(); err != nil {
			t.Fatal(err)
		}
		os.Remove(filepath.Join(to.baseDir, rel)) // the rows reached the target, the file did not
		note.Content, note.Tags = "new body: step two", []string{"ops", "urgent"}
		if err := from.UpdateNote(note); err != nil {
			t.Fatal(err)
		}
		if report := mustMove(t, from, to, false, note.ID); len(report.Moved) != 1 {
			t.Fatalf("report %+v", report)
		}
		for query, want := range map[string]int{"two": 1, "one": 0} {
			if results, _ := to.SearchWithOpts(SearchOpts{Query: query, Limit: 10}); len(results) != want {
				t.Errorf("search %q in the target: %d results, want %d", query, len(results), want)
			}
		}
		if results, _ := to.SearchWithOpts(SearchOpts{Query: "step", Tags: []string{"urgent"}, Limit: 10}); len(results) != 1 {
			t.Errorf("search --tag urgent in the target: %d results", len(results))
		}
	})

	t.Run("a hand edit that was not reindexed", func(t *testing.T) {
		from, to := moveStores(t)
		writeStoreSettings(t, to, `{"embedding.backend": "openai", "embedding.dimensions": "4"}`)
		note := addTaggedNote(t, from, "ops", "Steps", "Public steps. PRIVATEDETAIL my salary is 123", "team")
		addVectorRow(t, from, note.ID, "openai", "text-embedding-3-small", 4) // embedded from the old text
		rel := entryFile(t, from, note.ID)
		writeRel(t, from, rel, []byte(strings.Replace(string(readRelFile(t, from, rel)), " PRIVATEDETAIL my salary is 123", "", 1)))
		report := mustMove(t, from, to, false, note.ID)
		if len(report.Moved) != 1 || report.VectorsToEmbed != 1 {
			t.Fatalf("report %+v", report)
		}
		rows := rowsOf(t, to, note.ID)
		if strings.Contains(rows["fts"]+rows["entries"], "PRIVATEDETAIL") || rows["vectors"] != "" {
			t.Errorf("the target's rows carry what the file no longer says: %v", rows)
		}
		if results, _ := to.SearchWithOpts(SearchOpts{Query: "PRIVATEDETAIL", Limit: 10}); len(results) != 0 {
			t.Errorf("search PRIVATEDETAIL in the target found %d entries", len(results))
		}
	})

	t.Run("rows the target built itself", func(t *testing.T) {
		from, to := moveStores(t)
		writeStoreSettings(t, to, `{"embedding.backend": "openai", "embedding.dimensions": "4"}`)
		note := addTaggedNote(t, from, "ops", "Steps", "shared body", "team")
		addVectorRow(t, from, note.ID, "openai", "text-embedding-3-small", 4)
		from.LogAccess(note.ID, "get")
		rel := entryFile(t, from, note.ID)
		writeRel(t, to, rel, readRelFile(t, from, rel))
		if _, err := to.Reindex(); err != nil { // the file reached the target, which indexed it
			t.Fatal(err)
		}
		addVectorRow(t, to, note.ID, "openai", "text-embedding-3-small", 4) // and embedded it, perhaps from older text
		report := mustMove(t, from, to, false, note.ID)
		if len(report.Moved) != 1 || report.Moved[0].Note != "finished an earlier move" || report.VectorsToEmbed != 1 {
			t.Fatalf("report %+v", report)
		}
		if rows := rowsOf(t, to, note.ID); rows["vectors"] != "" || rows["access_log"] != "" || rows["fts"] == "" {
			t.Errorf("target rows %v, want the search row, no vectors and no access log", rows)
		}
		if rows := rowsOf(t, from, note.ID); len(rows) != 0 {
			t.Errorf("rows left here: %v", rows)
		}
	})

	t.Run("a reindex of the target builds the same rows", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Steps", "reindexkw body", "team", "ops")
		session := saveTaggedSession(t, from, "Review", "reindexkw summary", "team")
		mustMove(t, from, to, false, note.ID, session.ID)
		moved := []map[string]string{rowsOf(t, to, note.ID), rowsOf(t, to, session.ID)}
		if _, err := to.Reindex(); err != nil {
			t.Fatal(err)
		}
		for i, id := range []string{note.ID, session.ID} {
			rows := rowsOf(t, to, id)
			if rows["entries"] != moved[i]["entries"] || rows["fts"] != moved[i]["fts"] {
				t.Errorf("%s: the move wrote %v, a reindex builds %v", id, moved[i], rows)
			}
		}
	})
}

// A row or a file in the target is checked before the move relies on it.
func TestMoveEntries_ChecksTheTargetsRowsAndFiles(t *testing.T) {
	t.Run("a stale row naming a place outside the notes", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Stray", "straykw", "team")
		if _, err := to.db.Exec(`INSERT INTO entries (id, type, title, filepath, created, updated) VALUES (?, 'session', 'x', 'notes/../stray.md', '1', '1')`, note.ID); err != nil {
			t.Fatal(err)
		}
		if report := mustMove(t, from, to, false, note.ID); len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "stale row") {
			t.Errorf("report %+v", report)
		}
		if exists(filepath.Join(to.baseDir, "stray.md")) {
			t.Error("a file was written outside the target's notes")
		}
		if _, err := from.GetNote(note.ID); err != nil {
			t.Errorf("the note left: %v", err)
		}
	})

	t.Run("the target's index lists it, its file is only here outside the index", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Deploy runbook", "lostkw body", "team")
		rel := entryFile(t, from, note.ID)
		data := readRelFile(t, from, rel)
		mustMove(t, from, to, false, note.ID)
		os.Remove(filepath.Join(to.baseDir, rel))
		writeRel(t, from, rel, data)
		report := mustMove(t, from, to, false, note.ID)
		if len(report.Moved) != 1 || !strings.Contains(report.Moved[0].Note, "missing in the target") {
			t.Fatalf("report %+v", report)
		}
		if got, err := to.GetNote(note.ID); err != nil || got.Content != "lostkw body" {
			t.Errorf("GetNote in the target: %v %v", got, err)
		}
		if exists(filepath.Join(from.baseDir, rel)) {
			t.Error("the copy here was not removed")
		}
	})

	t.Run("the target's row names another entry's file", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Mine", "mine", "team")
		theirs := addTaggedNote(t, to, "ops", "Theirs", "theirs", "team")
		if _, err := to.db.Exec(`INSERT INTO entries (id, type, title, category, filepath, created, updated) VALUES (?, 'note', 'Mine', 'ops', ?, '1', '1')`,
			note.ID, entryFile(t, to, theirs.ID)); err != nil {
			t.Fatal(err)
		}
		if report := mustMove(t, from, to, false, note.ID); len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "another entry's file; reindex the target") {
			t.Errorf("report %+v", report)
		}
		os.Remove(filepath.Join(from.baseDir, entryFile(t, from, note.ID))) // as if an earlier run had moved the file
		if report := mustMove(t, from, to, false, note.ID); len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "another entry's file") {
			t.Errorf("without the file here: %+v", report)
		}
		if rows := rowsOf(t, from, note.ID); rows["entries"] == "" {
			t.Error("its rows here were removed")
		}
		if got, err := to.GetNote(theirs.ID); err != nil || got.Content != "theirs" {
			t.Errorf("the other entry changed: %v %v", got, err)
		}
	})

	t.Run("a different copy of the id elsewhere in the target", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Deploy runbook", "body here", "team")
		rel := entryFile(t, from, note.ID)
		writeRel(t, to, "notes/archive/old-name.md", []byte(strings.Replace(string(readRelFile(t, from, rel)), "body here", "other body", 1)))
		report := mustMove(t, from, to, false, note.ID)
		if len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "different file for this id at notes/archive/old-name.md") {
			t.Errorf("report %+v", report)
		}
		if exists(filepath.Join(to.baseDir, rel)) {
			t.Error("the file was written to the target")
		}
	})

	t.Run("a row here naming another entry's file", func(t *testing.T) {
		from, to := moveStores(t)
		a := addTaggedNote(t, from, "ops", "Alpha", "a", "team")
		b := addTaggedNote(t, from, "ops", "Bravo", "b", "team")
		if _, err := from.db.Exec(`UPDATE entries SET filepath = ? WHERE id = ?`, entryFile(t, from, b.ID), a.ID); err != nil {
			t.Fatal(err)
		}
		if report := mustMove(t, from, to, false, a.ID); len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "holds another id ("+b.ID+")") {
			t.Errorf("report %+v", report)
		}
	})
}

// A copy of a moved file left anywhere in this store is found, not only at the target's file name.
func TestMoveEntries_FindsLeftoverCopiesAnywhere(t *testing.T) {
	from, to := moveStores(t)
	addTaggedNote(t, to, "ops", "Deploy runbook", "theirs", "team")
	mine := addTaggedNote(t, from, "ops", "Deploy runbook", "mine", "team")
	rel := entryFile(t, from, mine.ID)
	data := readRelFile(t, from, rel)
	if report := mustMove(t, from, to, false, mine.ID); len(report.Moved) != 1 || report.Moved[0].File == rel {
		t.Fatalf("report %+v", report)
	}
	writeRel(t, from, rel, data) // a sync brings the file back under its old name
	report := mustMove(t, from, to, false, mine.ID)
	if len(report.AlreadyInTarget) != 1 || len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "removed a leftover copy") {
		t.Errorf("report %+v", report)
	}
	if exists(filepath.Join(from.baseDir, rel)) {
		t.Error("the copy is still here")
	}
}

// A leftover copy that cannot be read, or no longer parses, is reported, not skipped as if it were gone.
func TestMoveEntries_ReportsALeftoverCopyItCannotCheck(t *testing.T) {
	for _, problem := range []string{"cannot be read", "no longer parses"} {
		t.Run(problem, func(t *testing.T) {
			if problem == "cannot be read" && (runtime.GOOS == "windows" || os.Geteuid() == 0) {
				t.Skip("needs a file its permissions keep from being read")
			}
			from, to := moveStores(t)
			note := addTaggedNote(t, from, "ops", "Deploy runbook", "body", "team")
			rel := entryFile(t, from, note.ID)
			data := readRelFile(t, from, rel)
			if report := mustMove(t, from, to, false, note.ID); len(report.Moved) != 1 {
				t.Fatalf("report %+v", report)
			}
			path := filepath.Join(from.baseDir, rel)
			writeRel(t, from, rel, data) // a sync brings the file back
			if problem == "cannot be read" {
				atStep(t, "after-walk", func() { // the walk read it; then its permissions change
					if err := os.Chmod(path, 0); err != nil {
						t.Fatal(err)
					}
				})
				t.Cleanup(func() { os.Chmod(path, 0600) })
			} else {
				broken := strings.Replace(string(data), "\n---\n", "\n", 1) // drop the frontmatter's closing line
				writeRel(t, from, rel, []byte(broken))
			}
			report := mustMove(t, from, to, false, note.ID)
			if len(report.NeedsCleanup) != 1 || !strings.Contains(report.NeedsCleanup[0].Reason, problem) {
				t.Errorf("report %+v, want it left to clean up", report)
			}
			if !exists(path) {
				t.Error("the copy here was removed")
			}
		})
	}
}

// A file or a directory under notes/ or sessions/ that the walk cannot read is reported: it may hold a
// copy of a moved entry, which a reindex would bring back once it can be read.
func TestMoveEntries_ReportsFilesTheWalkCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs permissions that keep a file from being read")
	}
	for _, what := range []string{"a file", "a directory"} {
		t.Run(what, func(t *testing.T) {
			from, to := moveStores(t)
			note := addTaggedNote(t, from, "ops", "Deploy runbook", "body", "team")
			rel := entryFile(t, from, note.ID)
			data := readRelFile(t, from, rel)
			if report := mustMove(t, from, to, false, note.ID); len(report.Moved) != 1 {
				t.Fatalf("report %+v", report)
			}
			blocked := filepath.Join("notes", "archive", filepath.Base(rel))
			writeRel(t, from, blocked, data) // a copy comes back, somewhere the walk cannot read
			if what == "a directory" {
				blocked = filepath.Dir(blocked)
			}
			path := filepath.Join(from.baseDir, blocked)
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(path, 0700) })
			for _, dryRun := range []bool{true, false} {
				report := mustMove(t, from, to, dryRun, note.ID)
				if warnings := strings.Join(report.Warnings, "\n"); !strings.Contains(warnings, blocked+" cannot be read") {
					t.Errorf("dry run %v: no warning about %s: %q", dryRun, blocked, warnings)
				}
			}
		})
	}
}

// What a stopped run leaves is recognized: a temp file is removed, and a file in the target without
// rows is used when this store's rows have lost their file.
func TestMoveEntries_FinishesWhatAStoppedRunLeft(t *testing.T) {
	t.Run("a temp file of a stopped write", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Deploy runbook", "tempkw", "team")
		rel := entryFile(t, from, note.ID)
		temp := filepath.Join(filepath.Dir(rel), ".deploy-runbook.123456.tmp")
		writeRel(t, to, temp, readRelFile(t, from, rel))
		if report := mustMove(t, from, to, false, note.ID); len(report.Moved) != 1 {
			t.Fatalf("report %+v", report)
		}
		if exists(filepath.Join(to.baseDir, temp)) {
			t.Error("the temp file is still there")
		}
	})

	t.Run("a temp of this entry with other content is kept", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Deploy runbook", "old body", "team")
		rel := entryFile(t, from, note.ID)
		// a note update that failed to put its file in place names this temp as the new text's only copy
		newer := strings.Replace(string(readRelFile(t, from, rel)), "old body", "newer body only in the temp", 1)
		temp := filepath.Join(filepath.Dir(rel), ".deploy-runbook.987654.tmp")
		writeRel(t, to, temp, []byte(newer))
		if report := mustMove(t, from, to, false, note.ID); len(report.Moved) != 1 {
			t.Fatalf("report %+v", report)
		}
		if !exists(filepath.Join(to.baseDir, temp)) {
			t.Error("the temp with other content was removed")
		}
	})

	t.Run("the target's file without rows, the rows here without the file", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Deploy runbook", "lostrowskw", "team")
		rel := entryFile(t, from, note.ID)
		writeRel(t, to, rel, readRelFile(t, from, rel))
		os.Remove(filepath.Join(from.baseDir, rel))
		report := mustMove(t, from, to, false, note.ID)
		if len(report.Moved) != 1 || report.Moved[0].Note != "finished an earlier move" {
			t.Fatalf("report %+v", report)
		}
		if got, err := to.GetNote(note.ID); err != nil || got.Content != "lostrowskw" {
			t.Errorf("GetNote in the target: %v %v", got, err)
		}
		if rows := rowsOf(t, from, note.ID); len(rows) != 0 {
			t.Errorf("rows left here: %v", rows)
		}
	})
}

// The related notes of a session are read from its file: every Related Notes section, every line.
func TestMoveEntries_ReadsRelatedNotesFromTheFile(t *testing.T) {
	setRelated := func(t *testing.T, store *FileStore, session *models.Session, section string) {
		t.Helper()
		rel := entryFile(t, store, session.ID)
		writeRel(t, store, rel, []byte(string(readRelFile(t, store, rel))+"\n\n"+section+"\n"))
	}
	withID := func(t *testing.T, store *FileStore, id, category, title string) *models.Note {
		t.Helper()
		n := models.NewNote(category, title, "x")
		n.ID = id
		if _, err := store.AddNote(n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, tc := range []struct {
		name, want string
		section    func(private, shared *models.Note) string
	}{
		{"a line without an id", "names no note", func(private, shared *models.Note) string {
			return "## Related Notes\n- Private detail (ops)"
		}},
		{"a second Related Notes section", "stays in this store", func(private, shared *models.Note) string {
			return "## Related Notes\n- `" + private.ID + "` — \"Private detail\" (ops)\n\n## Related Notes\n- `" + shared.ID + "` — \"Shared\" (ops)"
		}},
		{"a ref too short", "too short", func(private, shared *models.Note) string {
			return "## Related Notes\n- `" + shared.ID[:4] + "` — \"Shared\" (ops)"
		}},
		{"a ref that names two notes", "matches several notes", func(private, shared *models.Note) string {
			return "## Related Notes\n- `abcdef12` — \"Twins\" (ops)"
		}},
		// a hand edit can leave the notes under a heading the session parser no longer reads
		{"a heading with a trailing space", "stays in this store", func(private, shared *models.Note) string {
			return "## Related Notes \n- `" + private.ID + "` — \"Private detail\" (ops)"
		}},
		{"a heading with a colon", "stays in this store", func(private, shared *models.Note) string {
			return "## Related notes:\n- `" + private.ID + "` — \"Private detail\" (ops)"
		}},
		{"a heading of another level", "stays in this store", func(private, shared *models.Note) string {
			return "### Related Notes\n- `" + private.ID + "` — \"Private detail\" (ops)"
		}},
		{"a heading indented and closed", "stays in this store", func(private, shared *models.Note) string {
			return "  #  related   NOTES  #\n- `" + private.ID + "` — \"Private detail\" (ops)"
		}},
		{"a deeper heading inside the section", "names no note", func(private, shared *models.Note) string {
			return "## Related Notes\n- `" + shared.ID + "` — \"Shared\" (ops)\n### Private\n- `" + private.ID + "` — \"Private detail\" (ops)"
		}},
		{"a ref in backticks that is no id", "is no note id", func(private, shared *models.Note) string {
			return "## Related Notes\n- `Private detail notes` — \"Private detail\" (ops)"
		}},
		{"a second id on a line", "stays in this store", func(private, shared *models.Note) string {
			return "## Related Notes\n- `" + shared.ID + "` — \"Shared\" (ops), `" + private.ID + "` — \"Private detail\" (ops)"
		}},
		{"a nested item", "stays in this store", func(private, shared *models.Note) string {
			return "## Related Notes\n- `" + shared.ID + "` — \"Shared\" (ops)\n  - `" + private.ID + "` — \"Private detail\" (ops)"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			from, to := moveStores(t)
			private := addTaggedNote(t, from, "ops", "Private detail", "x", "personal")
			shared := addTaggedNote(t, from, "ops", "Shared", "x", "team")
			withID(t, from, "abcdef12-0000-0000-0000-000000000001", "twins-a", "Twin one")
			withID(t, from, "abcdef12-0000-0000-0000-000000000002", "twins-b", "Twin two")
			session := saveTaggedSession(t, from, "Review", "relkw", "team")
			setRelated(t, from, session, tc.section(private, shared))
			report := mustMove(t, from, to, false, shared.ID, "abcdef12-0000-0000-0000-000000000001", session.ID)
			if len(report.Refused) != 1 || report.Refused[0].ID != session.ID || !strings.Contains(report.Refused[0].Reason, tc.want) {
				t.Errorf("report %+v", report)
			}
			if _, err := from.GetSession(session.ID); err != nil {
				t.Errorf("the session left: %v", err)
			}
		})
	}
}

// Every note id in a session file counts, wherever it is: a hand edit can name a note under a heading
// the section check does not read (bold, setext, HTML) or without backticks. A full lowercase id, or a
// hex prefix of 8 or more characters in backticks, that names a note staying here keeps the session;
// one that names no note does not.
func TestMoveEntries_ChecksEveryNoteIDInASessionFile(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stays bool
		edit  func(data string, private, shared *models.Note) string
	}{
		{"a bold heading", true, func(data string, private, shared *models.Note) string {
			return data + "\n## **Related Notes**\n- `" + private.ID + "` — \"Private detail\" (ops)\n"
		}},
		{"a setext heading", true, func(data string, private, shared *models.Note) string {
			return data + "\n## Notes for later\nsome text\n\nRelated Notes\n-------------\n- `" + private.ID + "` — \"Private detail\" (ops)\n"
		}},
		{"an HTML heading", true, func(data string, private, shared *models.Note) string {
			return data + "\n## Notes for later\nsome text\n\n<h2>Related Notes</h2>\n\n- `" + private.ID + "` — \"Private detail\" (ops)\n"
		}},
		{"a second id without backticks", true, func(data string, private, shared *models.Note) string {
			line := "- `" + shared.ID + "` — \"Shared\" (ops)"
			return strings.Replace(data, line, line+"; see also "+private.ID+" \"Private detail\"", 1)
		}},
		{"an id in the text", true, func(data string, private, shared *models.Note) string {
			return data + "\n## Notes\nwe went through " + private.ID + " today\n"
		}},
		{"a prefix in backticks in the text", true, func(data string, private, shared *models.Note) string {
			return data + "\n## Notes\nsee `" + private.ID[:8] + "` for the details\n"
		}},
		{"an id that names no note", false, func(data string, private, shared *models.Note) string {
			return data + "\n## Notes\nthe run 123e4567-e89b-42d3-a456-426614174000 failed\n"
		}},
		{"hex in backticks that names no note", false, func(data string, private, shared *models.Note) string {
			return data + "\n## Notes\ncommit `deadbeef` fixed it\n"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			from, to := moveStores(t)
			private := addTaggedNote(t, from, "ops", "Private detail", "x", "personal")
			shared := addTaggedNote(t, from, "ops", "Shared", "x", "team")
			session := models.NewSession("Review", "main", "proj", "sid-review")
			session.Summary = "relkw"
			session.RelatedNotes = []models.RelatedNote{{ID: shared.ID, Title: shared.Title, Category: shared.Category}}
			if _, err := from.SaveSession(session); err != nil {
				t.Fatal(err)
			}
			rel := entryFile(t, from, session.ID)
			data := string(readRelFile(t, from, rel))
			edited := tc.edit(data, private, shared)
			if edited == data {
				t.Fatalf("the edit changed nothing in:\n%s", data)
			}
			writeRel(t, from, rel, []byte(edited))
			report := mustMove(t, from, to, false, shared.ID, session.ID)
			refused := len(report.Refused) == 1 && report.Refused[0].ID == session.ID && strings.Contains(report.Refused[0].Reason, "stays in this store")
			if refused != tc.stays {
				t.Errorf("session refused: %v, want %v; report %+v", refused, tc.stays, report)
			}
		})
	}
}

// An index that cannot be read refuses the entry instead of calling its id wrong, and store locks
// that cannot be taken end the run instead of making every entry wait.
func TestMoveEntries_IndexAndLockFailures(t *testing.T) {
	t.Run("an index that cannot be read", func(t *testing.T) {
		from, to := moveStores(t)
		if _, err := to.db.Exec(`DROP TABLE entries`); err != nil {
			t.Fatal(err)
		}
		report := mustMove(t, from, to, false, "ffffffff-0000-0000-0000-000000000000")
		if len(report.Missing) != 0 || len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "read the index") {
			t.Errorf("report %+v", report)
		}
	})

	t.Run("a lock that cannot be taken", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows takes no store lock")
		}
		from, to := moveStores(t)
		var ids []string
		for _, title := range []string{"One", "Two", "Three"} {
			ids = append(ids, addTaggedNote(t, from, "ops", title, "x", "team").ID)
		}
		old := lockTimeout
		lockTimeout = 300 * time.Millisecond
		defer func() { lockTimeout = old }()
		holder, err := NewFileStore(to.baseDir)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		unlock, err := holder.lockStore()
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		start := time.Now()
		report := mustMove(t, from, to, false, ids...)
		if elapsed := time.Since(start); elapsed >= 2*lockTimeout {
			t.Errorf("the run took %v: it waited for the lock more than once", elapsed)
		}
		if len(report.Refused) != 3 || !strings.Contains(report.Refused[2].Reason, "not attempted") {
			t.Errorf("report %+v", report)
		}
		for _, id := range ids {
			if _, err := from.GetNote(id); err != nil {
				t.Errorf("note %s left: %v", id, err)
			}
		}
	})
}

func addVectorRow(t *testing.T, store *FileStore, id, backend, model string, dim int) {
	t.Helper()
	if _, err := store.db.Exec(`INSERT INTO vectors (doc_id, chunk, backend, model, dim, vector, created_at) VALUES (?, 0, ?, ?, ?, ?, ?)`,
		id, backend, model, dim, make([]byte, 4*dim), "2026-01-02T03:04:05Z"); err != nil {
		t.Fatal(err)
	}
}

// No vector goes along, whatever its backend, model or size, and the target's own vectors for a moved
// entry are removed: every moved entry is counted to embed. An index stamped by a newer claudemem stops
// the move before anything changes.
func TestMoveEntries_VectorsForTheTargetsSearch(t *testing.T) {
	from, to := moveStores(t)
	writeStoreSettings(t, to, `{"features.semantic_search": "true", "embedding.backend": "openai", "embedding.dimensions": "4"}`)
	served := addTaggedNote(t, from, "v1", "Served", "x", "team")
	addVectorRow(t, from, served.ID, "openai", "text-embedding-3-small", 4)
	otherBackend := addTaggedNote(t, from, "v2", "Other backend", "x", "team")
	addVectorRow(t, from, otherBackend.ID, "fake-cloud", "test-v1", 4)
	otherSize := addTaggedNote(t, from, "v3", "Other size", "x", "team")
	addVectorRow(t, from, otherSize.ID, "openai", "text-embedding-3-small", 8)
	addVectorRow(t, to, served.ID, "openai", "text-embedding-3-small", 4) // a stale vector the target already had

	report := mustMove(t, from, to, false, served.ID, otherBackend.ID, otherSize.ID)
	if len(report.Moved) != 3 || report.VectorsToEmbed != 3 {
		t.Errorf("report %+v, want all 3 entries to embed", report)
	}
	for _, id := range []string{served.ID, otherBackend.ID, otherSize.ID} {
		if rows := rowsOf(t, to, id); rows["vectors"] != "" {
			t.Errorf("the target has vectors for a moved entry: %v", rows)
		}
	}

	newer := addTaggedNote(t, from, "v4", "Newer", "x", "team")
	if _, err := to.db.Exec(`INSERT OR REPLACE INTO vector_meta (key, value) VALUES ('vectors_schema', '99')`); err != nil {
		t.Fatal(err)
	}
	if _, err := MoveEntries(from, to, moveIDs(newer.ID), false, nil); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("a target index from a newer claudemem: %v", err)
	}
	if _, err := from.GetNote(newer.ID); err != nil {
		t.Errorf("the note left: %v", err)
	}
}

func writeStoreSettings(t *testing.T, store *FileStore, settings string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(store.baseDir, "config.json"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
}

// Stores that overlap are refused, and the target's commits are synced before anything leaves this
// store; a pre-v22 vectors table here does not block the move.
func TestMoveEntries_StoresAndIndexes(t *testing.T) {
	from := setupTestStore(t)
	nested, err := NewFileStore(filepath.Join(from.baseDir, "notes", "team"))
	if err != nil {
		t.Fatal(err)
	}
	defer nested.Close()
	for _, pair := range [][2]*FileStore{{from, nested}, {nested, from}} {
		if _, err := MoveEntries(pair[0], pair[1], nil, true, nil); err == nil || !strings.Contains(err.Error(), "overlap") {
			t.Errorf("nested stores: %v", err)
		}
	}

	from, to := moveStores(t)
	note := addTaggedNote(t, from, "ops", "Synced", "x", "team")
	mustMove(t, from, to, false, note.ID)
	var level int
	if err := to.db.QueryRow(`PRAGMA synchronous`).Scan(&level); err != nil || level != 2 {
		t.Errorf("the target's synchronous level is %d (%v), want 2 (FULL)", level, err)
	}

	old, to2 := setupTestStore(t), setupTestStore(t)
	if _, err := old.db.Exec(`CREATE TABLE vectors (id TEXT PRIMARY KEY, vector BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	v21 := addTaggedNote(t, old, "ops", "Old layout", "x", "team")
	if report := mustMove(t, old, to2, false, v21.ID); len(report.Moved) != 1 {
		t.Errorf("a v21 vectors table: %+v", report)
	}
}

// A file that appears at the chosen name after it was chosen is never written over.
func TestMoveEntries_InstallNeverReplacesAFile(t *testing.T) {
	from, to := moveStores(t)
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "mine", "team")
	rel := entryFile(t, from, note.ID)
	data := readRelFile(t, from, rel)
	m := &mover{from: from, to: to}

	intruder := []byte(strings.Replace(string(data), note.ID, "11111111-2222-3333-4444-555555555555", 1))
	writeRel(t, to, rel, intruder)
	got, linked, err := m.installFile("note", note.ID, rel, rel, data)
	if err != nil || !linked || got != strings.TrimSuffix(rel, ".md")+"-2.md" {
		t.Errorf("installFile = %q %v %v", got, linked, err)
	}
	if string(readRelFile(t, to, rel)) != string(intruder) {
		t.Error("the file that appeared was written over")
	}
	if again, linked, err := m.installFile("note", note.ID, got, rel, data); err != nil || linked || again != got {
		t.Errorf("the same file there: %q %v %v", again, linked, err)
	}
	other := []byte(strings.Replace(string(data), "mine", "edited", 1))
	writeRel(t, to, "notes/ops/third.md", other)
	if _, _, err := m.installFile("note", note.ID, "notes/ops/third.md", rel, data); err == nil || !strings.Contains(err.Error(), "different file for this id") {
		t.Errorf("a different file of this id there: %v", err)
	}
}

// The target's file must never be the file this store gives up, even when the path checks pass.
func TestMoveEntries_TargetFileIsNeverTheSourceFile(t *testing.T) {
	from, to := moveStores(t)
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "samekw", "team")
	rel := entryFile(t, from, note.ID)
	data := readRelFile(t, from, rel)
	m := &mover{from: from, to: to}
	if _, _, _, err := m.placeInTarget("note", note.ID, rel, filepath.Join(to.baseDir, rel), nil, data); err == nil || !strings.Contains(err.Error(), "is this store's file") {
		t.Errorf("a source file that is the target's file: %v", err)
	}
	if _, _, _, err := m.placeInTarget("note", note.ID, rel, filepath.Join(from.baseDir, rel), nil, data); err != nil {
		t.Errorf("an ordinary source file: %v", err)
	}
}

// An entry whose file here cannot be removed is in the target, left to clean up there: the target
// has no vectors for it, so it is counted to embed all the same.
func TestMoveEntries_CountsToEmbedAnEntryLeftToCleanUp(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory whose permissions stop a removal")
	}
	from, to := moveStores(t)
	note := addTaggedNote(t, from, "ops", "Stuck", "stuck body", "team")
	dir := filepath.Join(from.baseDir, filepath.Dir(entryFile(t, from, note.ID)))
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0700) })
	report := mustMove(t, from, to, false, note.ID)
	if len(report.NeedsCleanup) != 1 || report.VectorsToEmbed != 1 {
		t.Errorf("report %+v, want it left to clean up and counted to embed", report)
	}
}

// failSyncFile makes syncFile fail for the rest of the test and returns the paths it was asked to
// sync.
func failSyncFile(t *testing.T) *[]string {
	t.Helper()
	var paths []string
	real := syncFile
	syncFile = func(path string) error {
		paths = append(paths, path)
		return errors.New("injected sync failure")
	}
	t.Cleanup(func() { syncFile = real })
	return &paths
}

// A file the move finds in the target (left there by an earlier run, a sync or another writer) may
// not be on disk yet: it is synced before a copy here goes, and while that fails, the copy stays.
func TestMoveEntries_SyncsAFileItFoundInTheTargetBeforeACopyHereGoes(t *testing.T) {
	t.Run("the entry's own file", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Deploy runbook", "foundkw", "team")
		rel := entryFile(t, from, note.ID)
		writeRel(t, to, rel, readRelFile(t, from, rel))
		real := syncFile
		synced := failSyncFile(t)
		report := mustMove(t, from, to, false, note.ID)
		if len(report.NeedsCleanup) != 1 || !strings.Contains(report.NeedsCleanup[0].Reason, "could not be synced") {
			t.Fatalf("report %+v, want it left to clean up", report)
		}
		if !exists(filepath.Join(from.baseDir, rel)) {
			t.Error("the file here was removed while the target's file was not synced")
		}
		if len(*synced) == 0 || (*synced)[0] != filepath.Join(to.baseDir, rel) {
			t.Errorf("synced %v, want the target's file first", *synced)
		}

		syncFile = real
		if report := mustMove(t, from, to, false, note.ID); len(report.Moved) != 1 {
			t.Fatalf("the second run: report %+v", report)
		}
		if exists(filepath.Join(from.baseDir, rel)) {
			t.Error("the file here is still there after the second run")
		}
	})

	t.Run("a leftover copy", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Deploy runbook", "copykw", "team")
		rel := entryFile(t, from, note.ID)
		data := readRelFile(t, from, rel)
		if report := mustMove(t, from, to, false, note.ID); len(report.Moved) != 1 {
			t.Fatalf("report %+v", report)
		}
		writeRel(t, from, rel, data) // a sync brings the file back
		failSyncFile(t)
		report := mustMove(t, from, to, false, note.ID)
		if len(report.NeedsCleanup) != 1 || !strings.Contains(report.NeedsCleanup[0].Reason, "could not be synced") {
			t.Fatalf("report %+v, want it left to clean up", report)
		}
		if !exists(filepath.Join(from.baseDir, rel)) {
			t.Error("the copy here was removed while the target's file was not synced")
		}
	})
}

// A move needs both stores open for writing; a dry run also runs on stores opened read-only.
func TestMoveEntries_ReadOnlyStoresOnlyForADryRun(t *testing.T) {
	from, to := moveStores(t)
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "rokw", "team")
	ro, err := OpenReadOnly(to.baseDir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err := MoveEntries(from, ro, moveIDs(note.ID), false, nil); err == nil || !strings.Contains(err.Error(), "open for writing") {
		t.Errorf("a move into a store opened read-only: %v", err)
	}
	if report, err := MoveEntries(from, ro, moveIDs(note.ID), true, nil); err != nil || len(report.Moved) != 1 {
		t.Errorf("a dry run into a store opened read-only: %+v %v", report, err)
	}
}

func TestMarkdownHeadingAndNoteIDForms(t *testing.T) {
	for _, tc := range []struct {
		line  string
		level int
		text  string
	}{
		{"## Related Notes", 2, "Related Notes"},
		{"## Related Notes \r", 2, "Related Notes"},
		{"   ###### Deep ##", 6, "Deep"},
		{"## C#", 2, "C#"},
		{"#", 1, ""},
		{"    ## indented four spaces", 0, ""},
		{"#hashtag", 0, ""},
		{"####### seven", 0, ""},
		{"- `id` — \"title\"", 0, ""},
	} {
		if level, text := markdownHeading(tc.line); level != tc.level || text != tc.text {
			t.Errorf("markdownHeading(%q) = %d %q, want %d %q", tc.line, level, text, tc.level, tc.text)
		}
	}
	for title, want := range map[string]bool{
		"Related Notes": true, "related notes:": true, "RELATED  NOTES :": true,
		"Related": false, "Related Notes and more": false, "Related Note": false,
	} {
		if got := isRelatedNotesTitle(title); got != want {
			t.Errorf("isRelatedNotesTitle(%q) = %v", title, got)
		}
	}
	for ref, want := range map[string]bool{
		"abcdef12": true, "abcdef12-0000": true, "eeeeeeee-0000-0000-0000-000000000000": true,
		"ABCDEF12": false, "abcdefgh": false, "abcdef12-": true, "abcdef120": false,
		"eeeeeeee-0000-0000-0000-0000000000000": false, "Private detail notes": false,
	} {
		if got := looksLikeNoteID(ref); got != want {
			t.Errorf("looksLikeNoteID(%q) = %v", ref, got)
		}
	}
}

// A rerun counts to embed the entries an earlier run put into the target that still have no vectors
// there, so its report still prints the command that embeds them; once embedded, they no longer count.
func TestMoveEntries_ARerunCountsToEmbedWhatIsStillNotEmbedded(t *testing.T) {
	from, to := moveStores(t)
	writeStoreSettings(t, to, `{"embedding.backend": "openai", "embedding.model": "test-v1", "embedding.dimensions": "4"}`)
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "body", "team")
	if report := mustMove(t, from, to, false, note.ID); len(report.Moved) != 1 || report.VectorsToEmbed != 1 {
		t.Fatalf("first run: %+v", report)
	}
	for _, dryRun := range []bool{true, false} {
		if report := mustMove(t, from, to, dryRun, note.ID); len(report.AlreadyInTarget) != 1 || report.VectorsToEmbed != 1 {
			t.Errorf("rerun (dry run %v) of an entry the target has not embedded: %+v", dryRun, report)
		}
	}
	addVectorRow(t, to, note.ID, "openai", "test-v1", 4) // the target embeds it, with its backend
	if report := mustMove(t, from, to, false, note.ID); len(report.AlreadyInTarget) != 1 || report.VectorsToEmbed != 0 {
		t.Errorf("rerun after the target embedded it: %+v", report)
	}
}

// A rerun counts to embed an entry already in the target while the target has no vectors of its
// active backend and model for it, as `reindex --vectors --missing` there would embed it: vectors of
// another backend (another machine's, say) do not count.
func TestMoveEntries_CountsToEmbedByTheTargetsActiveBackend(t *testing.T) {
	from, to := moveStores(t)
	writeStoreSettings(t, to, `{"features.semantic_search": "true", "embedding.backend": "openai", "embedding.model": "text-embedding-3-small", "embedding.dimensions": "4"}`)
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "body", "team")
	if report := mustMove(t, from, to, false, note.ID); len(report.Moved) != 1 {
		t.Fatalf("first run: %+v", report)
	}
	addVectorRow(t, to, note.ID, "ollama", "nomic-embed-text", 4) // embedded by another backend
	for _, dryRun := range []bool{true, false} {
		if report := mustMove(t, from, to, dryRun, note.ID); len(report.AlreadyInTarget) != 1 || report.VectorsToEmbed != 1 {
			t.Errorf("rerun (dry run %v) with vectors of another backend only: %+v", dryRun, report)
		}
	}
	addVectorRow(t, to, note.ID, "openai", "text-embedding-3-small", 4) // the target's own backend
	if report := mustMove(t, from, to, false, note.ID); len(report.AlreadyInTarget) != 1 || report.VectorsToEmbed != 0 {
		t.Errorf("rerun after the target embedded it: %+v", report)
	}
}
