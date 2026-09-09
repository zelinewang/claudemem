package vectors

import (
	"encoding/json"
	"fmt"
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

// TestVertex_EmbedBatchWarnsWhenAPITruncates: gemini-embedding-001 does not
// reject an over-limit input — it embeds the first 2,048 tokens and reports
// statistics.truncated=true (measured 2026-09-09: 5,919 UUID chars →
// token_count 5380, truncated). Silent truncation is exactly the coverage
// loss v23 chunking exists to prevent, so the embedder must surface it.
func TestVertex_EmbedBatchWarnsWhenAPITruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"predictions":[
			{"embeddings":{"values":[1],"statistics":{"token_count":5380,"truncated":true}}},
			{"embeddings":{"values":[2],"statistics":{"token_count":12,"truncated":false}}}]}`))
	}))
	defer srv.Close()

	var warnings []string
	orig := vertexWarnf
	vertexWarnf = func(format string, a ...any) { warnings = append(warnings, fmt.Sprintf(format, a...)) }
	defer func() { vertexWarnf = orig }()

	emb := NewVertexEmbedder("test-project", "us-central1", "gemini-embedding-001", 1).
		WithBaseURL(srv.URL).
		WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test"}))
	out, err := emb.EmbedBatch([]string{"dense uuid text", "short"}, InputTypeDocument)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(out) != 2 || out[0][0] != 1 || out[1][0] != 2 {
		t.Fatalf("vectors must still be returned in order, got %v", out)
	}
	if len(warnings) != 1 {
		t.Fatalf("want exactly one truncation warning, got %d: %v", len(warnings), warnings)
	}
	for _, needle := range []string{"1 of 2", "truncated", "5380"} {
		if !strings.Contains(warnings[0], needle) {
			t.Fatalf("warning should mention %q, got %q", needle, warnings[0])
		}
	}
}
