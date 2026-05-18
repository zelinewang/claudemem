package vectors

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func newVertexMockServer(t *testing.T, handler func(path string, body []byte) (int, string)) (*httptest.Server, *VertexEmbedder) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("request to %s missing bearer token, got %q", r.URL.Path, r.Header.Get("Authorization"))
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		status, resp := handler(r.URL.Path, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(resp))
	}))
	emb := NewVertexEmbedder("test-project", "us-central1", "gemini-embedding-001", 768).
		WithBaseURL(srv.URL).
		WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"}))
	return srv, emb
}

func TestVertex_Embed_BuildsPredictRequest(t *testing.T) {
	var seenPath, seenBody string
	srv, emb := newVertexMockServer(t, func(path string, body []byte) (int, string) {
		seenPath = path
		seenBody = string(body)
		return 200, `{"predictions":[{"embeddings":{"values":[0.1,0.2,0.3]}}]}`
	})
	defer srv.Close()

	vec, err := emb.Embed("what is vertex?", InputTypeDocument)
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(vec) != 3 || vec[0] != 0.1 || vec[1] != 0.2 || vec[2] != 0.3 {
		t.Errorf("unexpected vector: %v", vec)
	}
	if !strings.HasSuffix(seenPath, "/projects/test-project/locations/us-central1/publishers/google/models/gemini-embedding-001:predict") {
		t.Errorf("wrong path: %s", seenPath)
	}
	var req map[string]interface{}
	if err := json.Unmarshal([]byte(seenBody), &req); err != nil {
		t.Fatalf("could not parse sent body: %v", err)
	}
	instances, _ := req["instances"].([]interface{})
	first, _ := instances[0].(map[string]interface{})
	if first["content"] != "what is vertex?" {
		t.Errorf("wrong content: %v", first["content"])
	}
	if first["task_type"] != "RETRIEVAL_DOCUMENT" {
		t.Errorf("expected task_type=RETRIEVAL_DOCUMENT, got %v", first["task_type"])
	}
	parameters, _ := req["parameters"].(map[string]interface{})
	if dim, _ := parameters["outputDimensionality"].(float64); int(dim) != 768 {
		t.Errorf("expected outputDimensionality=768, got %v", parameters["outputDimensionality"])
	}
}

func TestVertex_EmbedBatch_QueryTaskType(t *testing.T) {
	var seenBody string
	srv, emb := newVertexMockServer(t, func(path string, body []byte) (int, string) {
		seenBody = string(body)
		return 200, `{"predictions":[{"embeddings":{"values":[1.0]}},{"embeddings":{"values":[2.0]}}]}`
	})
	defer srv.Close()

	vecs, err := emb.EmbedBatch([]string{"one", "two"}, InputTypeQuery)
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs))
	}
	if strings.Count(seenBody, `"task_type":"RETRIEVAL_QUERY"`) != 2 {
		t.Errorf("InputTypeQuery did not produce query task type for every instance, body: %s", seenBody)
	}
}

func TestVertex_AuthRejectedIsBackendUnavailable(t *testing.T) {
	srv, emb := newVertexMockServer(t, func(path string, body []byte) (int, string) {
		return 403, `{"error":{"message":"permission denied"}}`
	})
	defer srv.Close()

	_, err := emb.Embed("hello", InputTypeDocument)
	if !IsBackendUnavailable(err) {
		t.Fatalf("expected ErrBackendUnavailable, got %T: %v", err, err)
	}
	if strings.Contains(err.Error(), "aistudio.google.com") {
		t.Fatalf("vertex auth error must not point users to AI Studio: %v", err)
	}
}

func TestVertex_MissingProject(t *testing.T) {
	emb := NewVertexEmbedder("", "us-central1", "gemini-embedding-001", 768)
	_, err := emb.Embed("hello", InputTypeDocument)
	if !IsBackendUnavailable(err) {
		t.Fatalf("expected ErrBackendUnavailable, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "GOOGLE_CLOUD_PROJECT") {
		t.Errorf("missing project hint should mention GOOGLE_CLOUD_PROJECT, got: %v", err)
	}
}

func TestVertex_Registry_BuildsFromConfig(t *testing.T) {
	cfg := BackendConfig{
		Backend:  "vertex",
		Model:    "gemini-embedding-001",
		Dim:      768,
		Project:  "test-project",
		Location: "us-central1",
	}
	emb, err := BuildEmbedder(cfg)
	if err != nil {
		t.Fatalf("BuildEmbedder: %v", err)
	}
	if emb.Name() != "vertex" {
		t.Errorf("expected vertex backend, got %s", emb.Name())
	}
	if emb.Dimensions() != 768 {
		t.Errorf("expected 768 dims, got %d", emb.Dimensions())
	}
}

func TestVertex_EmbedBatch_LengthMismatch(t *testing.T) {
	srv, emb := newVertexMockServer(t, func(path string, body []byte) (int, string) {
		return 200, `{"predictions":[{"embeddings":{"values":[1.0]}}]}`
	})
	defer srv.Close()

	_, err := emb.EmbedBatch([]string{"one", "two"}, InputTypeDocument)
	if err == nil {
		t.Fatal("expected length mismatch error")
	}
	if !strings.Contains(fmt.Sprint(err), "expected 2 embeddings") {
		t.Errorf("unexpected error: %v", err)
	}
}
