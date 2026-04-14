package vectors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestVoyage_Embed_RoundTrip verifies request shape + response parsing
// against a mocked Voyage endpoint.
func TestVoyage_Embed_RoundTrip(t *testing.T) {
	var seenBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-voyage-key" {
			t.Errorf("missing auth header; got %q", r.Header.Get("Authorization"))
			http.Error(w, "no auth", 401)
			return
		}
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		seenBody = buf[:n]
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3,0.4],"index":0}]}`))
	}))
	defer srv.Close()

	emb := NewVoyageEmbedder("voyage-3.5-lite", "test-voyage-key", 1024).WithBaseURL(srv.URL)
	vec, err := emb.Embed("hello", InputTypeDocument)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 4 {
		t.Errorf("want 4-dim vector, got %d", len(vec))
	}

	var req map[string]interface{}
	json.Unmarshal(seenBody, &req)
	if req["model"] != "voyage-3.5-lite" {
		t.Errorf("model wrong: %v", req["model"])
	}
	if req["input_type"] != "document" {
		t.Errorf("expected input_type=document, got %v", req["input_type"])
	}
	if dim, _ := req["output_dimension"].(float64); int(dim) != 1024 {
		t.Errorf("expected output_dimension=1024, got %v", req["output_dimension"])
	}
}

// TestVoyage_InputTypeQuery confirms query mode is serialized correctly —
// matters for retrieval quality on Voyage (asymmetric model).
func TestVoyage_InputTypeQuery(t *testing.T) {
	var seenBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		seenBody = buf[:n]
		w.Write([]byte(`{"data":[{"embedding":[0.5],"index":0}]}`))
	}))
	defer srv.Close()

	emb := NewVoyageEmbedder("voyage-3.5-lite", "k", 1024).WithBaseURL(srv.URL)
	_, _ = emb.Embed("search query", InputTypeQuery)

	if !strings.Contains(string(seenBody), `"input_type":"query"`) {
		t.Errorf("expected input_type=query, got body: %s", string(seenBody))
	}
}

// TestVoyage_Batch_RespectsChunking exercises the 128-per-batch ceiling.
// 300 inputs should chunk into 3 calls (128+128+44).
func TestVoyage_Batch_RespectsChunking(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req voyageRequest
		buf := make([]byte, 1<<20)
		n, _ := r.Body.Read(buf)
		json.Unmarshal(buf[:n], &req)
		// Echo back one 2-dim vector per input, with correct indices
		data := make([]map[string]interface{}, len(req.Input))
		for i := range req.Input {
			data[i] = map[string]interface{}{
				"embedding": []float32{float32(i), 0.5},
				"index":     i,
			}
		}
		resp, _ := json.Marshal(map[string]interface{}{"data": data})
		w.Write(resp)
	}))
	defer srv.Close()

	texts := make([]string, 300)
	for i := range texts {
		texts[i] = "text"
	}
	emb := NewVoyageEmbedder("voyage-3.5-lite", "k", 0).WithBaseURL(srv.URL)
	vecs, err := emb.EmbedBatch(texts, InputTypeDocument)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 300 {
		t.Errorf("want 300 vectors, got %d", len(vecs))
	}
	if callCount != 3 {
		t.Errorf("want 3 batch calls (128+128+44), got %d", callCount)
	}
}

// TestOpenAI_Embed_RoundTrip mirrors the Voyage round-trip test.
func TestOpenAI_Embed_RoundTrip(t *testing.T) {
	var seenBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-openai-key" {
			t.Errorf("missing auth header")
			http.Error(w, "no auth", 401)
			return
		}
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		seenBody = buf[:n]
		w.Write([]byte(`{"data":[{"embedding":[0.7,0.8],"index":0}]}`))
	}))
	defer srv.Close()

	emb := NewOpenAIEmbedder("text-embedding-3-small", "test-openai-key", 512).WithBaseURL(srv.URL)
	vec, err := emb.Embed("hello", InputTypeDocument)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 2 {
		t.Errorf("want 2-dim vector, got %d", len(vec))
	}

	var req map[string]interface{}
	json.Unmarshal(seenBody, &req)
	if req["model"] != "text-embedding-3-small" {
		t.Errorf("model wrong: %v", req["model"])
	}
	if dim, _ := req["dimensions"].(float64); int(dim) != 512 {
		t.Errorf("expected dimensions=512, got %v", req["dimensions"])
	}
	// OpenAI ignores input_type — make sure we don't send it
	if _, hasIT := req["input_type"]; hasIT {
		t.Errorf("openai request should not have input_type field")
	}
}

// TestRegistry_AllCloudBackends confirms the factory can build each
// cloud provider from config. Uses test keys (no real API calls).
func TestRegistry_AllCloudBackends(t *testing.T) {
	cases := []struct {
		backend string
		model   string
		wantErr bool
	}{
		{"gemini", "gemini-embedding-001", false},
		{"voyage", "voyage-3.5-lite", false},
		{"openai", "text-embedding-3-small", false},
		{"bogus", "x", true},
	}
	for _, tc := range cases {
		emb, err := BuildEmbedder(BackendConfig{
			Backend: tc.backend,
			Model:   tc.model,
			APIKey:  "test-key",
			Dim:     768,
		})
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected error", tc.backend)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.backend, err)
			continue
		}
		if emb.Name() != tc.backend {
			t.Errorf("%s: got name %q", tc.backend, emb.Name())
		}
		if emb.Model() != tc.model {
			t.Errorf("%s: got model %q", tc.backend, emb.Model())
		}
	}
}
