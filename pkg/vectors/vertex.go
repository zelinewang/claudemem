package vectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	defaultVertexLocation = "us-central1"
	vertexCloudScope      = "https://www.googleapis.com/auth/cloud-platform"
)

// VertexEmbedder uses Vertex AI publisher models with Application Default
// Credentials. This is the service-account path for Gemini embeddings; it
// does not use Google AI Studio API keys.
type VertexEmbedder struct {
	project     string
	location    string
	model       string
	dim         int
	baseURL     string
	client      *http.Client
	tokenSource oauth2.TokenSource
	tokenMu     sync.Mutex
}

func NewVertexEmbedder(project, location, model string, dim int) *VertexEmbedder {
	if location == "" {
		location = defaultVertexLocation
	}
	if model == "" {
		model = "gemini-embedding-001"
	}
	return &VertexEmbedder{
		project:  project,
		location: location,
		model:    model,
		dim:      dim,
		baseURL:  "https://" + location + "-aiplatform.googleapis.com/v1",
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

func (v *VertexEmbedder) WithBaseURL(u string) *VertexEmbedder {
	v.baseURL = strings.TrimRight(u, "/")
	return v
}

func (v *VertexEmbedder) WithTokenSource(ts oauth2.TokenSource) *VertexEmbedder {
	v.tokenSource = ts
	return v
}

func (v *VertexEmbedder) Name() string { return "vertex" }

func (v *VertexEmbedder) Model() string { return v.model }

func (v *VertexEmbedder) Dimensions() int {
	if v.dim > 0 {
		return v.dim
	}
	return 3072
}

func (v *VertexEmbedder) Available() error {
	_, err := v.Embed("claudemem vertex embedding health check", InputTypeDocument)
	return err
}

func (v *VertexEmbedder) Embed(text string, t InputType) ([]float32, error) {
	vecs, err := v.EmbedBatch([]string{text}, t)
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("vertex returned empty embedding")
	}
	return vecs[0], nil
}

func (v *VertexEmbedder) EmbedBatch(texts []string, t InputType) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if err := v.validateConfig(); err != nil {
		return nil, err
	}

	instances := make([]vertexEmbeddingInstance, len(texts))
	for i, text := range texts {
		instances[i] = vertexEmbeddingInstance{
			Content:  text,
			TaskType: vertexTaskType(t),
		}
	}
	body := vertexPredictRequest{
		Instances: instances,
		Parameters: vertexEmbeddingParameters{
			OutputDimensionality: v.dim,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal vertex request: %w", err)
	}

	resp, err := v.postPredict(raw)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		b, _ := io.ReadAll(resp.Body)
		return nil, &ErrBackendUnavailable{
			Backend: v.Name() + ":" + v.model,
			Cause:   fmt.Errorf("auth rejected (HTTP %d): %s", resp.StatusCode, string(b)),
			Hint:    "verify GOOGLE_APPLICATION_CREDENTIALS points to a Vertex-enabled service account and GOOGLE_CLOUD_PROJECT/GOOGLE_CLOUD_LOCATION are correct",
		}
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("vertex predict HTTP %d: %s", resp.StatusCode, string(b))
	}

	var parsed vertexPredictResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode vertex response: %w", err)
	}
	if len(parsed.Predictions) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(parsed.Predictions))
	}
	out := make([][]float32, len(parsed.Predictions))
	for i, prediction := range parsed.Predictions {
		if len(prediction.Embeddings.Values) == 0 {
			return nil, fmt.Errorf("vertex returned empty embedding at index %d", i)
		}
		out[i] = prediction.Embeddings.Values
	}
	return out, nil
}

func (v *VertexEmbedder) validateConfig() error {
	if v.project == "" {
		return &ErrBackendUnavailable{
			Backend: v.Name() + ":" + v.model,
			Cause:   fmt.Errorf("no Vertex AI project configured"),
			Hint:    "set embedding.project or export GOOGLE_CLOUD_PROJECT",
		}
	}
	if v.location == "" {
		return &ErrBackendUnavailable{
			Backend: v.Name() + ":" + v.model,
			Cause:   fmt.Errorf("no Vertex AI location configured"),
			Hint:    "set embedding.location or export GOOGLE_CLOUD_LOCATION",
		}
	}
	return nil
}

func (v *VertexEmbedder) postPredict(body []byte) (*http.Response, error) {
	token, err := v.token()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", v.predictURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	return v.client.Do(req)
}

func (v *VertexEmbedder) token() (*oauth2.Token, error) {
	v.tokenMu.Lock()
	defer v.tokenMu.Unlock()

	if v.tokenSource == nil {
		creds, err := google.FindDefaultCredentials(context.Background(), vertexCloudScope)
		if err != nil {
			return nil, &ErrBackendUnavailable{
				Backend: v.Name() + ":" + v.model,
				Cause:   err,
				Hint:    "export GOOGLE_APPLICATION_CREDENTIALS=/path/to/service-account.json",
			}
		}
		if v.project == "" && creds.ProjectID != "" {
			v.project = creds.ProjectID
		}
		v.tokenSource = creds.TokenSource
	}
	token, err := v.tokenSource.Token()
	if err != nil {
		return nil, &ErrBackendUnavailable{
			Backend: v.Name() + ":" + v.model,
			Cause:   err,
			Hint:    "verify the service-account JSON in GOOGLE_APPLICATION_CREDENTIALS is valid",
		}
	}
	return token, nil
}

func (v *VertexEmbedder) predictURL() string {
	return v.baseURL + "/projects/" + url.PathEscape(v.project) +
		"/locations/" + url.PathEscape(v.location) +
		"/publishers/google/models/" + url.PathEscape(v.model) + ":predict"
}

func vertexTaskType(t InputType) string {
	switch t {
	case InputTypeQuery:
		return "RETRIEVAL_QUERY"
	case InputTypeDocument:
		return "RETRIEVAL_DOCUMENT"
	default:
		return "RETRIEVAL_DOCUMENT"
	}
}

type vertexPredictRequest struct {
	Instances  []vertexEmbeddingInstance `json:"instances"`
	Parameters vertexEmbeddingParameters `json:"parameters,omitempty"`
}

type vertexEmbeddingInstance struct {
	Content  string `json:"content"`
	TaskType string `json:"task_type,omitempty"`
}

type vertexEmbeddingParameters struct {
	OutputDimensionality int `json:"outputDimensionality,omitempty"`
}

type vertexPredictResponse struct {
	Predictions []vertexPrediction `json:"predictions"`
}

type vertexPrediction struct {
	Embeddings geminiEmbeddingValues `json:"embeddings"`
}
