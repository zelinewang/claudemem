package sync

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireGit skips the test if git is not on PATH. Keeps CI from failing
// on minimal containers without git installed.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed on this system")
	}
}

// setupLocalBare creates a bare git repo in a temp dir to act as the
// "remote" for the round-trip test. Returns its path.
func setupLocalBare(t *testing.T) string {
	t.Helper()
	bare := t.TempDir()
	cmd := exec.Command("git", "init", "--bare", "-b", "main", bare)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create bare: %v\n%s", err, string(out))
	}
	return bare
}

// setGitUser configures a temp user so commits work without global
// git config. Required in CI and fresh containers.
func setGitUser(t *testing.T, dir string) {
	t.Helper()
	for _, cmd := range [][]string{
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		c := exec.Command("git", cmd...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", cmd, err, string(out))
		}
	}
}

// TestGitSync_InitAndPush_RoundTrip exercises the full flow:
//  1. Machine A `claudemem sync init` + seeds a note + push
//  2. Machine B init against same bare repo + pull
//  3. The note from Machine A is present on Machine B
//
// This is the smoke test for the whole cross-machine sync design.
func TestGitSync_InitAndPush_RoundTrip(t *testing.T) {
	requireGit(t)
	remote := setupLocalBare(t)

	// --- Machine A ---
	dirA := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dirA, "notes", "test"), 0755); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(dirA, "notes", "test", "hello.md")
	if err := os.WriteFile(note, []byte("# Hello from A\n"), 0644); err != nil {
		t.Fatal(err)
	}

	gA := NewGitSync(dirA)
	if err := gA.Init(remote); err != nil {
		t.Fatalf("A init: %v", err)
	}
	setGitUser(t, dirA)
	if err := gA.Push("seed from A"); err != nil {
		t.Fatalf("A push: %v", err)
	}

	// --- Machine B ---
	dirB := t.TempDir()
	// Simulate clone via init + remote + pull — matches the command users run
	gB := NewGitSync(dirB)
	cmd := exec.Command("git", "clone", remote, dirB)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone B: %v\n%s", err, string(out))
	}
	setGitUser(t, dirB)

	// Check hello.md arrived
	receivedPath := filepath.Join(dirB, "notes", "test", "hello.md")
	data, err := os.ReadFile(receivedPath)
	if err != nil {
		t.Fatalf("B missing hello.md: %v", err)
	}
	if !strings.Contains(string(data), "Hello from A") {
		t.Errorf("wrong content on B: %s", string(data))
	}

	// Test that .gitignore was pushed too
	gitignore := filepath.Join(dirB, ".gitignore")
	if data, err := os.ReadFile(gitignore); err != nil {
		t.Errorf(".gitignore missing on B: %v", err)
	} else if !strings.Contains(string(data), ".index/") {
		t.Errorf(".gitignore lacks .index/ rule: %s", data)
	}

	// Verify B sees the remote URL
	if gB.RemoteURL() == "" {
		t.Error("B's remote URL is empty after clone")
	}
}

// TestGitSync_Init_AlreadyInitialized verifies we don't clobber an
// existing .git directory.
func TestGitSync_Init_AlreadyInitialized(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	exec.Command("git", "-C", dir, "init").Run()
	g := NewGitSync(dir)
	err := g.Init("")
	if err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("expected 'already a git repo' error, got: %v", err)
	}
}

// TestGitSync_GitignoreExcludesStateFiles is a direct assertion on the
// gitignore content because a broken rule here could leak config.json
// (no secrets currently; env-var rule means API keys live OUT of config
// — but config.json may contain per-machine tokens in the future).
func TestGitSync_GitignoreExcludesStateFiles(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	g := NewGitSync(dir)
	if err := g.Init(""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mustExclude := range []string{
		".index/",
		"config.json",
		".sync_auto_pull",
		".sync_auto_push",
		// archive artifacts — never cross machines
		"*.tar.gz",
		"*.tgz",
		"*.zip",
		"*.tar",
		// defensive: legacy self-referential symlinks
		".claudemem",
		// macOS cross-filesystem residue
		"._*",
		".Spotlight-V100",
	} {
		if !strings.Contains(string(data), mustExclude) {
			t.Errorf(".gitignore missing rule for %q", mustExclude)
		}
	}
}

// TestGitSync_Push_HandlesRemoteSwitch pins the fix for a silent-skip bug:
// when a user runs `git remote set-url origin <new>`, the local tracking
// ref `refs/remotes/origin/main` is NOT updated, so localAheadOfRemote()
// returns false against the stale ref even though the new remote is
// completely empty. Pre-fix behaviour: sync push reports success but
// pushes nothing; a fresh clone sees an empty repo.
//
// Fix: Push runs `git fetch --quiet origin` before the skip-check, so the
// tracking refs reflect the real remote state.
func TestGitSync_Push_HandlesRemoteSwitch(t *testing.T) {
	requireGit(t)
	remote1 := setupLocalBare(t)
	remote2 := setupLocalBare(t) // empty — represents the "new" remote

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "notes", "demo"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes", "demo", "x.md"), []byte("# x\n"), 0644); err != nil {
		t.Fatal(err)
	}

	g := NewGitSync(dir)
	if err := g.Init(remote1); err != nil {
		t.Fatalf("init: %v", err)
	}
	setGitUser(t, dir)
	if err := g.Push(""); err != nil {
		t.Fatalf("first push to remote1: %v", err)
	}

	// Simulate the failure-mode: user (or we) switch the remote via
	// `git remote set-url`. Tracking refs for origin/main still point at
	// remote1's commit, while remote2 is completely empty.
	cmd := exec.Command("git", "remote", "set-url", "origin", remote2)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("remote set-url: %v\n%s", err, string(out))
	}

	// No new changes; with the pre-fix localAheadOfRemote heuristic this
	// would silent-skip. The fetch we added forces the tracking ref to
	// catch up (remote2 has no main branch), making the push proceed.
	if err := g.Push(""); err != nil {
		t.Fatalf("push after remote switch: %v", err)
	}

	// Verify remote2 actually received the content via a throwaway clone.
	cloneDir := t.TempDir()
	cmd = exec.Command("git", "clone", remote2, cloneDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone remote2: %v\n%s", err, string(out))
	}
	if _, err := os.Stat(filepath.Join(cloneDir, "notes", "demo", "x.md")); err != nil {
		t.Errorf("remote2 still empty after push (silent skip regressed): %v", err)
	}
}
