package vectors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAIEmbedder uses OpenAI's v1/embeddings endpoint.
//
// text-embedding-3-small is the budget English-focused option
// ($0.02/M tokens, 1536 native → 512 truncatable via dimensions).
// Weaker Chinese retrieval than Gemini/Voyage — use only if your
// corpus is English-dominant and you're already paying for OpenAI
// for other reasons.
//
// No input_type distinction — OpenAI doesn't differentiate
// document/query at the API level. The parameter is accepted
// for interface compliance and ignored.
//
// See: https://platform.openai.com/docs/guides/embeddings
type OpenAIEmbedder struct {
	baseURL string
	model   string
	apiKey  string
	dim     int
	client  *http.Client
}

const DefaultOpenAIBaseURL = "https://api.openai.com/v1"

func NewOpenAIEmbedder(model, apiKey string, dim int) *OpenAIEmbedder {
	if model == "" {
		model = "text-embedding-3-small"
	}
	return &OpenAIEmbedder{
		baseURL: DefaultOpenAIBaseURL,
		model:   model,
		apiKey:  apiKey,
		dim:     dim,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (o *OpenAIEmbedder) WithBaseURL(u string) *OpenAIEmbedder {
	o.baseURL = u
	return o
}

func (o *OpenAIEmbedder) Name() string  { return "openai" }
func (o *OpenAIEmbedder) Model() string { return o.model }
func (o *OpenAIEmbedder) Dimensions() int {
	if o.dim > 0 {
		return o.dim
	}
	// Native sizes: 3-small 1536, 3-large 3072. Callers should set dim
	// explicitly for anything other than 3-small.
	return 1536
}

func (o *OpenAIEmbedder) Available() error {
	if o.apiKey == "" {
		return &ErrBackendUnavailable{
			Backend: "openai:" + o.model,
			Cause:   fmt.Errorf("no API key configured"),
			Hint:    "export OPENAI_API_KEY=... (or run `claudemem setup`)",
		}
	}
	// List-models is cheap and auth-scoped.
	req, _ := http.NewRequest("GET", o.baseURL+"/models/"+o.model, nil)
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	resp, err := o.client.Do(req)
	if err != nil {
		return &ErrBackendUnavailable{
			Backend: "openai:" + o.model,
			Cause:   err,
			Hint:    "check network / proxy, or switch backend via `claudemem setup`",
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return &ErrBackendUnavailable{
			Backend: "openai:" + o.model,
			Cause:   fmt.Errorf("auth rejected (HTTP %d)", resp.StatusCode),
			Hint:    "rotate key at https://platform.openai.com/api-keys",
		}
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return &ErrBackendUnavailable{
			Backend: "openai:" + o.model,
			Cause:   fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b)),
			Hint:    "check model name and API status at https://status.openai.com",
		}
	}
	return nil
}

func (o *OpenAIEmbedder) Embed(text string, t InputType) ([]float32, error) {
	vecs, err := o.EmbedBatch([]string{text}, t)
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("openai returned no embeddings")
	}
	return vecs[0], nil
}

func (o *OpenAIEmbedder) EmbedBatch(texts []string, _ InputType) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	const max = 2048 // OpenAI batch limit
	out := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += max {
		end := i + max
		if end > len(texts) {
			end = len(texts)
		}
		chunk := texts[i:end]
		vecs, err := o.embedBatchOne(chunk)
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (o *OpenAIEmbedder) embedBatchOne(texts []string) ([][]float32, error) {
	body := openaiRequest{
		Input:      texts,
		Model:      o.model,
		Dimensions: o.dim,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", o.baseURL+"/embeddings", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai HTTP %d: %s", resp.StatusCode, string(b))
	}
	var parsed openaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode openai: %w", err)
	}
	out := make([][]float32, len(parsed.Data))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("openai returned out-of-range index %d", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	return out, nil
}

type openaiRequest struct {
	Input      []string `json:"input"`
	Model      string   `json:"model"`
	Dimensions int      `json:"dimensions,omitempty"`
}

type openaiResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}
