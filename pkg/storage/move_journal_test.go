package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ─── move: the target's journal of files a stopped move left there ────────────

type stoppedRun struct{}

// moveStoppedAt runs a move that stops at step, as if the process died there (the store locks are
// released either way).
func moveStoppedAt(t *testing.T, step string, from, to *FileStore, ids ...string) {
	t.Helper()
	atStep(t, step, func() { panic(stoppedRun{}) })
	defer func() {
		moveTestHook = nil
		if r := recover(); r == nil {
			t.Fatalf("the move did not reach %s", step)
		} else if _, ok := r.(stoppedRun); !ok {
			panic(r)
		}
	}()
	MoveEntries(from, to, moveIDs(ids...), false, nil)
}

func journalRecords(t *testing.T, store *FileStore) []string {
	t.Helper()
	entries, err := os.ReadDir(store.pendingDir())
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// tempsIn lists the temp files in a directory of a store.
func tempsIn(t *testing.T, store *FileStore, dir string) []string {
	t.Helper()
	temps, _ := filepath.Glob(filepath.Join(store.baseDir, dir, ".*.tmp"))
	return temps
}

// A rerun after a move that stopped with the entry's file in the target and no rows there is not
// refused for that file when the entry changed here meanwhile: the journal names it as the stopped
// move's own, and it goes.
func TestMoveEntries_ARerunRemovesTheFileAStoppedMoveLeft(t *testing.T) {
	from, to := moveStores(t)
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "first text", "team")
	moveStoppedAt(t, "after-install", from, to, note.ID)
	if len(journalRecords(t, to)) != 1 {
		t.Fatalf("journal after the stop: %v", journalRecords(t, to))
	}
	note.Content = "edited after the stop"
	if err := from.UpdateNote(note); err != nil {
		t.Fatal(err)
	}
	report := mustMove(t, from, to, false, note.ID)
	if len(report.Moved) != 1 || len(report.Refused) != 0 {
		t.Fatalf("rerun: %+v", report)
	}
	if got, err := to.GetNote(note.ID); err != nil || got.Content != "edited after the stop" {
		t.Errorf("the target holds %v (%v), want the edited note", got, err)
	}
	if records := journalRecords(t, to); len(records) != 0 {
		t.Errorf("records left in the journal: %v", records)
	}
	files, _, _ := entryFilesByID(to.baseDir, map[string]bool{note.ID: true})
	if len(files[note.ID]) != 1 {
		t.Errorf("the target holds %v for the note, want one file", files[note.ID])
	}
}

// A temp file a stopped move wrote into the target goes on the next run, also when that run moves
// something else: a note that turned out to be private leaves no copy in the team store.
func TestMoveEntries_TheNextRunRemovesATempAStoppedMoveLeft(t *testing.T) {
	from, to := moveStores(t)
	private := addTaggedNote(t, from, "ops", "Salary talk", "private text", "personal")
	other := addTaggedNote(t, from, "ops", "Deploy runbook", "team text", "team")
	moveStoppedAt(t, "after-temp", from, to, private.ID)
	if temps := tempsIn(t, to, filepath.Join("notes", "ops")); len(temps) != 1 {
		t.Fatalf("temps after the stop: %v", temps)
	}

	dry := mustMove(t, from, to, true, other.ID)
	if len(tempsIn(t, to, filepath.Join("notes", "ops"))) != 1 || !strings.Contains(strings.Join(dry.Warnings, "\n"), "would remove the temp file") {
		t.Errorf("dry run: %+v", dry)
	}
	report := mustMove(t, from, to, false, other.ID)
	if len(report.Moved) != 1 || !strings.Contains(strings.Join(report.Warnings, "\n"), "removed the temp file") {
		t.Errorf("report %+v", report)
	}
	if temps := tempsIn(t, to, filepath.Join("notes", "ops")); len(temps) != 0 {
		t.Errorf("temps left in the target: %v", temps)
	}
	if records := journalRecords(t, to); len(records) != 0 {
		t.Errorf("records left in the journal: %v", records)
	}
	if _, err := to.GetNote(private.ID); err == nil {
		t.Error("the private note is in the target")
	}
}

