package vectors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// VoyageEmbedder uses Voyage AI's v1/embeddings endpoint.
//
// Voyage-3.5-lite is the April 2026 budget cloud pick — $0.02/M tokens
// with 200M free tokens for new accounts (effectively free for most
// claudemem users). 1024 native dim, 32K input limit, 128 inputs/batch.
// Supports input_type=document|query (use the right one per-call for
// retrieval quality).
//
// See: https://docs.voyageai.com/reference/embeddings-api
type VoyageEmbedder struct {
	baseURL string
	model   string
	apiKey  string
	dim     int
	client  *http.Client
}

const DefaultVoyageBaseURL = "https://api.voyageai.com/v1"

func NewVoyageEmbedder(model, apiKey string, dim int) *VoyageEmbedder {
	if model == "" {
		model = "voyage-3.5-lite"
	}
	return &VoyageEmbedder{
		baseURL: DefaultVoyageBaseURL,
		model:   model,
		apiKey:  apiKey,
		dim:     dim,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (v *VoyageEmbedder) WithBaseURL(u string) *VoyageEmbedder {
	v.baseURL = u
	return v
}

func (v *VoyageEmbedder) Name() string  { return "voyage" }
func (v *VoyageEmbedder) Model() string { return v.model }
func (v *VoyageEmbedder) Dimensions() int {
	if v.dim > 0 {
		return v.dim
	}
	return 1024 // voyage-3.5-lite native
}

// Available does a LIGHTWEIGHT check: api_key present. We intentionally
// do NOT probe the API here because Voyage has no cheap metadata
// endpoint — a real probe would burn billed tokens on every CLI start.
// If the key is invalid, the first real Embed call surfaces it as an
// ErrBackendUnavailable via the same code path; we trade precision of
// error timing for zero idle cost. Matches the "embedder is not pinged
// at construct time" design comment in store.go.
func (v *VoyageEmbedder) Available() error {
	if v.apiKey == "" {
		return &ErrBackendUnavailable{
			Backend: "voyage:" + v.model,
			Cause:   fmt.Errorf("no API key configured"),
			Hint:    "export VOYAGE_API_KEY=... (or run `claudemem setup`)",
		}
	}
	return nil
}

func (v *VoyageEmbedder) Embed(text string, t InputType) ([]float32, error) {
	vecs, err := v.EmbedBatch([]string{text}, t)
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("voyage returned no embeddings")
	}
	return vecs[0], nil
}

func (v *VoyageEmbedder) EmbedBatch(texts []string, t InputType) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	const max = 128 // Voyage batch limit
	out := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += max {
		end := i + max
		if end > len(texts) {
			end = len(texts)
		}
		chunk := texts[i:end]
		vecs, err := v.embedBatchOne(chunk, t)
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (v *VoyageEmbedder) embedBatchOne(texts []string, t InputType) ([][]float32, error) {
	body := voyageRequest{
		Input:           texts,
		Model:           v.model,
		InputType:       voyageInputType(t),
		OutputDimension: v.dim, // omitempty — 0 keeps native
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", v.baseURL+"/embeddings", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+v.apiKey)
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voyage request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("voyage HTTP %d: %s", resp.StatusCode, string(b))
	}
	var parsed voyageResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode voyage: %w", err)
	}
	// Defensive: size the output by INPUT length, not response length. A
	// partial response (rate-limited, mid-batch failure) can otherwise
	// panic downstream RebuildIndex which does `embeddings[j]` assuming
	// response length == batch length. Matches Gemini's guard.
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("voyage returned %d embeddings for %d inputs (partial response not supported)",
			len(parsed.Data), len(texts))
	}
	out := make([][]float32, len(texts))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("voyage returned out-of-range index %d", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}

func voyageInputType(t InputType) string {
	switch t {
	case InputTypeQuery:
		return "query"
	case InputTypeDocument:
		return "document"
	default:
		return "document"
	}
}

type voyageRequest struct {
	Input           []string `json:"input"`
	Model           string   `json:"model"`
	InputType       string   `json:"input_type,omitempty"`
	OutputDimension int      `json:"output_dimension,omitempty"`
}

type voyageResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}
