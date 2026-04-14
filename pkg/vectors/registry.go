package vectors

import (
	"fmt"
	"strings"
)

// BackendConfig describes a chosen embedding backend in a form suitable
// for building an Embedder. Populated by the setup wizard (P3) or by
// reading flat config keys (embedding.backend, embedding.model, ...).
type BackendConfig struct {
	Backend  string // "ollama" | "gemini" | "voyage" | "openai" | "tfidf"
	Model    string
	Endpoint string // optional URL override
	APIKey   string // plaintext; obtained by the caller from os.Getenv
	Dim      int    // optional matryoshka truncation target (0 = native)
}

// BuildEmbedder materialises an Embedder from a BackendConfig.
// Unknown backends return an error — callers should surface this to the
// user as "run `claudemem setup` to configure an embedding backend."
//
// This is intentionally dumb: it just wires the right constructor. All
// availability / API-key validation is deferred to Embedder.Available()
// (called once after construction in the CLI layer).
func BuildEmbedder(cfg BackendConfig) (Embedder, error) {
	switch strings.ToLower(cfg.Backend) {
	case "", "tfidf":
		return NewTFIDFEmbedder(), nil

	case "ollama":
		emb := NewOllamaEmbedder(cfg.Model)
		if cfg.Endpoint != "" {
			emb.baseURL = cfg.Endpoint
		}
		return emb, nil

	case "gemini":
		model := cfg.Model
		if model == "" {
			model = "gemini-embedding-001"
		}
		emb := NewGeminiEmbedder(model, cfg.APIKey, cfg.Dim)
		if cfg.Endpoint != "" {
			emb.WithBaseURL(cfg.Endpoint)
		}
		return emb, nil
	case "voyage":
		model := cfg.Model
		if model == "" {
			model = "voyage-3.5-lite"
		}
		emb := NewVoyageEmbedder(model, cfg.APIKey, cfg.Dim)
		if cfg.Endpoint != "" {
			emb.WithBaseURL(cfg.Endpoint)
		}
		return emb, nil

	case "openai":
		model := cfg.Model
		if model == "" {
			model = "text-embedding-3-small"
		}
		emb := NewOpenAIEmbedder(model, cfg.APIKey, cfg.Dim)
		if cfg.Endpoint != "" {
			emb.WithBaseURL(cfg.Endpoint)
		}
		return emb, nil

	default:
		return nil, fmt.Errorf("unknown embedding backend %q (known: tfidf, ollama, gemini, voyage, openai)", cfg.Backend)
	}
}
