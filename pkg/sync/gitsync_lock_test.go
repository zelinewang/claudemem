package sync

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

	// a push the remote accepts: it writes no file of the tree, and takes the lock once, for its add
	// and commit (TestGitSync_PushCommitsUnderTheLock)
	writeNote(t, dirB, "from-b.md")
	plain := &lockProbe{path: filepath.Join(dirB, "notes", "ops", "from-b.md")}
	gB.LockWrites = plain.lock
	if err := gB.Push("from b"); err != nil {
		t.Fatalf("B push: %v", err)
	}
	if plain.calls != 1 {
		t.Errorf("an accepted push took the store lock %d times, want 1", plain.calls)
	}

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
	rebase.check(t, "the commit, then the rebase after a refused push", 2)
}

// slowRemote points origin at an ssh URL whose ssh command waits, then fails: a remote that hangs on
// the network. The command records whether the store lock was held while it ran; the returned
// LockWrites marks the lock as held.
func slowRemote(t *testing.T, dir string) (lockWrites func() (func(), error), heldDuringNetwork func() bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the slow ssh command is a shell script")
	}
	bin := t.TempDir()
	marker := filepath.Join(bin, "locked")
	script := filepath.Join(bin, "slow-ssh")
	body := "#!/bin/sh\n[ -e '" + marker + "' ] && touch '" + marker + ".seen'\nsleep 0.2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", script)
	cmd := exec.Command("git", "remote", "set-url", "origin", "ssh://git@claudemem-test.invalid/memory.git")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("set-url: %v\n%s", err, out)
	}
	lockWrites = func() (func(), error) {
		if err := os.WriteFile(marker, nil, 0600); err != nil {
			return nil, err
		}
		return func() { os.Remove(marker) }, nil
	}
	return lockWrites, func() bool { return fileExists(marker + ".seen") }
}

// The network steps of a sync run without the store lock: a remote that hangs (75 s in the review)
// must not make every note write wait for it and fail.
func TestGitSync_TalksToTheRemoteWithoutTheLock(t *testing.T) {
	requireGit(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote := setupLocalBare(t)

	t.Run("pull", func(t *testing.T) {
		dir, g := newSyncedStore(t, remote, "pull-note.md")
		var held func() bool
		g.LockWrites, held = slowRemote(t, dir)
		if err := g.Pull(); err == nil {
			t.Fatal("a pull from a remote that fails succeeded")
		}
		if held() {
			t.Error("the pull held the store lock while it waited on the remote")
		}
	})

	t.Run("the rebase after a refused push", func(t *testing.T) {
		dir, g := newSyncedStore(t, remote, "push-note.md")
		writeNote(t, dir, "unpushed.md")
		var held func() bool
		g.LockWrites, held = slowRemote(t, dir)
		if err := g.Push("unpushed"); err == nil {
			t.Fatal("a push to a remote that fails succeeded")
		}
		if held() {
			t.Error("the push held the store lock while it waited on the remote")
		}
	})
}

// A pull with local commits the remote lacks rebases them onto the remote's, under the lock.
func TestGitSync_PullRebasesLocalCommitsOntoTheRemote(t *testing.T) {
	requireGit(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote := setupLocalBare(t)
	dirA, gA := newSyncedStore(t, remote, "base.md")
	dirB := t.TempDir()
	gB := NewGitSync(dirB)
	if err := gB.Init(remote); err != nil {
		t.Fatalf("B init: %v", err)
	}
	setGitUser(t, dirB)

	writeNote(t, dirA, "from-a.md")
	if err := gA.Push("from a"); err != nil {
		t.Fatalf("A push: %v", err)
	}
	writeNote(t, dirB, "from-b.md")
	for _, args := range [][]string{{"add", "notes/"}, {"commit", "-m", "from b, not pushed"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dirB
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	probe := &lockProbe{path: filepath.Join(dirB, "notes", "ops", "from-a.md")}
	gB.LockWrites = probe.lock
	if err := gB.Pull(); err != nil {
		t.Fatalf("B pull: %v", err)
	}
	probe.check(t, "a diverged pull", 1)
	if !fileExists(filepath.Join(dirB, "notes", "ops", "from-b.md")) {
		t.Error("the local note is gone after the rebase")
	}
}

// A push adds and commits the store's files while it holds the store lock, so that no claudemem
// writer, a move above all, changes them in between; the push to the remote runs without it.
func TestGitSync_PushCommitsUnderTheLock(t *testing.T) {
	requireGit(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote := setupLocalBare(t)
	dir, g := newSyncedStore(t, remote, "seed.md")
	writeNote(t, dir, "new.md")
	head := func() string {
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = dir
		out, _ := cmd.Output()
		return strings.TrimSpace(string(out))
	}
	calls := 0
	var atLock, atUnlock string
	g.LockWrites = func() (func(), error) {
		calls++
		atLock = head()
		return func() { atUnlock = head() }, nil
	}
	if err := g.Push("new"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || atLock == atUnlock || atUnlock != head() {
		t.Errorf("the push took the lock %d times; HEAD when locked %s, when released %s, now %s: want the commit made under one lock", calls, atLock, atUnlock, head())
	}
}

// A file a move set aside (.<name>.md.moving), which a stopped move can leave, is never committed: new
// stores ignore it, and a push leaves it out also in a store whose .gitignore predates that rule.
func TestGitSync_NeverCommitsAFileAMoveSetAside(t *testing.T) {
	requireGit(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote := setupLocalBare(t)
	for _, oldIgnore := range []bool{false, true} {
		t.Run(fmt.Sprintf("a .gitignore from before the rule: %v", oldIgnore), func(t *testing.T) {
			dir, g := newSyncedStore(t, remote, fmt.Sprintf("seed-%v.md", oldIgnore))
			ignore := filepath.Join(dir, ".gitignore")
			if oldIgnore {
				data, err := os.ReadFile(ignore)
				if err != nil {
					t.Fatal(err)
				}
				var kept []string
				for _, line := range strings.Split(string(data), "\n") {
					if !strings.Contains(line, ".moving") {
						kept = append(kept, line)
					}
				}
				if err := os.WriteFile(ignore, []byte(strings.Join(kept, "\n")), 0644); err != nil {
					t.Fatal(err)
				}
			}
			aside := filepath.Join("notes", "ops", fmt.Sprintf(".runbook-%v.md.moving", oldIgnore))
			if err := os.WriteFile(filepath.Join(dir, aside), []byte("set aside by a stopped move\n"), 0600); err != nil {
				t.Fatal(err)
			}
			writeNote(t, dir, fmt.Sprintf("next-%v.md", oldIgnore))
			if err := g.Push("with a set-aside file"); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("git", "ls-files")
			cmd.Dir = dir
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(out), ".moving") || !strings.Contains(string(out), "next-") {
				t.Errorf("tracked files:\n%s", out)
			}
		})
	}
}
