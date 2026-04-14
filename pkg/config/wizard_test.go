package config

import (
	"bytes"
	"strings"
	"testing"
)

// TestWizard_TFIDFPath walks through the simplest happy path: user picks
// TF-IDF (no network, no keys) and declines the auto-reindex.
func TestWizard_TFIDFPath(t *testing.T) {
	// Input script: "5\n" (pick TF-IDF) → "n\n" (decline reindex)
	in := bytes.NewBufferString("5\nn\n")
	out := &bytes.Buffer{}

	result, err := RunSetupWizard(WizardOptions{In: in, Out: out})
	if err != nil {
		t.Fatalf("wizard error: %v\noutput: %s", err, out.String())
	}
	if result.Config.Backend != "tfidf" {
		t.Errorf("expected backend=tfidf, got %q", result.Config.Backend)
	}
	if result.RunReindex {
		t.Errorf("user declined reindex but RunReindex=true")
	}
}

// TestWizard_TFIDFPath_ReindexAccepted verifies the default-yes reindex prompt.
func TestWizard_TFIDFPath_ReindexAccepted(t *testing.T) {
	// "5\n" pick TF-IDF; "\n" (empty = accept default Y)
	in := bytes.NewBufferString("5\n\n")
	out := &bytes.Buffer{}

	result, err := RunSetupWizard(WizardOptions{In: in, Out: out})
	if err != nil {
		t.Fatalf("wizard error: %v", err)
	}
	if !result.RunReindex {
		t.Errorf("default should be yes-reindex (got false)")
	}
}

// TestWizard_RejectsVoyageStub verifies P7-deferred backends return a
// helpful error rather than a crash.
func TestWizard_RejectsVoyageStub(t *testing.T) {
	in := bytes.NewBufferString("3\n")
	out := &bytes.Buffer{}

	_, err := RunSetupWizard(WizardOptions{In: in, Out: out})
	if err == nil || !strings.Contains(err.Error(), "voyage") {
		t.Errorf("expected voyage-not-implemented error, got: %v", err)
	}
}

// TestWizard_InvalidChoice loops until a valid option is provided,
// and the loop MUST consume invalid input (not crash on 0, 99, letters).
func TestWizard_InvalidChoice(t *testing.T) {
	// "0\nfoo\n99\n5\nn\n" — three invalid then pick TF-IDF + decline reindex
	in := bytes.NewBufferString("0\nfoo\n99\n5\nn\n")
	out := &bytes.Buffer{}

	result, err := RunSetupWizard(WizardOptions{In: in, Out: out})
	if err != nil {
		t.Fatalf("wizard error: %v", err)
	}
	if result.Config.Backend != "tfidf" {
		t.Errorf("expected backend=tfidf after retries, got %q", result.Config.Backend)
	}
	// Output should contain the "please enter 1..5" guidance
	if !strings.Contains(out.String(), "Please enter a number 1..5") {
		t.Errorf("output missing retry guidance; got: %s", out.String())
	}
}

// TestIsSecretKey guards against the "copy-pasted curl with API key" footgun.
// Ensure known secret-ish names are rejected, but the env-var-name key is ok.
func TestIsSecretKey(t *testing.T) {
	cases := []struct {
		key    string
		secret bool
	}{
		{"embedding.api_key", true},
		{"embedding.api_key_env", false}, // storing env var NAME is fine
		{"gemini_api_key", true},
		{"openai_api_key", true},
		{"voyage_api_key", true},
		{"some.password", true},
		{"oauth_secret", true},
		{"auth.token", true},
		{"embedding.backend", false},
		{"embedding.model", false},
		{"embedding.endpoint", false},
		{"features.semantic_search", false},
	}
	for _, tc := range cases {
		got := IsSecretKey(tc.key)
		if got != tc.secret {
			t.Errorf("IsSecretKey(%q): want %v, got %v", tc.key, tc.secret, got)
		}
	}
}

// TestApplyBackendToConfig confirms WizardResult lands in the right config keys
// AND that the API key itself never touches disk.
func TestApplyBackendToConfig(t *testing.T) {
	dir := t.TempDir()
	result := &WizardResult{
		APIKeyEnvVar: "GEMINI_API_KEY",
	}
	result.Config.Backend = "gemini"
	result.Config.Model = "gemini-embedding-001"
	result.Config.Dim = 768
	result.Config.APIKey = "THIS-SHOULD-NEVER-BE-WRITTEN"

	if err := ApplyBackendToConfig(dir, result); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Reload and verify
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.GetString("embedding.backend") != "gemini" {
		t.Errorf("backend: want gemini, got %q", cfg.GetString("embedding.backend"))
	}
	if cfg.GetString("embedding.model") != "gemini-embedding-001" {
		t.Errorf("model wrong: %q", cfg.GetString("embedding.model"))
	}
	if cfg.GetInt("embedding.dimensions") != 768 {
		t.Errorf("dim wrong: %d", cfg.GetInt("embedding.dimensions"))
	}
	if cfg.GetString("embedding.api_key_env") != "GEMINI_API_KEY" {
		t.Errorf("env var name not recorded")
	}
	// CRITICAL: the raw key must NOT be in config
	for _, key := range cfg.Keys() {
		v := cfg.GetString(key)
		if strings.Contains(v, "THIS-SHOULD-NEVER-BE-WRITTEN") {
			t.Fatalf("API key leaked into config key %q", key)
		}
	}
}
