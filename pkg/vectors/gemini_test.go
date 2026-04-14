package vectors

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newGeminiMockServer returns an httptest server that simulates Gemini's
// embedContent + batchEmbedContents + model metadata endpoints.
// handler receives the request path and raw body, returns (status, body).
func newGeminiMockServer(t *testing.T, handler func(path string, body []byte) (int, string)) (*httptest.Server, *GeminiEmbedder) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assert auth header present on every call — enforces the contract.
		if r.Header.Get("x-goog-api-key") == "" {
			t.Errorf("request to %s missing x-goog-api-key header", r.URL.Path)
			http.Error(w, "no key", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		status, resp := handler(r.URL.Path, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(resp))
	}))
	emb := NewGeminiEmbedder("gemini-embedding-001", "test-key", 768).WithBaseURL(srv.URL)
	return srv, emb
}

func TestGemini_Available_Success(t *testing.T) {
	srv, emb := newGeminiMockServer(t, func(path string, body []byte) (int, string) {
		if !strings.HasSuffix(path, "/models/gemini-embedding-001") {
			return 404, `{"error":{"message":"wrong path"}}`
		}
		return 200, `{"name":"models/gemini-embedding-001"}`
	})
	defer srv.Close()
	if err := emb.Available(); err != nil {
		t.Errorf("Available should return nil, got %v", err)
	}
}

func TestGemini_Available_MissingKey(t *testing.T) {
	emb := NewGeminiEmbedder("gemini-embedding-001", "", 768)
	err := emb.Available()
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	if !IsBackendUnavailable(err) {
		t.Errorf("expected ErrBackendUnavailable, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Errorf("error message should mention GEMINI_API_KEY env var, got: %v", err)
	}
}

func TestGemini_Available_AuthRejected(t *testing.T) {
	srv, emb := newGeminiMockServer(t, func(path string, body []byte) (int, string) {
		return 401, `{"error":{"message":"invalid key"}}`
	})
	defer srv.Close()
	err := emb.Available()
	if !IsBackendUnavailable(err) {
		t.Fatalf("expected ErrBackendUnavailable for 401, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "aistudio.google.com") {
		t.Errorf("401 error should include key-rotation URL, got: %v", err)
	}
}

func TestGemini_Embed_BuildsCorrectRequest(t *testing.T) {
	var seenPath, seenBody string
	srv, emb := newGeminiMockServer(t, func(path string, body []byte) (int, string) {
		seenPath = path
		seenBody = string(body)
		return 200, `{"embedding":{"values":[0.1,0.2,0.3]}}`
	})
	defer srv.Close()

	vec, err := emb.Embed("what is the meaning of life?", InputTypeDocument)
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(vec) != 3 || vec[0] != 0.1 || vec[1] != 0.2 || vec[2] != 0.3 {
		t.Errorf("unexpected vector: %v", vec)
	}
	// Verify request shape
	if !strings.HasSuffix(seenPath, "/models/gemini-embedding-001:embedContent") {
		t.Errorf("wrong path: %s", seenPath)
	}
	var req map[string]interface{}
	if err := json.Unmarshal([]byte(seenBody), &req); err != nil {
		t.Fatalf("could not parse sent body: %v", err)
	}
	if req["taskType"] != "RETRIEVAL_DOCUMENT" {
		t.Errorf("expected taskType=RETRIEVAL_DOCUMENT, got %v", req["taskType"])
	}
	if dim, _ := req["output_dimensionality"].(float64); int(dim) != 768 {
		t.Errorf("expected output_dimensionality=768, got %v", req["output_dimensionality"])
	}
	content, _ := req["content"].(map[string]interface{})
	parts, _ := content["parts"].([]interface{})
	firstPart, _ := parts[0].(map[string]interface{})
	if firstPart["text"] != "what is the meaning of life?" {
		t.Errorf("wrong text in request: %v", firstPart["text"])
	}
}

func TestGemini_Embed_InputTypeQueryMappsCorrectly(t *testing.T) {
	var seenBody string
	srv, emb := newGeminiMockServer(t, func(path string, body []byte) (int, string) {
		seenBody = string(body)
		return 200, `{"embedding":{"values":[1.0]}}`
	})
	defer srv.Close()

	_, _ = emb.Embed("a query", InputTypeQuery)
	if !strings.Contains(seenBody, `"taskType":"RETRIEVAL_QUERY"`) {
		t.Errorf("InputTypeQuery did not produce RETRIEVAL_QUERY taskType, body: %s", seenBody)
	}
}

func TestGemini_EmbedBatch_ChunksOverOneHundred(t *testing.T) {
	// 250 inputs → should produce 3 batch calls (100, 100, 50)
	batchCallCount := 0
	srv, emb := newGeminiMockServer(t, func(path string, body []byte) (int, string) {
		if !strings.HasSuffix(path, ":batchEmbedContents") {
			return 404, `{}`
		}
		batchCallCount++
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		requests, _ := req["requests"].([]interface{})
		// Echo back one 4-dim vector per input
		embeddings := make([]string, len(requests))
		for i := range requests {
			embeddings[i] = fmt.Sprintf(`{"values":[%d.0, 1.0, 2.0, 3.0]}`, i)
		}
		return 200, `{"embeddings":[` + strings.Join(embeddings, ",") + `]}`
	})
	defer srv.Close()

	texts := make([]string, 250)
	for i := range texts {
		texts[i] = fmt.Sprintf("doc %d", i)
	}
	vectors, err := emb.EmbedBatch(texts, InputTypeDocument)
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}
	if len(vectors) != 250 {
		t.Errorf("expected 250 vectors, got %d", len(vectors))
	}
	if batchCallCount != 3 {
		t.Errorf("expected 3 batch calls for 250 inputs (100/100/50), got %d", batchCallCount)
	}
}

func TestGemini_Embed_ErrorBody(t *testing.T) {
	srv, emb := newGeminiMockServer(t, func(path string, body []byte) (int, string) {
		return 400, `{"error":{"message":"content too long"}}`
	})
	defer srv.Close()
	_, err := emb.Embed("hello", InputTypeDocument)
	if err == nil {
		t.Fatal("expected error on HTTP 400")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should include status code, got: %v", err)
	}
}

func TestGemini_Name_Model_Dimensions(t *testing.T) {
	emb := NewGeminiEmbedder("gemini-embedding-001", "k", 1024)
	if emb.Name() != "gemini" {
		t.Errorf("Name: want gemini, got %s", emb.Name())
	}
	if emb.Model() != "gemini-embedding-001" {
		t.Errorf("Model: want gemini-embedding-001, got %s", emb.Model())
	}
	if emb.Dimensions() != 1024 {
		t.Errorf("Dimensions: want 1024, got %d", emb.Dimensions())
	}
	// Native fallback when dim=0
	emb2 := NewGeminiEmbedder("gemini-embedding-001", "k", 0)
	if emb2.Dimensions() != 3072 {
		t.Errorf("native Dimensions: want 3072, got %d", emb2.Dimensions())
	}
}

func TestGemini_Registry_BuildsFromConfig(t *testing.T) {
	cfg := BackendConfig{
		Backend: "gemini",
		Model:   "gemini-embedding-001",
		APIKey:  "test-key",
		Dim:     768,
	}
	emb, err := BuildEmbedder(cfg)
	if err != nil {
		t.Fatalf("BuildEmbedder: %v", err)
	}
	if emb.Name() != "gemini" {
		t.Errorf("expected gemini backend, got %s", emb.Name())
	}
	if emb.Dimensions() != 768 {
		t.Errorf("expected 768 dims, got %d", emb.Dimensions())
	}
}
