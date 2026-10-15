//go:build !windows

package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ─── Commands that write a store's files or index wait for its write lock ─────
//
// A move holds both stores' locks while it moves one entry. A command that rewrites files or the
// index without the lock can run between the move's rows and its file and drop the entry's rows
// (review of the move, finding 3). Each command runs in a child process here, as it does when a
// hook or a cron job starts it while a move runs.

// holdStoreLock takes the store's write lock as claudemem does (a flock on .write.lock) and returns
// its release.
func holdStoreLock(t *testing.T, store string) func() {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(store, ".write.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		t.Fatal(err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}

// runWhileLocked starts `claudemem --store <store> args...` in a child process while the store's
// lock is held, checks that it is still waiting a second later, releases the lock and returns what
// it printed.
func runWhileLocked(t *testing.T, store string, args ...string) string {
	t.Helper()
	release := holdStoreLock(t, store)
	child := exec.Command(os.Args[0])
	child.Env = append(os.Environ(), "HOME="+t.TempDir(),
		cliChildArgs+"="+strings.Join(append([]string{"--store", store}, args...), "\x1f"))
	var out bytes.Buffer
	child.Stdout, child.Stderr = &out, &out
	if err := child.Start(); err != nil {
		release()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case err := <-done:
		release()
		t.Fatalf("claudemem %s finished while the store lock was held (%v):\n%s", strings.Join(args, " "), err, out.String())
	case <-time.After(time.Second):
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("claudemem %s: %v\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String()
}

func TestCLI_WritersWaitForTheStoreLock(t *testing.T) {
	t.Run("reindex", func(t *testing.T) {
		store := t.TempDir()
		addNoteCLI(t, store, "ops", "Deploy runbook", "lockkw deploy", "team")
		if out := runWhileLocked(t, store, "reindex"); !strings.Contains(out, "1 entries indexed") {
			t.Errorf("reindex printed:\n%s", out)
		}
	})

	t.Run("import", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		addNoteCLI(t, src, "ops", "Deploy runbook", "lockkw deploy", "team")
		archive := filepath.Join(t.TempDir(), "backup.tar.gz")
		mustRunCLI(t, src, "export", archive)
		if out := runWhileLocked(t, dst, "import", archive); !strings.Contains(out, "Indexed 1 entries") {
			t.Errorf("import printed:\n%s", out)
		}
	})

	t.Run("repair", func(t *testing.T) {
		store := t.TempDir()
		mustRunCLI(t, store, "config", "set", "features.semantic_search", "true") // health reads the vectors table
		addNoteCLI(t, store, "ops", "Deploy runbook", "lockkw deploy", "team")
		files, _ := filepath.Glob(filepath.Join(store, "notes", "*", "*.md"))
		if len(files) != 1 {
			t.Fatalf("note files: %v", files)
		}
		if err := os.Remove(files[0]); err != nil { // the index now lists a note that has no file
			t.Fatal(err)
		}
		if out := runWhileLocked(t, store, "repair", "--yes"); !strings.Contains(out, "FTS index rebuilt (0 entries)") {
			t.Errorf("repair printed:\n%s", out)
		}
	})

	t.Run("session repair", func(t *testing.T) {
		store, sessions := setupSessionRepairStore(t, map[string]string{"fused.md": repairCLI_fixtureFused})
		mustRunCLI(t, store, "reindex") // an index makes the directory a store
		out := runWhileLocked(t, store, "session", "repair", "--apply", "--backup-dir", t.TempDir())
		if !strings.Contains(out, "applied: 1 file(s) repaired") || !strings.Contains(out, "search index rebuilt") {
			t.Errorf("session repair printed:\n%s", out)
		}
		if data, err := os.ReadFile(filepath.Join(sessions, "fused.md")); err != nil || strings.Contains(string(data), "body **Solution**") {
			t.Errorf("the session was not repaired (%v):\n%s", err, data)
		}
	})

	t.Run("sync pull", func(t *testing.T) {
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
		store := t.TempDir()
		addNoteCLI(t, store, "ops", "Deploy runbook", "lockkw deploy", "team")
		mustRunCLI(t, store, "sync", "init", remote)
		mustRunCLI(t, store, "sync", "push")
		if out := runWhileLocked(t, store, "sync", "pull"); !strings.Contains(out, "reconciled 1 FTS entries") {
			t.Errorf("sync pull printed:\n%s", out)
		}
	})
}
