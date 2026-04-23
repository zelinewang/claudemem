// Package sync implements markdown-only git sync for claudemem memory
// across machines. Only notes/ + sessions/ ship over the wire; SQLite
// index and config.json stay per-machine. Each machine rebuilds its own
// FTS and embeds missing vectors under its configured backend on pull.
//
// This decouples memory content (shared) from search infrastructure
// (per-machine). Two machines can run different embedding backends
// against the same corpus and still share notes.
package sync

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitSync wraps the git CLI at a single claudemem store directory.
// We shell out rather than depend on libgit2 because (a) git CLI is
// universally available on dev machines, (b) claudemem's small Go
// binary footprint stays small, (c) users already understand git
// error messages when things go wrong.
type GitSync struct {
	Dir string // ~/.claudemem
}

// NewGitSync returns a GitSync rooted at dir.
func NewGitSync(dir string) *GitSync { return &GitSync{Dir: dir} }

// IsInitialized reports whether Dir is already a git repo.
func (g *GitSync) IsInitialized() bool {
	_, err := os.Stat(filepath.Join(g.Dir, ".git"))
	return err == nil
}

// Init creates .git, writes .gitignore, and sets the remote. Does NOT
// create an initial commit — the caller should add markdown + commit
// explicitly so the user sees what is about to be pushed.
func (g *GitSync) Init(remoteURL string) error {
	// First-run safety: the ~/.claudemem dir may not exist yet on a fresh
	// install. `git init` refuses to create its parent; fail here with a
	// useful message rather than the cryptic git error.
	if err := os.MkdirAll(g.Dir, 0700); err != nil {
		return fmt.Errorf("create %s: %w", g.Dir, err)
	}
	if g.IsInitialized() {
		return fmt.Errorf("%s is already a git repo; use `sync status` to inspect", g.Dir)
	}
	// `-b main` works on git 2.28+; symbolic-ref fallback handles older git
	// that doesn't recognize -b.
	if err := g.git("init", "-b", "main"); err != nil {
		if err2 := g.git("init"); err2 != nil {
			return fmt.Errorf("git init: %w", err2)
		}
		_ = g.git("symbolic-ref", "HEAD", "refs/heads/main")
	}

	if err := g.writeGitignore(); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}

	if remoteURL != "" {
		if err := g.git("remote", "add", "origin", remoteURL); err != nil {
			return fmt.Errorf("add remote: %w", err)
		}
	}
	return nil
}

// writeGitignore installs the canonical ignore rules. Keeps ~/.claudemem
// safe to push even if the user accidentally runs commands that scribble
// state into unexpected paths.
//
// What is gitignored:
//   .index/              — SQLite DB; per-machine, rebuildable from markdown
//   config.json          — per-machine embedding backend choice
//   .sync_auto_pull      — per-machine auto-sync toggle
//   .sync_auto_push      — per-machine auto-sync toggle
//   .bak, .tmp, .swp     — editor temp files
func (g *GitSync) writeGitignore() error {
	content := `# claudemem memory sync — per-machine files excluded
.index/
config.json
.sync_auto_pull
.sync_auto_push

# defensive: self-referential or stray symlinks inside the memory dir
# (e.g. legacy .claudemem -> /home/*/.claudemem left by older installs)
.claudemem

# backup archives — per-machine snapshots, never sync
*.tar
*.tar.gz
*.tgz
*.zip

# editor / OS temp files
*.bak
*.tmp
*.swp
.DS_Store

# macOS cross-filesystem residue (AppleDouble extended-attribute files)
# Appear as ._* next to every real file when memory was touched via SMB/SFTP
# from a Mac. Worthless on non-Mac machines, no cross-machine value.
._*
.Spotlight-V100
.Trashes
.fseventsd
`
	return os.WriteFile(filepath.Join(g.Dir, ".gitignore"), []byte(content), 0644)
}

// Status returns the output of `git status --short`.
func (g *GitSync) Status() (string, error) {
	return g.gitOutput("status", "--short")
}

