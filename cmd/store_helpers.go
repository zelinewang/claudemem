package cmd

import (
	"github.com/zelinewang/claudemem/pkg/config"
	"github.com/zelinewang/claudemem/pkg/storage"
)

// getFileStoreWithVectors returns a FileStore with optional vector store initialized
// if the semantic search feature flag is enabled. This should be used by write paths
// (note add, session save) so that vector indexing happens automatically.
func getFileStoreWithVectors() (*storage.FileStore, error) {
	store, err := getFileStore()
	if err != nil {
		return nil, err
	}

	// Check if semantic search is enabled; if so, initialize vector store
	cfg, cfgErr := config.Load(getStoreDir())
	if cfgErr == nil && cfg.GetString("features.semantic_search") == "true" {
		// Best effort: if vector store init fails, we still return a working store.
		// Vector indexing will silently be skipped (vectorStore remains nil).
		_ = store.InitVectorStore()
	}

	return store, nil
}
