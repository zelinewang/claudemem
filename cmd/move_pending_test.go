package cmd

import (
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