// RemoteURL returns the origin push URL, or "" if no remote configured.
func (g *GitSync) RemoteURL() string {
	out, err := g.gitOutput("remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Push stages every tracked + new markdown file under notes/ and sessions/
// plus MEMORY.md if present, commits with a descriptive message, and
// pushes to origin. Silent on no-op (no changes to commit).
func (g *GitSync) Push(message string) error {
	if !g.IsInitialized() {
		return fmt.Errorf("not a git repo; run `claudemem sync init <remote>` first")
	}
	// Stage ONLY the content paths that actually exist. git add with
	// multiple pathspecs treats a missing one as a hard error which
	// prevents the OTHER paths from being staged — so iterate per-path.
	for _, p := range []string{"notes/", "sessions/", "MEMORY.md", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(g.Dir, p)); err != nil {
			continue // not present — skip
		}
		if err := g.git("add", p); err != nil {
			return fmt.Errorf("git add %s: %w", p, err)
		}
	}

	// Check if anything is actually staged. `git diff --cached --quiet`
	// exits 0 when nothing is staged, 1 when there are staged changes.
	// We use that exit code rather than trying to parse the generic
	// "exit status 1" we get back from the commit command.
	hasChanges := g.hasStagedChanges()
	if hasChanges {
		msg := message
		if msg == "" {
			msg = "memory: sync from " + hostname()
		}
		if err := g.git("commit", "-m", msg); err != nil {
			return fmt.Errorf("git commit: %w", err)
		}
	}

	if g.RemoteURL() == "" {
		return fmt.Errorf("no remote configured; run `git remote add origin <url>` inside %s", g.Dir)
	}

	// Refresh remote tracking refs before deciding whether to skip.
	// Rationale: `git remote set-url` doesn't update `refs/remotes/origin/*`,
	// so a freshly-re-pointed remote (e.g. file:// → github.com) can appear
	// "up to date" via stale tracking — causing Push to silent-skip while
	// the real remote is empty. `--prune` is load-bearing: without it,
	// an old tracking ref whose branch no longer exists on the new remote
	// stays around and keeps misleading localAheadOfRemote.
	// Failure (offline / auth) is non-fatal: we fall through to the push
	// below, which will surface a real error to the user.
	_ = g.git("fetch", "--prune", "--quiet", "origin")

	// If nothing was committed AND local is already up-to-date with remote,
	// skip the push — avoids a noisy "Everything up-to-date" on every hook.
	if !hasChanges {
		if g.localAheadOfRemote() {
			// local has unpushed commits from a previous session; push them
		} else {
			return nil // nothing to do
		}
	}

	if err := g.git("push", "-u", "origin", "HEAD"); err != nil {
		// Push failed — likely because remote has new commits from another
		// machine. Try pull --rebase to replay our local commits on top,
		// then push again. This auto-resolves the most common conflict:
		// two machines each adding different files.
		if rebaseErr := g.git("pull", "--rebase", "--quiet", "origin", "HEAD"); rebaseErr != nil {
			return fmt.Errorf("git push failed and auto-rebase failed: push=%w, rebase=%v — resolve manually in %s", err, rebaseErr, g.Dir)
		}
		if err2 := g.git("push", "-u", "origin", "HEAD"); err2 != nil {
			return fmt.Errorf("git push after rebase: %w", err2)
		}
	}
	return nil
}

// hasStagedChanges reports whether `git commit` would produce a new
// commit. Uses `diff --cached --quiet` whose exit code is 0=clean, 1=dirty.
func (g *GitSync) hasStagedChanges() bool {
	cmd := exec.Command("git", "diff", "--cached", "--quiet")
	cmd.Dir = g.Dir
	err := cmd.Run()
	// Run() returns *ExitError with ExitCode()==1 when there ARE changes.
	if err == nil {
		return false
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return true
	}
	// Any other error (e.g., git not found) — treat as "don't commit".
	return false
}

// localAheadOfRemote reports whether HEAD has commits not yet on origin.
// Used by Push to decide whether to skip an up-to-date no-op push.
//
// Semantics on error: returns TRUE (assume "ahead"). Rationale — the
// only errors rev-list produces here are "upstream ref doesn't resolve"
// (new remote, branch renamed, tracking ref pruned). In all those cases
// the correct action is to push, not skip — the push itself will set up
// tracking via `-u`. False-positive cost is one noisy "Everything
// up-to-date" line; false-negative cost is a silent-skip bug where the
// user thinks they've synced and hasn't.
func (g *GitSync) localAheadOfRemote() bool {
	cmd := exec.Command("git", "rev-list", "--count", "@{u}..HEAD")
	cmd.Dir = g.Dir
	out, err := cmd.Output()
	if err != nil {
		return true // err on the side of pushing — see comment above
	}
	return strings.TrimSpace(string(out)) != "0"
}

// Pull runs `git pull --ff-only` to avoid accidental merge commits
// inside the memory repo. If fast-forward fails, returns an error
// suggesting manual resolution — we don't automate merge of markdown
// because timestamps + session_id make auto-merges unsafe.
func (g *GitSync) Pull() error {
	if !g.IsInitialized() {
		return fmt.Errorf("not a git repo; run `claudemem sync init <remote>` first")
	}
	if err := g.git("pull", "--ff-only", "origin", "HEAD"); err != nil {
		// ff-only failed — local has commits that remote doesn't (diverged).
		// Try rebase to replay local commits on top of remote. This safely
		// handles the common case of two machines adding different files.
		if rebaseErr := g.git("pull", "--rebase", "origin", "HEAD"); rebaseErr != nil {
			return fmt.Errorf("git pull --ff-only and --rebase both failed: %w — resolve manually in %s", rebaseErr, g.Dir)
		}
	}
	return nil
}

// --- internal helpers ---

// git runs `git <args>` in g.Dir and surfaces stderr+stdout in the error
// message when the command fails. Hook contexts (SessionEnd) often
// suppress stderr, so wrapping the full output into the returned error
// is the only way users see "fatal: refusing to merge unrelated
// histories" or similar actionable git messages.
func (g *GitSync) git(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Trim trailing newlines from git's output for cleaner error format.
		msg := strings.TrimRight(string(out), "\n")
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w\n%s", err, msg)
	}
	return nil
}

func (g *GitSync) gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.Dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// (gitMaybe removed — Push now per-path stats each target before adding,
// which makes "missing file" tolerance unnecessary.)

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}
