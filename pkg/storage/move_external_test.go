package storage

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ─── move: a writer outside claudemem saves a file while it moves ──────────────
//
// An editor or an add-only sync takes no claudemem lock. A file it saves while the move has the entry
// in flight must not be removed: the target holds the version the move read, the newer one stays here,
// and the entry is left to clean up.

const savedParagraph = "PARAGRAPH SAVED BY ANOTHER TOOL"

func appendParagraph(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("\n\n"+savedParagraph+"\n")...), 0600); err != nil {
		t.Fatal(err)
	}
}

// atStep runs fn once, the first time the move reaches step.
func atStep(t *testing.T, step string, fn func()) {
	t.Helper()
	done := false
	moveTestHook = func(s string) {
		if s == step && !done {
			done = true
			fn()
		}
	}
	t.Cleanup(func() { moveTestHook = nil })
}

func TestMoveEntries_KeepsAFileSavedByAnotherWriterDuringTheMove(t *testing.T) {
	for _, step := range []string{"before-install", "after-install", "after-target-rows"} {
		t.Run(step, func(t *testing.T) {
			from, to := moveStores(t)
			note := addTaggedNote(t, from, "ops", "Deploy runbook", "original text", "team")
			rel := entryFile(t, from, note.ID)
			atStep(t, step, func() { appendParagraph(t, filepath.Join(from.baseDir, rel)) })
			report := mustMove(t, from, to, false, note.ID)
			if len(report.Moved) != 0 || len(report.NeedsCleanup) != 1 || !strings.Contains(report.NeedsCleanup[0].Reason, "changed during the move") {
				t.Fatalf("report %+v, want it left to clean up", report)
			}
			if here := string(readRelFile(t, from, rel)); !strings.Contains(here, savedParagraph) {
				t.Errorf("the saved paragraph is gone from this store:\n%s", here)
			}
			if rows := rowsOf(t, from, note.ID); rows["entries"] == "" {
				t.Error("this store's rows for it were removed")
			}
			if there := string(readRelFile(t, to, entryFile(t, to, note.ID))); strings.Contains(there, savedParagraph) || !strings.Contains(there, "original text") {
				t.Errorf("the target's file is not the version the move read:\n%s", there)
			}
			for _, name := range dirNames(t, filepath.Join(from.baseDir, filepath.Dir(rel))) {
				if strings.HasSuffix(name, setAsideSuffix) {
					t.Errorf("a set-aside file is left: %s", name)
				}
			}
		})
	}

	t.Run("a leftover copy", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Deploy runbook", "original text", "team")
		rel := entryFile(t, from, note.ID)
		data := readRelFile(t, from, rel)
		if report := mustMove(t, from, to, false, note.ID); len(report.Moved) != 1 {
			t.Fatalf("report %+v", report)
		}
		writeRel(t, from, rel, data) // a sync brings the file back
		atStep(t, "before-leftover-remove", func() { appendParagraph(t, filepath.Join(from.baseDir, rel)) })
		report := mustMove(t, from, to, false, note.ID)
		if len(report.NeedsCleanup) != 1 || !strings.Contains(report.NeedsCleanup[0].Reason, "changed during the move") {
			t.Fatalf("report %+v, want it left to clean up", report)
		}
		if here := string(readRelFile(t, from, rel)); !strings.Contains(here, savedParagraph) {
			t.Errorf("the saved paragraph is gone from this store:\n%s", here)
		}
	})
}

