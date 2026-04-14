package vectors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GeminiEmbedder uses Google's Gemini embedding API
// (https://generativelanguage.googleapis.com/v1beta/models/...:embedContent).
//
// This is the recommended cloud backend as of April 2026 — gemini-embedding-001
// leads verified public MTEB multilingual (68.32) at $0.15/M tokens (batch
// $0.075/M). The model supports matryoshka truncation via output_dimensionality
// (native 3072, recommended 768 for <5K-note corpora).
//
// We talk to the REST API directly rather than pulling in google.golang.org/genai
// so claudemem keeps its small dependency footprint. The two endpoints
// (:embedContent and :batchEmbedContents) are stable and simple enough that
// the SDK's abstraction would cost more than it saves.
type GeminiEmbedder struct {
	baseURL string
	model   string
	apiKey  string
	dim     int
	client  *http.Client
}

// DefaultGeminiBaseURL is the Google Generative Language API root.
// Can be overridden via BackendConfig.Endpoint (e.g. to point at a proxy).
const DefaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// NewGeminiEmbedder constructs an embedder. apiKey is required; callers
// obtain it from the env var named in embedding.api_key_env (typically
// GEMINI_API_KEY). dim is the matryoshka output size — 0 means native
// (3072 for gemini-embedding-001), but 768 is the recommended default
// for claudemem's corpus size.
func NewGeminiEmbedder(model, apiKey string, dim int) *GeminiEmbedder {
	if model == "" {
		model = "gemini-embedding-001"
	}
	return &GeminiEmbedder{
		baseURL: DefaultGeminiBaseURL,
		model:   model,
		apiKey:  apiKey,
		dim:     dim,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// WithBaseURL overrides the API root (used by tests + proxy setups).
func (g *GeminiEmbedder) WithBaseURL(u string) *GeminiEmbedder {
	g.baseURL = u
	return g
}

// Name identifies this backend in the (backend, model) vector tag.
func (g *GeminiEmbedder) Name() string { return "gemini" }

// Model returns the active Gemini model ID.
func (g *GeminiEmbedder) Model() string { return g.model }

// Dimensions returns the configured (possibly matryoshka-truncated) vector
// length. Falls back to native 3072 for gemini-embedding-001 if dim==0.
func (g *GeminiEmbedder) Dimensions() int {
	if g.dim > 0 {
		return g.dim
	}
	return 3072
}

// Available pings the Gemini API using a lightweight model metadata call.
// Returns ErrBackendUnavailable with a recovery hint on failure — this is
// what the fail-loud CLI layer (P4) uses to tell the user exactly what
// to do (set GEMINI_API_KEY, check network, etc.).
func (g *GeminiEmbedder) Available() error {
	if g.apiKey == "" {
		return &ErrBackendUnavailable{
			Backend: g.Name() + ":" + g.model,
			Cause:   fmt.Errorf("no API key configured"),
			Hint:    "export GEMINI_API_KEY=... (or run `claudemem setup` to re-configure)",
		}
	}
	// Cheapest call: fetch model metadata.
	req, err := http.NewRequest("GET",
		g.baseURL+"/models/"+g.model, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-goog-api-key", g.apiKey)
	resp, err := g.client.Do(req)
	if err != nil {
		return &ErrBackendUnavailable{
			Backend: g.Name() + ":" + g.model,
			Cause:   err,
			Hint:    "check network / proxy, or switch backend via `claudemem setup`",
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		body, _ := io.ReadAll(resp.Body)
		return &ErrBackendUnavailable{
			Backend: g.Name() + ":" + g.model,
			Cause:   fmt.Errorf("auth rejected (HTTP %d): %s", resp.StatusCode, string(body)),
			Hint:    "verify GEMINI_API_KEY is valid; rotate at https://aistudio.google.com/app/apikey",
		}
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return &ErrBackendUnavailable{
			Backend: g.Name() + ":" + g.model,
			Cause:   fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body)),
			Hint:    "model may be unavailable in your region; try a different model or region",
		}
	}
	return nil
}

// Embed calls the :embedContent endpoint for a single text.
func (g *GeminiEmbedder) Embed(text string, t InputType) ([]float32, error) {
	body := geminiEmbedRequest{
		Content:             geminiContent{Parts: []geminiPart{{Text: text}}},
		TaskType:            geminiTaskType(t),
		OutputDimensionality: g.dim, // omitempty — 0 keeps native
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	resp, err := g.post("/models/"+g.model+":embedContent", raw)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini embedContent HTTP %d: %s", resp.StatusCode, string(b))
	}
	var parsed geminiEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if len(parsed.Embedding.Values) == 0 {
		return nil, fmt.Errorf("gemini returned empty embedding")
	}
	return parsed.Embedding.Values, nil
}

// EmbedBatch calls :batchEmbedContents. Google caps batches at 100 requests;
// we chunk internally so callers don't have to care.
func (g *GeminiEmbedder) EmbedBatch(texts []string, t InputType) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	const max = 100 // Gemini batch ceiling
	out := make([][]float32, 0, len(texts))

	for i := 0; i < len(texts); i += max {
		end := i + max
		if end > len(texts) {
			end = len(texts)
		}
		chunk := texts[i:end]

		requests := make([]geminiEmbedRequest, len(chunk))
		for j, text := range chunk {
			requests[j] = geminiEmbedRequest{
				Model:                "models/" + g.model, // batch API needs model on each item
				Content:              geminiContent{Parts: []geminiPart{{Text: text}}},
				TaskType:             geminiTaskType(t),
				OutputDimensionality: g.dim,
			}
		}
		raw, err := json.Marshal(geminiBatchRequest{Requests: requests})
		if err != nil {
			return nil, err
		}
		resp, err := g.post("/models/"+g.model+":batchEmbedContents", raw)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("gemini batchEmbedContents HTTP %d: %s", resp.StatusCode, string(b))
		}
		var parsed geminiBatchResponse
		err = json.NewDecoder(resp.Body).Decode(&parsed)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode batch: %w", err)
		}
		if len(parsed.Embeddings) != len(chunk) {
			return nil, fmt.Errorf("expected %d embeddings, got %d", len(chunk), len(parsed.Embeddings))
		}
		for _, e := range parsed.Embeddings {
			out = append(out, e.Values)
		}
	}
	return out, nil
}

