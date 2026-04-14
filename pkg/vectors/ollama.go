package vectors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OllamaEmbedder generates embeddings via a local Ollama instance.
// Connects to localhost only — no external network calls.
type OllamaEmbedder struct {
	baseURL string
	model   string
	client  *http.Client
	dims    int // cached embedding dimensions (0 until first call)
}

type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedBatchRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Error      string      `json:"error,omitempty"`
}

// NewOllamaEmbedder creates an embedder pointing at a local Ollama instance.
// Default model: nomic-embed-text (768 dims, fast, good quality).
func NewOllamaEmbedder(model string) *OllamaEmbedder {
	if model == "" {
		model = "nomic-embed-text"
	}
	return &OllamaEmbedder{
		baseURL: "http://localhost:11434",
		model:   model,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Available returns nil if the Ollama daemon is reachable. On failure it
// returns an ErrBackendUnavailable carrying a recovery hint — this is the
// failure signal the search/read path uses to fail loud instead of falling
// back to TF-IDF silently.
func (o *OllamaEmbedder) Available() error {
	resp, err := o.client.Get(o.baseURL + "/api/tags")
	if err != nil {
		return &ErrBackendUnavailable{
			Backend: o.Name() + ":" + o.model,
			Cause:   err,
			Hint:    "start the daemon: `ollama serve` (or switch backend: `claudemem setup`)",
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &ErrBackendUnavailable{
			Backend: o.Name() + ":" + o.model,
			Cause:   fmt.Errorf("ollama /api/tags returned HTTP %d", resp.StatusCode),
			Hint:    "check `ollama serve` logs or run `claudemem setup` to switch backend",
		}
	}
	return nil
}

// Name returns "ollama" — the backend identifier used in the composite
// (backend, model) key that tags each vector row.
func (o *OllamaEmbedder) Name() string { return "ollama" }

// Dimensions returns the vector length produced by this model. Returns 0
// until the first successful Embed call (Ollama does not advertise dims
// up-front, so we cache after observing the first response).
func (o *OllamaEmbedder) Dimensions() int { return o.dims }

// Embed generates an embedding. The InputType hint is accepted for interface
// conformance and ignored — Ollama does not distinguish document vs query
// modes at the API level.
func (o *OllamaEmbedder) Embed(text string, _ InputType) ([]float32, error) {
	reqBody := embedRequest{
		Model: o.model,
		Input: text,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := o.client.Post(o.baseURL+"/api/embed", "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(body))
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Ollama can return 200 with an error field (e.g., context length exceeded)
	if result.Error != "" {
		return nil, fmt.Errorf("ollama error: %s", result.Error)
	}

	if len(result.Embeddings) == 0 || len(result.Embeddings[0]) == 0 {
		return nil, fmt.Errorf("ollama returned empty embeddings")
	}

	// Cache dimensions
	if o.dims == 0 {
		o.dims = len(result.Embeddings[0])
	}

	return result.Embeddings[0], nil
}

// EmbedBatch generates embeddings for multiple texts in one request.
// InputType is accepted for interface conformance and ignored (see Embed).
func (o *OllamaEmbedder) EmbedBatch(texts []string, _ InputType) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody := embedBatchRequest{
		Model: o.model,
		Input: texts,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := o.client.Post(o.baseURL+"/api/embed", "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(body))
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Ollama can return 200 with an error field (e.g., context length exceeded)
	if result.Error != "" {
		return nil, fmt.Errorf("ollama error: %s", result.Error)
	}

	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(result.Embeddings))
	}

	// Cache dimensions
	if o.dims == 0 && len(result.Embeddings) > 0 {
		o.dims = len(result.Embeddings[0])
	}

	return result.Embeddings, nil
}

// Model returns the model name being used.
func (o *OllamaEmbedder) Model() string {
	return o.model
}

// maxEmbedChars is a conservative character limit for nomic-embed-text.
// nomic-embed-text supports 8192 tokens. Empirically tested: markdown/code content
// with UUIDs, file paths, and special chars tokenizes at ~1.0 chars/token (worst case).
// Boundary tested at 7800 OK / 8000 FAIL on real session reports.
// Use 7500 chars for safe margin across all content types.
const maxEmbedChars = 7500

// TruncateForEmbed truncates text to fit within the embedding model's context window.
// Returns the (possibly truncated) text. Truncation is at a word boundary when possible.
func TruncateForEmbed(text string) string {
	if len(text) <= maxEmbedChars {
		return text
	}
	// Find last space before the limit to avoid splitting words
	truncated := text[:maxEmbedChars]
	lastSpace := len(truncated) - 1
	for lastSpace > maxEmbedChars-200 && truncated[lastSpace] != ' ' {
		lastSpace--
	}
	if truncated[lastSpace] == ' ' {
		return truncated[:lastSpace]
	}
	return truncated
}
