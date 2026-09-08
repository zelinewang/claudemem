package vectors

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"
)

// TestVertex_EmbedBatchSplitsAtInstanceLimit reproduces the 2026-09-08 Mac
// backfill failure: v23 chunking flattens a 50-document batch into one
// EmbedBatch call, and Vertex's text-embedding predict endpoint rejects more
// than 250 instances per request ("batchSize value of 256 but the supported
// range is from 1 (inclusive) to 251 (exclusive)"). The embedder must split
// the call into ≤250-instance requests and return the vectors in input order.
func TestVertex_EmbedBatchSplitsAtInstanceLimit(t *testing.T) {
	var mu sync.Mutex
	var sizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Instances []struct {
				Content string `json:"content"`
			} `json:"instances"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		mu.Lock()
		sizes = append(sizes, len(req.Instances))
		mu.Unlock()
		if len(req.Instances) > 250 {
			http.Error(w, `{"error":{"code":400,"message":"Unable to submit request because it has a batchSize value of `+
				strconv.Itoa(len(req.Instances))+` but the supported range is from 1 (inclusive) to 251 (exclusive).","status":"INVALID_ARGUMENT"}}`, 400)
			return
		}
		// Echo the input index (text "t<N>") as the single embedding value so
		// ordering across split requests is verifiable.
		preds := make([]string, len(req.Instances))
		for i, inst := range req.Instances {
			preds[i] = `{"embeddings":{"values":[` + strings.TrimPrefix(inst.Content, "t") + `]}}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"predictions":[` + strings.Join(preds, ",") + `]}`))
	}))
	defer srv.Close()

	emb := NewVertexEmbedder("test-project", "us-central1", "gemini-embedding-001", 1).
		WithBaseURL(srv.URL).
		WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test"}))

	texts := make([]string, 600)
	for i := range texts {
		texts[i] = "t" + strconv.Itoa(i)
	}
	out, err := emb.EmbedBatch(texts, InputTypeDocument)
	if err != nil {
		t.Fatalf("EmbedBatch(600 texts): %v", err)
	}
	if len(out) != 600 {
		t.Fatalf("want 600 vectors, got %d", len(out))
	}
	for i, vec := range out {
		if len(vec) != 1 || int(vec[0]) != i {
			t.Fatalf("vector %d out of order: got %v", i, vec)
		}
	}
	want := []int{250, 250, 100}
	if len(sizes) != len(want) {
		t.Fatalf("want request sizes %v, got %v", want, sizes)
	}
	for i := range want {
		if sizes[i] != want[i] {
			t.Fatalf("want request sizes %v, got %v", want, sizes)
		}
	}
}
