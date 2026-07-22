package cmd

import (
	"testing"

	"github.com/zelinewang/claudemem/pkg/config"
	"github.com/zelinewang/claudemem/pkg/vectors"
)

func TestApplyEmbeddingConfigHealthMissingCloudKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	cfg := testHealthConfig(t, "gemini", "GEMINI_API_KEY")
	report := healthyHealthReport()

	applyEmbeddingConfigHealth(report, cfg)

	if report.I6ActiveBackendConfigured {
		t.Fatal("expected missing Gemini key to fail I6")
	}
	if report.Healthy() {
		t.Fatal("report with missing Gemini key should not be healthy")
	}
	if len(report.Issues) == 0 || report.Issues[0][:2] != "I6" {
		t.Fatalf("expected I6 issue, got %#v", report.Issues)
	}
}

func TestApplyEmbeddingConfigHealthPresentCloudKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	cfg := testHealthConfig(t, "gemini", "GEMINI_API_KEY")
	report := healthyHealthReport()

	applyEmbeddingConfigHealth(report, cfg)

	if !report.I6ActiveBackendConfigured {
		t.Fatal("expected configured Gemini key to pass I6")
	}
	if !report.Healthy() {
		t.Fatalf("report should remain healthy, got %#v", report.Issues)
	}
}

func TestApplyEmbeddingConfigHealthIgnoresLocalBackend(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	cfg := testHealthConfig(t, "tfidf", "GEMINI_API_KEY")
	report := healthyHealthReport()

	applyEmbeddingConfigHealth(report, cfg)

	if !report.Healthy() {
		t.Fatalf("local backend should not require cloud key, got %#v", report.Issues)
	}
}

func testHealthConfig(t *testing.T, backend, keyEnv string) *config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Set("features.semantic_search", true)
	cfg.Set("embedding.backend", backend)
	cfg.Set("embedding.api_key_env", keyEnv)
	return cfg
}

func healthyHealthReport() *vectors.HealthReport {
	return &vectors.HealthReport{
		I1MarkdownMatchesEntries:    true,
		I2EntriesMatchesFTS:         true,
		I3VectorsMatchActiveBackend: true,
		I6ActiveBackendConfigured:   true,
	}
}

func TestStaleVectorSummary(t *testing.T) {
	r := healthyHealthReport()
	r.ActiveBackend = "vertex"
	r.ActiveModel = "gemini-embedding-001"
	r.VectorTotals = map[string]int{
		"vertex:gemini-embedding-001": 4100,
		"tfidf:tfidf":                 3082,
		"ollama:nomic-embed-text":     469,
	}

	total, backends := staleVectorSummary(r)
	if total != 3551 || backends != 2 {
		t.Fatalf("expected total=3551 backends=2, got total=%d backends=%d", total, backends)
	}

	// No active backend configured -> nothing counts as stale (pruning
	// against an empty active tuple would delete everything).
	r2 := healthyHealthReport()
	r2.VectorTotals = map[string]int{"tfidf:tfidf": 10}
	if total, backends := staleVectorSummary(r2); total != 0 || backends != 0 {
		t.Fatalf("no-active-backend must report zero, got total=%d backends=%d", total, backends)
	}

	// Only the active backend present -> zero stale.
	r3 := healthyHealthReport()
	r3.ActiveBackend = "tfidf"
	r3.ActiveModel = "tfidf"
	r3.VectorTotals = map[string]int{"tfidf:tfidf": 10}
	if total, backends := staleVectorSummary(r3); total != 0 || backends != 0 {
		t.Fatalf("active-only must report zero, got total=%d backends=%d", total, backends)
	}
}