// A record whose entry the target's index lists at its file is from a move whose rows committed:
// the file stays.
func TestMoveEntries_TheJournalKeepsACommittedFile(t *testing.T) {
	from, to := moveStores(t)
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "body", "team")
	if report := mustMove(t, from, to, false, note.ID); len(report.Moved) != 1 {
		t.Fatalf("report %+v", report)
	}
	rel := entryFile(t, to, note.ID)
	if err := to.recordPending(pendingInstall{ID: note.ID, File: rel, Temp: filepath.Join(filepath.Dir(rel), ".deploy-runbook.0a0b0c.tmp"), SHA256: sha256Hex(readRelFile(t, to, rel))}); err != nil {
		t.Fatal(err)
	}
	if report := mustMove(t, from, to, false, note.ID); len(report.AlreadyInTarget) != 1 {
		t.Fatalf("rerun: %+v", report)
	}
	if !exists(filepath.Join(to.baseDir, rel)) {
		t.Error("the committed file was removed")
	}
	if records := journalRecords(t, to); len(records) != 0 {
		t.Errorf("records left in the journal: %v", records)
	}
}

// A file a stopped move put into the target stays there when the store it came from no longer holds
// the entry: it may be the only copy left. The record stays too, so the next run says so again.
func TestMoveEntries_TheJournalKeepsAFileTheSourceNoLongerHolds(t *testing.T) {
	for _, how := range []string{"note deleted here", "file gone here, then reindexed (a pull)"} {
		t.Run(how, func(t *testing.T) {
			from, to := moveStores(t)
			note := addTaggedNote(t, from, "ops", "Deploy runbook", "the only text", "team")
			other := addTaggedNote(t, from, "infra", "Network map", "routes", "team")
			rel := entryFile(t, from, note.ID)
			moveStoppedAt(t, "after-install", from, to, note.ID)
			switch how {
			case "note deleted here":
				if err := from.DeleteNote(note.ID); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Remove(filepath.Join(from.baseDir, rel)); err != nil {
					t.Fatal(err)
				}
				if _, err := from.Reindex(); err != nil {
					t.Fatal(err)
				}
			}
			for _, dryRun := range []bool{true, false} {
				report := mustMove(t, from, to, dryRun, other.ID)
				if !exists(filepath.Join(to.baseDir, rel)) {
					t.Fatalf("dry run %v: the target's file, the only copy of the note, was removed; report %+v", dryRun, report)
				}
				if warnings := strings.Join(report.Warnings, "\n"); !strings.Contains(warnings, "no longer holds it") {
					t.Errorf("dry run %v: warnings %q", dryRun, warnings)
				}
			}
			if records := journalRecords(t, to); len(records) != 1 {
				t.Errorf("journal records %v, want the record kept", records)
			}
		})
	}
}

