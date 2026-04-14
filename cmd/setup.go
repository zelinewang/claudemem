package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/config"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive setup: pick an embedding backend and verify it works",
	Long: `Walk through choosing an embedding backend (local Ollama or cloud Gemini),
verify it is reachable, and (optionally) rebuild the vector index.

claudemem never stores API keys in config files. For cloud backends the
wizard reads the key from the appropriate environment variable (e.g.
GEMINI_API_KEY) and records only the env-var NAME in config.json — so
secrets are always at the OS/shell layer, safe for git sync.

Equivalent manual path (for scripts / CI):
  claudemem config set embedding.backend gemini
  claudemem config set embedding.model gemini-embedding-001
  claudemem config set embedding.dimensions 768
  claudemem config set embedding.api_key_env GEMINI_API_KEY
  claudemem reindex --vectors
`,
	RunE: runSetup,
}

func runSetup(cmd *cobra.Command, args []string) error {
	result, err := config.RunSetupWizard(config.WizardOptions{
		In:       os.Stdin,
		Out:      os.Stdout,
		StoreDir: getStoreDir(),
	})
	if err != nil {
		return fmt.Errorf("setup aborted: %w", err)
	}

	if err := config.ApplyBackendToConfig(getStoreDir(), result); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	OutputText("Saved embedding.backend=%s, embedding.model=%s to config.json",
		result.Config.Backend, result.Config.Model)

	if !result.RunReindex {
		OutputText("Run `claudemem reindex --vectors` when ready to rebuild the index.")
		return nil
	}

	// Trigger the existing reindex path. This uses the newly-written config
	// so the active backend is exactly what the user just picked.
	store, err := getFileStore()
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.InitVectorStore(); err != nil {
		return fmt.Errorf("init vector store: %w", err)
	}
	backend := store.VectorBackend()
	count, err := store.ReindexVectors()
	if err != nil {
		return fmt.Errorf("vector reindex failed: %w", err)
	}
	OutputText("Vector index rebuilt: %d documents indexed (backend: %s)", count, backend)
	return nil
}

func init() {
	rootCmd.AddCommand(setupCmd)
}
