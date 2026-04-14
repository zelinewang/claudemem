package vectors

import (
	"errors"
	"fmt"
)

// InputType hints the backend about how an input will be used, so it can pick
// the right embedding optimization. Ollama and TF-IDF ignore this; Voyage and
// Gemini use it to distinguish "this is a document to be indexed" vs
// "this is a query looking up documents" — the two modes train differently
// on those backends and produce better retrieval when called correctly.
type InputType string

const (
	InputTypeDocument InputType = "document"
	InputTypeQuery    InputType = "query"
)

// Embedder produces dense vector embeddings from text.
//
// Design rules (see docs/HYBRID_EMBEDDING_PLAN.md):
//
//  1. No silent fallback. Callers must handle Available() errors explicitly;
//     they MUST NOT degrade to a different backend automatically.
//  2. Available() returns an error with a recovery hint when the backend
//     cannot be reached. The error message is shown to the user verbatim,
//     so it must tell them exactly what to do (e.g. "run: ollama serve").
//  3. InputType is advisory. Backends that ignore it (Ollama, TF-IDF) must
//     accept any value without error.
type Embedder interface {
	// Available returns nil when the backend is healthy. On failure returns
	// an ErrBackendUnavailable with a user-visible recovery hint.
	Available() error

	// Embed produces a single embedding. On error, callers may log + skip
	// (write path) or fail-loud (read/search path).
	Embed(text string, t InputType) ([]float32, error)

	// EmbedBatch produces many embeddings in one round-trip. Implementations
	// should respect MaxBatchSize (returned separately, not part of the
	// interface to avoid forcing every backend to advertise a limit).
	EmbedBatch(texts []string, t InputType) ([][]float32, error)

	// Name is the backend identifier ("ollama", "gemini", "voyage", "openai",
	// "tfidf"). Used in the (backend, model) composite key that tags each
	// vector row for mixed-backend cross-machine sync.
	Name() string

	// Model is the model identifier within a backend. For Ollama this is a
	// local tag ("nomic-embed-text", "qwen3-embedding:4b"). For cloud
	// backends it is the vendor's model name. For TF-IDF, the literal "tfidf".
	Model() string

	// Dimensions is the length of vectors produced by this backend/model.
	// Must be stable across calls for a given Embedder instance.
	Dimensions() int
}

// ErrBackendUnavailable signals that the configured backend is not reachable.
// The Hint field carries the user-facing recovery instructions.
type ErrBackendUnavailable struct {
	Backend string // e.g. "ollama:qwen3-embedding:4b"
	Cause   error  // underlying connection / HTTP error
	Hint    string // "run: ollama serve" or "export GEMINI_API_KEY=..."
}

func (e *ErrBackendUnavailable) Error() string {
	return fmt.Sprintf("embedding backend %q unreachable: %v\n  Hint: %s",
		e.Backend, e.Cause, e.Hint)
}

func (e *ErrBackendUnavailable) Unwrap() error {
	return e.Cause
}

// IsBackendUnavailable reports whether err signals an unavailable backend.
// Callers on the search/read path use this to trigger fail-loud behavior.
var ErrNotAvailable = errors.New("backend not available")

func IsBackendUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var eb *ErrBackendUnavailable
	return errors.As(err, &eb) || errors.Is(err, ErrNotAvailable)
}
