package sync

import (
	"os"
	"path/filepath"
	"testing"
)

// lockProbe is a LockWrites that records, for one file a git step brings in, whether it was there
// when the lock was taken and when it was released.
type lockProbe struct {
	path          string
	calls         int
	before, after bool
}

func (p *lockProbe) lock() (func(), error) {
	p.calls++
	p.before = fileExists(p.path)
	return func() { p.after = fileExists(p.path) }, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// check reports a step that wrote the file outside the lock, or took the lock a number of times
// other than want.
func (p *lockProbe) check(t *testing.T, step string, want int) {
	t.Helper()
	if p.calls != want {
		t.Errorf("%s took the store lock %d times, want %d", step, p.calls, want)
	}
	if want > 0 && (p.before || !p.after) {
		t.Errorf("%s did not bring %s in while holding the lock (there when locked: %v, when released: %v)",
			step, filepath.Base(p.path), p.before, p.after)
	}
}

// newSyncedStore is a store directory synced with remote that holds the given note.
func newSyncedStore(t *testing.T, remote, note string) (string, *GitSync) {
	t.Helper()
	dir := t.TempDir()
	writeNote(t, dir, note)
	g := NewGitSync(dir)
	if err := g.Init(remote); err != nil {
		t.Fatalf("init: %v", err)
	}
	setGitUser(t, dir)
	if err := g.Push("seed"); err != nil {
		t.Fatalf("push: %v", err)
	}
	return dir, g
}

func writeNote(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "notes", "ops"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes", "ops", name), []byte("# "+name+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

// Every git step that writes the working tree runs while LockWrites is held; a push that the remote
// accepts writes no file and takes no lock.
func TestGitSync_WritesTheTreeOnlyUnderLockWrites(t *testing.T) {
	requireGit(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote := setupLocalBare(t)
	dirA, gA := newSyncedStore(t, remote, "from-a.md")

	// init adopting the remote's history
	dirB := t.TempDir()
	gB := NewGitSync(dirB)
	adopt := &lockProbe{path: filepath.Join(dirB, "notes", "ops", "from-a.md")}
	gB.LockWrites = adopt.lock
	if err := gB.Init(remote); err != nil {
		t.Fatalf("B init: %v", err)
	}
	setGitUser(t, dirB)
	adopt.check(t, "init", 1)

	// pull
	writeNote(t, dirA, "second-from-a.md")
	if err := gA.Push("second"); err != nil {
		t.Fatalf("A push: %v", err)
	}
	pull := &lockProbe{path: filepath.Join(dirB, "notes", "ops", "second-from-a.md")}
	gB.LockWrites = pull.lock
	if err := gB.Pull(); err != nil {
		t.Fatalf("B pull: %v", err)
	}
	pull.check(t, "pull", 1)

	// a push the remote accepts
	writeNote(t, dirB, "from-b.md")
	plain := &lockProbe{path: filepath.Join(dirB, "notes", "ops", "from-b.md")}
	gB.LockWrites = plain.lock
	if err := gB.Push("from b"); err != nil {
		t.Fatalf("B push: %v", err)
	}
	plain.check(t, "an accepted push", 0)

	// a push the remote refuses, then the rebase that brings the other machine's note in
	gA.LockWrites = (&lockProbe{}).lock
	if err := gA.Pull(); err != nil {
		t.Fatalf("A pull: %v", err)
	}
	writeNote(t, dirA, "third-from-a.md")
	if err := gA.Push("third"); err != nil {
		t.Fatalf("A push: %v", err)
	}
	writeNote(t, dirB, "second-from-b.md")
	rebase := &lockProbe{path: filepath.Join(dirB, "notes", "ops", "third-from-a.md")}
	gB.LockWrites = rebase.lock
	if err := gB.Push("second from b"); err != nil {
		t.Fatalf("B push after the remote moved on: %v", err)
	}
	rebase.check(t, "the rebase after a refused push", 1)
}
