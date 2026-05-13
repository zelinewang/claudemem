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
