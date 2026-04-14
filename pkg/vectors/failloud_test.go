package vectors

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"
)

// newMockOllamaServer returns an httptest server that responds to
// /api/tags with the given status + body. Used to mock an Ollama
// daemon for Available() tests without starting a real daemon.
func newMockOllamaServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// failingEmbedder implements Embedder with all calls returning
// ErrBackendUnavailable. Exists to simulate a down-backend scenario
// in tests without requiring actual network.
type failingEmbedder struct {
	name, model string
	hint        string
}

func newFailingEmbedder(name, model, hint string) *failingEmbedder {
	return &failingEmbedder{name: name, model: model, hint: hint}
}

func (f *failingEmbedder) Available() error {
	return &ErrBackendUnavailable{
		Backend: f.name + ":" + f.model,
		Cause:   errors.New("simulated unreachable"),
		Hint:    f.hint,
	}
}
func (f *failingEmbedder) Embed(text string, _ InputType) ([]float32, error) {
	return nil, f.Available()
}
func (f *failingEmbedder) EmbedBatch(texts []string, _ InputType) ([][]float32, error) {
	return nil, f.Available()
}
func (f *failingEmbedder) Name() string     { return f.name }
func (f *failingEmbedder) Model() string    { return f.model }
func (f *failingEmbedder) Dimensions() int  { return 768 }

// TestIndexDocument_ForgivingOnEmbedFailure verifies the write-path
// contract: if the backend is down at index time, IndexDocument logs
// and returns nil — it MUST NOT propagate the error, because that would
// block note creation every time the user is offline.
//
// This is the explicit carve-out from "no silent fallback": reads fail
// loud, writes are forgiving. The pairing of this test + TestHybridSearch_PropagatesBackendUnavailable
// below pins both halves of the rule in code.
func TestIndexDocument_ForgivingOnEmbedFailure(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	emb := newFailingEmbedder("ollama", "nomic-embed-text", "run: ollama serve")
	vs, err := NewVectorStore(db, emb)
	if err != nil {
		t.Fatalf("NewVectorStore: %v", err)
	}

	// IndexDocument should return nil (not the embed error), log a warning,
	// and leave the vectors table untouched.
	err = vs.IndexDocument("doc-1", "some content to embed")
	if err != nil {
		t.Errorf("IndexDocument should tolerate embed failure and return nil; got %v", err)
	}

	// Vectors table should have 0 rows — nothing was inserted because embed failed
	n, _ := vs.CountAll()
	if n != 0 {
		t.Errorf("expected 0 vector rows after failed embed, got %d", n)
	}
}

// TestSearch_PropagatesBackendUnavailable verifies the read-path contract:
// if the backend is down during search, the error MUST propagate to the
// caller so cmd/search.go's fail-loud + interactive recovery path can
// trigger. Silent degradation to FTS would be a regression.
func TestSearch_PropagatesBackendUnavailable(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	emb := newFailingEmbedder("ollama", "qwen3-embedding:4b", "run: ollama serve")
	vs, err := NewVectorStore(db, emb)
	if err != nil {
		t.Fatalf("NewVectorStore: %v", err)
	}

	// No docs indexed — but the query still tries to embed, which fails.
	_, err = vs.Search("something", 10)
	if err == nil {
		t.Fatal("Search should propagate backend failure; got nil error")
	}
	if !IsBackendUnavailable(err) {
		t.Errorf("Search should return ErrBackendUnavailable, got %T: %v", err, err)
	}

	// Error message should include the recovery hint so the CLI can show it.
	var ebu *ErrBackendUnavailable
	if !errors.As(err, &ebu) {
		t.Fatalf("expected *ErrBackendUnavailable, got %T", err)
	}
	if ebu.Hint != "run: ollama serve" {
		t.Errorf("expected recovery hint 'run: ollama serve', got %q", ebu.Hint)
	}
	if ebu.Backend != "ollama:qwen3-embedding:4b" {
		t.Errorf("expected Backend='ollama:qwen3-embedding:4b', got %q", ebu.Backend)
	}
}

// TestOllamaEmbedder_Available_Reachable verifies the happy-path
// Available() call against a mocked Ollama daemon returning 200 on /api/tags.
func TestOllamaEmbedder_Available_Reachable(t *testing.T) {
	srv := newMockOllamaServer(t, 200, `{"models":[{"name":"nomic-embed-text:latest"}]}`)
	defer srv.Close()

	emb := NewOllamaEmbedder("nomic-embed-text")
	emb.baseURL = srv.URL
	if err := emb.Available(); err != nil {
		t.Errorf("Available should succeed on HTTP 200, got %v", err)
	}
}

// TestOllamaEmbedder_Available_Unreachable verifies the error-path
// contract: connection refused → ErrBackendUnavailable with a hint
// that tells the user to run `ollama serve`. The hint text is
// load-bearing UX (the plan documents it verbatim); regression here
// silently breaks the CLI's recovery message.
func TestOllamaEmbedder_Available_Unreachable(t *testing.T) {
	// Use port 1 (privileged, never bound by a real server under a normal
	// user) → dial returns ECONNREFUSED fast and deterministically.
	// Avoids the httptest.Close()+URL-reuse pattern whose port could
	// theoretically be rebound by another process between Close() and
	// the test's dial on busy CI runners.
	emb := NewOllamaEmbedder("qwen3-embedding:4b")
	emb.baseURL = "http://127.0.0.1:1"

	err := emb.Available()
	if err == nil {
		t.Fatal("Available should fail when daemon is unreachable")
	}
	if !IsBackendUnavailable(err) {
		t.Errorf("expected ErrBackendUnavailable, got %T: %v", err, err)
	}

	var ebu *ErrBackendUnavailable
	if !errors.As(err, &ebu) {
		t.Fatalf("could not unwrap ErrBackendUnavailable from %T: %v", err, err)
	}
	if ebu.Backend != "ollama:qwen3-embedding:4b" {
		t.Errorf("Backend: want ollama:qwen3-embedding:4b, got %q", ebu.Backend)
	}
	// Hint must mention `ollama serve` so users know what to run
	if !containsStr(ebu.Hint, "ollama serve") {
		t.Errorf("Hint should mention 'ollama serve', got %q", ebu.Hint)
	}
}

// TestOllamaEmbedder_Available_Non200 verifies graceful handling of
// HTTP errors (daemon running but unhealthy).
func TestOllamaEmbedder_Available_Non200(t *testing.T) {
	srv := newMockOllamaServer(t, 503, `service temporarily unavailable`)
	defer srv.Close()

	emb := NewOllamaEmbedder("qwen3-embedding:4b")
	emb.baseURL = srv.URL

	err := emb.Available()
	if err == nil {
		t.Fatal("Available should fail on HTTP 503")
	}
	if !IsBackendUnavailable(err) {
		t.Errorf("expected ErrBackendUnavailable, got %T", err)
	}
}

// --- helpers ---

func containsStr(haystack, needle string) bool {
	return len(haystack) >= len(needle) && findSub(haystack, needle) >= 0
}

func findSub(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
