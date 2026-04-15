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
