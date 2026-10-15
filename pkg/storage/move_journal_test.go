package storage

import (
	"os"
	"path/filepath"
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
	files, _ := entryFilesByID(to.baseDir, map[string]bool{note.ID: true})
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
