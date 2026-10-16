package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writePendingRecord leaves a record in a store's move journal, as a move that stopped before the
// store's rows were written does.
func writePendingRecord(t *testing.T, store string) {
	t.Helper()
	dir := filepath.Join(store, ".index", "move-pending")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	record := `{"id":"0f0e0d0c-0000-4000-8000-000000000001","file":"notes/ops/runbook.md","temp":"notes/ops/.runbook.0a0b0c.tmp","sha256":"00"}`
	if err := os.WriteFile(filepath.Join(dir, "0a0b0c0d.json"), []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
}

// reindex and sync pull, which reindexes, warn while the store's move journal holds records: a move
// into it stopped, and the reindex lists what that move put there without rows. They go on.
func TestCLI_ReindexAndPullWarnWhileAMoveIsPending(t *testing.T) {
	store := newStoreDir(t)
	if _, stderr, err := runCLIWithStderr(t, store, "reindex"); err != nil || strings.Contains(stderr, "move-pending") {
		t.Fatalf("reindex without a record: %v, warned %q", err, stderr)
	}
	writePendingRecord(t, store)
	if _, stderr, err := runCLIWithStderr(t, store, "reindex"); err != nil || !strings.Contains(stderr, "move-pending") {
		t.Errorf("reindex with a record: %v, stderr %q", err, stderr)
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "test", "GIT_AUTHOR_EMAIL": "test@example.com",
		"GIT_COMMITTER_NAME": "test", "GIT_COMMITTER_EMAIL": "test@example.com",
	} {
		t.Setenv(k, v)
	}
	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	first, second := t.TempDir(), t.TempDir()
	addNoteCLI(t, first, "ops", "Deploy runbook", "steps", "team")
	mustRunCLI(t, first, "sync", "init", remote)
	mustRunCLI(t, first, "sync", "push")
	mustRunCLI(t, second, "sync", "init", remote)
	writePendingRecord(t, second)
	if _, stderr, err := runCLIWithStderr(t, second, "sync", "pull"); err != nil || !strings.Contains(stderr, "move-pending") {
		t.Errorf("sync pull with a record: %v, stderr %q", err, stderr)
	}
}

// A file that a stopped move kept in a store, because the store its entry came from no longer holds
// it, is settled by a reindex, as the move's warning says: the entry stays, its record is cleared, and
// the reindex does not send the operator back to the move.
func TestCLI_ReindexSettlesAFileAStoppedMoveKept(t *testing.T) {
	store := newStoreDir(t)
	id := addNoteCLI(t, store, "ops", "Deploy runbook", "steps", "team")
	files, _ := filepath.Glob(filepath.Join(store, "notes", "ops", "*.md"))
	if len(files) != 1 {
		t.Fatalf("note files %v", files)
	}
	rel, err := filepath.Rel(store, files[0])
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store, ".index", "move-pending")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	record := fmt.Sprintf(`{"id":%q,"file":%q,"temp":%q,"sha256":"00","from":%q,"from_file":%q,"kept":true}`,
		id, rel, filepath.Join(filepath.Dir(rel), ".deploy-runbook.0a0b0c.tmp"), t.TempDir(), rel)
	if err := os.WriteFile(filepath.Join(dir, "0a0b0c0d.json"), []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := runCLIWithStderr(t, store, "reindex")
	if err != nil || strings.Contains(stderr, "run that move again") || !strings.Contains(stderr, "stays in this store") {
		t.Errorf("reindex: %v, stderr %q", err, stderr)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("the record is still there: %v", left)
	}
}