// post is the shared HTTP POST helper with auth header + JSON content type.
func (g *GeminiEmbedder) post(path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest("POST", g.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.apiKey)
	return g.client.Do(req)
}

// geminiTaskType translates our InputType enum to Gemini's task_type string.
// These strings directly affect retrieval quality: using RETRIEVAL_QUERY for
// query text vs RETRIEVAL_DOCUMENT for corpus docs can improve top-k accuracy
// by ~3-5% per Google's own benchmarks.
func geminiTaskType(t InputType) string {
	switch t {
	case InputTypeQuery:
		return "RETRIEVAL_QUERY"
	case InputTypeDocument:
		return "RETRIEVAL_DOCUMENT"
	default:
		return "RETRIEVAL_DOCUMENT"
	}
}

// --- request/response shapes ---

type geminiEmbedRequest struct {
	Model                string        `json:"model,omitempty"` // batch only
	Content              geminiContent `json:"content"`
	TaskType             string        `json:"taskType,omitempty"`
	OutputDimensionality int           `json:"output_dimensionality,omitempty"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiEmbedResponse struct {
	Embedding geminiEmbeddingValues `json:"embedding"`
}

type geminiBatchRequest struct {
	Requests []geminiEmbedRequest `json:"requests"`
}

type geminiBatchResponse struct {
	Embeddings []geminiEmbeddingValues `json:"embeddings"`
}

type geminiEmbeddingValues struct {
	Values []float32 `json:"values"`
}
