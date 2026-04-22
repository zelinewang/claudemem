package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/sync"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Cross-machine memory sync via git (markdown only)",
	Long: `Sync notes + sessions across machines via a private git repo.

Only markdown files travel over the wire — SQLite index and config.json
stay per-machine. Each machine rebuilds its own FTS and embeds missing
vectors under its own configured backend on pull. Two machines can
run different embedding backends against the same corpus.

Typical flow:
  claudemem sync init git@github.com:YOU/claudemem-memory.git  # once
  claudemem sync push                                           # after work
  # on another machine:
  claudemem sync pull                                           # receive + reindex

Auto-sync (opt-in, per-machine) via:
  touch ~/.claudemem/.sync_auto_pull   # auto-pull on SessionStart
  touch ~/.claudemem/.sync_auto_push   # auto-push on SessionEnd
See docs/HOOK_INTEGRATION.md for the hook additions.`,
}

var syncInitCmd = &cobra.Command{
	Use:   "init <remote-url>",
	Short: "Initialize git repo at ~/.claudemem and set remote",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		g := sync.NewGitSync(getStoreDir())
		remote := ""
		if len(args) == 1 {
			remote = args[0]
		}
		if err := g.Init(remote); err != nil {
			return err
		}
		OutputText("✓ Initialized %s as git repo", getStoreDir())
		if remote != "" {
			OutputText("  Remote: %s", remote)
		} else {
			OutputText("  No remote set; add later with: cd %s && git remote add origin <url>", getStoreDir())
		}
		OutputText("\nNext:")
		OutputText("  claudemem sync push    # commit + push notes/sessions to remote")
		OutputText("  claudemem sync pull    # (on another machine) receive updates")
		return nil
	},
}

var syncPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Commit new/changed markdown and push to remote",
	RunE: func(cmd *cobra.Command, args []string) error {
		g := sync.NewGitSync(getStoreDir())
		msg, _ := cmd.Flags().GetString("message")
		quiet, _ := cmd.Flags().GetBool("quiet")
		if err := g.Push(msg); err != nil {
			if !quiet {
				return err
			}
			// Quiet mode for hook invocations: log to stderr, exit 0
			fmt.Fprintf(os.Stderr, "claudemem sync push (quiet): %v\n", err)
			return nil
		}
		if !quiet {
			OutputText("✓ pushed memory to %s", g.RemoteURL())
		}
		return nil
	},
}

var syncPullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Pull markdown from remote; rebuild local FTS + embed missing vectors",
	RunE: func(cmd *cobra.Command, args []string) error {
		quiet, _ := cmd.Flags().GetBool("quiet")
		g := sync.NewGitSync(getStoreDir())
		if err := g.Pull(); err != nil {
			if !quiet {
				return err
			}
			fmt.Fprintf(os.Stderr, "claudemem sync pull (quiet): %v\n", err)
			return nil
		}

		// Reconcile: rebuild FTS from markdown (cheap), then embed any docs
		// missing a vector for the active backend.
		store, err := getFileStore()
		if err != nil {
			return err
		}
		defer store.Close()

		ftsCount, err := store.Reindex()
		if err != nil {
			return fmt.Errorf("FTS rebuild post-pull: %w", err)
		}

		// Only touch vectors if semantic search is enabled
		vectorsAdded := 0
		_ = store.InitVectorStore()
		if store.HasVectorStore() {
			// Full reindex is simpler than diff-based partial embed for v1;
			// RebuildIndex already scopes the wipe to the active
			// (backend, model) so cross-machine rows are preserved.
			n, err := store.ReindexVectors()
			if err != nil {
				return fmt.Errorf("vector reindex post-pull: %w", err)
			}
			vectorsAdded = n
		}

		if !quiet {
			OutputText("✓ pulled memory; reconciled %d FTS entries + %d vectors (backend: %s)",
				ftsCount, vectorsAdded, store.VectorBackend())
			if !store.HasVectorStore() {
				OutputText("")
				OutputText("⚠ No embedding backend configured. Search uses basic TF-IDF.")
				OutputText("  Run `claudemem setup` to pick a backend (Gemini, Ollama, etc.)")
			}
		}
		return nil
	},
}

var syncStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show git status + health of the local memory store",
	RunE: func(cmd *cobra.Command, args []string) error {
		g := sync.NewGitSync(getStoreDir())
		if !g.IsInitialized() {
			OutputText("not initialized — run `claudemem sync init <remote-url>` to start")
			return nil
		}
		out, err := g.Status()
		if err != nil {
			return err
		}
		remote := g.RemoteURL()
		if remote == "" {
			remote = "(none)"
		}
		OutputText("Remote:  %s", remote)
		OutputText("Storage: %s", getStoreDir())
		if out == "" {
			OutputText("Status:  clean (no uncommitted changes)")
		} else {
			OutputText("Status:\n%s", out)
		}
		return nil
	},
}

func init() {
	syncPushCmd.Flags().StringP("message", "m", "", "commit message (default: 'memory: sync from <hostname>')")
	syncPushCmd.Flags().Bool("quiet", false, "suppress output except errors (for hook invocations)")
	syncPullCmd.Flags().Bool("quiet", false, "suppress output except errors (for hook invocations)")

	syncCmd.AddCommand(syncInitCmd)
	syncCmd.AddCommand(syncPushCmd)
	syncCmd.AddCommand(syncPullCmd)
	syncCmd.AddCommand(syncStatusCmd)
	rootCmd.AddCommand(syncCmd)
}