// A run that stops after setting a file aside leaves it under its hidden name: the next run removes it
// when it is the moved file, and reports it when it differs.
func TestMoveEntries_HandlesAFileAStoppedRunSetAside(t *testing.T) {
	for _, same := range []bool{true, false} {
		name := "the same file"
		if !same {
			name = "a different file"
		}
		t.Run(name, func(t *testing.T) {
			from, to := moveStores(t)
			note := addTaggedNote(t, from, "ops", "Deploy runbook", "original text", "team")
			rel := entryFile(t, from, note.ID)
			data := readRelFile(t, from, rel)
			if report := mustMove(t, from, to, false, note.ID); len(report.Moved) != 1 {
				t.Fatalf("report %+v", report)
			}
			aside := setAsidePath(filepath.Join(from.baseDir, rel))
			if !same {
				data = []byte(strings.Replace(string(data), "original text", "edited text", 1))
			}
			if err := os.WriteFile(aside, data, 0600); err != nil {
				t.Fatal(err)
			}
			report := mustMove(t, from, to, false, note.ID)
			switch {
			case same && (len(report.AlreadyInTarget) != 1 || exists(aside)):
				t.Errorf("report %+v; the set-aside file is still there: %v", report, exists(aside))
			case !same && (len(report.NeedsCleanup) != 1 || !exists(aside)):
				t.Errorf("report %+v; the different set-aside file is gone: %v", report, !exists(aside))
			}
		})
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// A different copy of an entry's file that arrives after the walk at the start of the run (a sync pull
// during a long move) is found by a walk at the end: the entry is left to clean up, not moved.
func TestMoveEntries_ReportsADifferentCopyThatArrivesDuringTheRun(t *testing.T) {
	for _, where := range []string{"the target", "this store"} {
		t.Run(where, func(t *testing.T) {
			from, to := moveStores(t)
			note := addTaggedNote(t, from, "ops", "Deploy runbook", "my version", "team")
			rel := entryFile(t, from, note.ID)
			theirs := []byte(strings.Replace(string(readRelFile(t, from, rel)), "my version", "their version", 1))
			store := to
			if where == "this store" {
				store = from
			}
			atStep(t, "after-walk", func() { writeRel(t, store, filepath.Join("notes", "archive", filepath.Base(rel)), theirs) })
			report := mustMove(t, from, to, false, note.ID)
			if len(report.Moved) != 0 || len(report.NeedsCleanup) != 1 || !strings.Contains(report.NeedsCleanup[0].Reason, "arrived in "+where) {
				t.Fatalf("report %+v, want it left to clean up", report)
			}
		})
	}
}

// A refusal names what is wrong: the target holding the file elsewhere is not "in neither store", two
// copies in the target are not "this store's file cannot be read", and a file a failed entry left in
// the target is reported.
func TestMoveEntries_RefusalsNameWhatIsWrong(t *testing.T) {
	t.Run("the target holds the file away from its row", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Deploy runbook", "body", "team")
		rel := entryFile(t, from, note.ID)
		data := readRelFile(t, from, rel)
		writeRel(t, to, rel, data)
		if _, err := to.Reindex(); err != nil { // the target lists it at rel
			t.Fatal(err)
		}
		os.Remove(filepath.Join(to.baseDir, rel)) // then a pull moved it to another category
		elsewhere := filepath.Join("notes", "archive", filepath.Base(rel))
		writeRel(t, to, elsewhere, data)
		os.Remove(filepath.Join(from.baseDir, rel))
		report := mustMove(t, from, to, false, note.ID)
		if len(report.Refused) != 1 || strings.Contains(report.Refused[0].Reason, "neither store") ||
			!strings.Contains(report.Refused[0].Reason, elsewhere) || !strings.Contains(report.Refused[0].Reason, "reindex the target") {
			t.Errorf("report %+v", report)
		}
	})

	t.Run("two copies in the target, none here", func(t *testing.T) {
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Deploy runbook", "body", "team")
		rel := entryFile(t, from, note.ID)
		data := readRelFile(t, from, rel)
		writeRel(t, to, rel, data)
		writeRel(t, to, filepath.Join("notes", "archive", filepath.Base(rel)), data)
		os.Remove(filepath.Join(from.baseDir, rel))
		report := mustMove(t, from, to, false, note.ID)
		if len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "holds 2 copies") {
			t.Errorf("report %+v", report)
		}
	})

	t.Run("a file the rollback cannot remove", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs a directory whose permissions stop a removal")
		}
		from, to := moveStores(t)
		note := addTaggedNote(t, from, "ops", "Deploy runbook", "body", "team")
		rel := entryFile(t, from, note.ID)
		dir := filepath.Join(to.baseDir, filepath.Dir(rel))
		atStep(t, "after-install", func() {
			if _, err := to.db.Exec(`DROP TABLE memory_fts`); err != nil { // the rows write fails
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0500); err != nil { // and the file cannot be removed
				t.Fatal(err)
			}
		})
		t.Cleanup(func() { os.Chmod(dir, 0700) })
		report := mustMove(t, from, to, false, note.ID)
		if len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "stays there at "+rel) {
			t.Errorf("report %+v", report)
		}
	})
}