// The record names the store the entry came from, which may not be the store the next run moves
// from; a record without that name is checked against the store the run moves from.
func TestMoveEntries_TheJournalChecksTheStoreTheRecordNames(t *testing.T) {
	for _, stillThere := range []bool{true, false} {
		t.Run(fmt.Sprintf("another store, still holding it: %v", stillThere), func(t *testing.T) {
			from, to := moveStores(t)
			third := setupTestStore(t)
			note := addTaggedNote(t, third, "ops", "Deploy runbook", "text from the third store", "team")
			other := addTaggedNote(t, from, "infra", "Network map", "routes", "team")
			rel := entryFile(t, third, note.ID)
			moveStoppedAt(t, "after-install", third, to, note.ID)
			if !stillThere {
				if err := third.DeleteNote(note.ID); err != nil {
					t.Fatal(err)
				}
			}
			report := mustMove(t, from, to, false, other.ID)
			if got := exists(filepath.Join(to.baseDir, rel)); got == stillThere {
				t.Errorf("the target's file exists: %v, want %v; report %+v", got, !stillThere, report)
			}
			if records := journalRecords(t, to); (len(records) == 0) != stillThere {
				t.Errorf("journal records %v", records)
			}
			if !stillThere && !strings.Contains(strings.Join(report.Warnings, "\n"), resolvedDir(third.baseDir)+" no longer holds it") {
				t.Errorf("the warning does not name the store the note came from: %q", report.Warnings)
			}
		})
	}

	for _, here := range []bool{true, false} {
		t.Run(fmt.Sprintf("a record without the store, this store holding it: %v", here), func(t *testing.T) {
			from, to := moveStores(t)
			note := addTaggedNote(t, from, "ops", "Deploy runbook", "body", "team")
			rel := entryFile(t, from, note.ID)
			data := readRelFile(t, from, rel)
			writeRel(t, to, rel, data) // a file a stopped move put there, without rows
			if err := to.recordPending(pendingInstall{ID: note.ID, File: rel, Temp: filepath.Join(filepath.Dir(rel), ".deploy-runbook.0a0b0c.tmp"), SHA256: sha256Hex(data)}); err != nil {
				t.Fatal(err)
			}
			if !here {
				if err := from.DeleteNote(note.ID); err != nil {
					t.Fatal(err)
				}
			}
			other := addTaggedNote(t, from, "infra", "Network map", "routes", "team")
			mustMove(t, from, to, false, other.ID)
			if got := exists(filepath.Join(to.baseDir, rel)); got == here {
				t.Errorf("the target's file exists: %v, want %v", got, !here)
			}
		})
	}
}

// A file the journal cannot remove (its directory refuses it) is reported, and its record stays, so
// that a later move removes it once it can.
func TestMoveEntries_TheJournalKeepsTheRecordOfAFileItCannotRemove(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory whose permissions stop a removal")
	}
	from, to := moveStores(t)
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "body", "team")
	other := addTaggedNote(t, from, "infra", "Network map", "routes", "team")
	rel := entryFile(t, from, note.ID)
	moveStoppedAt(t, "after-install", from, to, note.ID)
	dir := filepath.Join(to.baseDir, filepath.Dir(rel))
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0700) })
	report := mustMove(t, from, to, false, other.ID)
	if !exists(filepath.Join(to.baseDir, rel)) {
		t.Fatal("the file was removed through a directory that refuses it")
	}
	if warnings := strings.Join(report.Warnings, "\n"); !strings.Contains(warnings, "could not remove "+rel) {
		t.Errorf("no warning about the file it could not remove: %q", warnings)
	}
	if records := journalRecords(t, to); len(records) != 1 {
		t.Fatalf("journal records %v, want the record kept", records)
	}

	os.Chmod(dir, 0700)
	third := addTaggedNote(t, from, "infra", "Rack plan", "racks", "team")
	mustMove(t, from, to, false, third.ID)
	if exists(filepath.Join(to.baseDir, rel)) {
		t.Error("a later move did not remove the file once it could")
	}
	if records := journalRecords(t, to); len(records) != 0 {
		t.Errorf("journal records %v after the file went", records)
	}
}

// A dry run while another move into the target is running finds that move's record in the journal. It
// does not take the record for a stopped move's and say it would remove the file being moved: the
// target's lock is taken, so it says another move is running and leaves the journal unjudged.
func TestMoveEntries_ADryRunDuringAMoveLeavesItsJournalAlone(t *testing.T) {
	from, to := moveStores(t)
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "body", "team")
	second := setupTestStore(t)
	other := addTaggedNote(t, second, "misc", "Checklist", "items", "team")
	var dry *MoveReport
	atStep(t, "after-install", func() { // the running move holds both locks; its record is written
		dry = mustMove(t, second, to, true, other.ID)
	})
	if report := mustMove(t, from, to, false, note.ID); len(report.Moved) != 1 {
		t.Fatalf("the running move: %+v", report)
	}
	warnings := strings.Join(dry.Warnings, "\n")
	if strings.Contains(warnings, "would remove") || !strings.Contains(warnings, "another move") {
		t.Errorf("dry run warnings %q", warnings)
	}
	if len(dry.Moved) != 1 {
		t.Errorf("dry run %+v", dry)
	}
}
