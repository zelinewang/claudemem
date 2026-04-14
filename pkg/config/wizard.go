package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zelinewang/claudemem/pkg/vectors"
)

// WizardOptions controls wizard I/O. Extracted so tests can drive the wizard
// without stdin/stdout.
type WizardOptions struct {
	In       io.Reader // defaults to os.Stdin
	Out      io.Writer // defaults to os.Stdout
	StoreDir string    // ~/.claudemem
	// SkipReindexPrompt: if true, caller handles reindex separately.
	SkipReindexPrompt bool
}

// WizardResult describes what the wizard chose. The caller writes it to
// the config file and optionally runs reindex.
type WizardResult struct {
	Config       vectors.BackendConfig
	APIKeyEnvVar string // name of the env var for cloud; "" for local/tfidf
	RunReindex   bool   // whether the user wants to rebuild the index now
}

// RunSetupWizard walks the user through picking + verifying a backend.
// Returns the selected config. Does NOT write to disk — caller is
// responsible for persistence (keeps the wizard pure + testable).
func RunSetupWizard(opts WizardOptions) (*WizardResult, error) {
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	r := bufio.NewReader(opts.In)

	banner(opts.Out)

	fmt.Fprintln(opts.Out, "Which embedding backend do you want to use?")
	fmt.Fprintln(opts.Out, "")
	fmt.Fprintln(opts.Out, "  1) Local — Ollama         (offline, zero cost, recommended for daily use)")
	fmt.Fprintln(opts.Out, "  2) Cloud — Gemini         (best quality, ~$0.15/M tokens, requires API key)")
	fmt.Fprintln(opts.Out, "  3) Cloud — Voyage         (budget pick, $0.02/M, 200M free tokens)")
	fmt.Fprintln(opts.Out, "  4) Cloud — OpenAI         (widely available, weaker Chinese)")
	fmt.Fprintln(opts.Out, "  5) No semantic search     — TF-IDF (keyword-ish; no daemon/key needed)")
	fmt.Fprintln(opts.Out, "")

	choice, err := promptChoice(opts.Out, r, "> ", 5)
	if err != nil {
		return nil, err
	}

	switch choice {
	case 1:
		return setupOllama(opts.Out, r, opts.StoreDir)
	case 2:
		return setupGemini(opts.Out, r, opts.StoreDir)
	case 3:
		return setupVoyage(opts.Out, r, opts.StoreDir)
	case 4:
		return setupOpenAI(opts.Out, r, opts.StoreDir)
	case 5:
		return setupTFIDF(opts.Out, r)
	}
	return nil, fmt.Errorf("unexpected choice %d", choice)
}

// --- Ollama path ---

func setupOllama(w io.Writer, r *bufio.Reader, storeDir string) (*WizardResult, error) {
	const baseURL = "http://localhost:11434"
	fmt.Fprintf(w, "Checking %s ... ", baseURL)
	if err := pingOllama(baseURL); err != nil {
		fmt.Fprintln(w, "unreachable")
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "  Recovery:")
		fmt.Fprintln(w, "    - Install Ollama: curl -fsSL https://ollama.com/install.sh | sh  (needs sudo)")
		fmt.Fprintln(w, "    - Or user-local: see docs/HYBRID_EMBEDDING_PLAN.md Phase A")
		fmt.Fprintln(w, "    - Then: ollama serve &")
		return nil, fmt.Errorf("ollama daemon not reachable: %w", err)
	}
	fmt.Fprintln(w, "OK")
	fmt.Fprintln(w, "")

	models, err := listOllamaModels(baseURL)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}

	// Recommended top 3 + "other"
	recommended := []string{
		"qwen3-embedding:4b  (bilingual, 2.5GB, recommended)",
		"bge-m3              (100+ languages, 1.2GB)",
		"nomic-embed-text    (small, 270MB, English-heavy)",
	}
	fmt.Fprintln(w, "Which model?")
	for i, m := range recommended {
		status := "  "
		if contains(models, trimModelLine(m)) {
			status = "✓ " // already pulled
		}
		fmt.Fprintf(w, "  %d) %s%s\n", i+1, status, m)
	}
	fmt.Fprintln(w, "  4) Other (enter name)")
	modelChoice, err := promptChoice(w, r, "> ", 4)
	if err != nil {
		return nil, err
	}

	var model string
	switch modelChoice {
	case 1:
		model = "qwen3-embedding:4b"
	case 2:
		model = "bge-m3"
	case 3:
		model = "nomic-embed-text"
	case 4:
		model, err = promptLine(w, r, "Model name: ")
		if err != nil {
			return nil, err
		}
	}

	// Offer to pull if missing
	if !contains(models, model) {
		fmt.Fprintf(w, "Model %q not installed locally. Pull now? [Y/n] ", model)
		answer, _ := promptLine(w, r, "")
		if strings.ToLower(strings.TrimSpace(answer)) != "n" {
			fmt.Fprintf(w, "Run: ollama pull %s  (this may take a few minutes)\n", model)
			fmt.Fprintln(w, "  (run this in another terminal; press Enter here once the pull completes)")
			_, _ = promptLine(w, r, "")
		}
	}

	dim, err := promptDim(w, r, 768)
	if err != nil {
		return nil, err
	}

	// Verify with a test embedding
	fmt.Fprint(w, "Testing embedding: \"hello world\" ... ")
	emb := vectors.NewOllamaEmbedder(model)
	vec, err := emb.Embed("hello world", vectors.InputTypeDocument)
	if err != nil {
		fmt.Fprintln(w, "FAIL")
		return nil, fmt.Errorf("ollama test embed failed: %w", err)
	}
	actualDim := len(vec)
	fmt.Fprintf(w, "✓ got %d-dim vector\n", actualDim)

	// Honor matryoshka truncation request ONLY if backend supports it.
	// Ollama doesn't natively truncate; warn if user asked for a smaller dim.
	if dim > 0 && dim != actualDim {
		fmt.Fprintf(w,
			"⚠ Ollama produces %d-dim vectors; requested truncation to %d is not supported by the API.\n",
			actualDim, dim)
		fmt.Fprintln(w, "  Keeping native dim. (Matryoshka truncation works on Gemini/Voyage, not Ollama.)")
		dim = actualDim
	}
	if dim == 0 {
		dim = actualDim
	}

	return &WizardResult{
		Config: vectors.BackendConfig{
			Backend: "ollama",
			Model:   model,
			Dim:     dim,
		},
		RunReindex: confirmReindex(w, r),
	}, nil
}

// --- Gemini path ---

func setupGemini(w io.Writer, r *bufio.Reader, storeDir string) (*WizardResult, error) {
	const envVarName = "GEMINI_API_KEY"
	key := os.Getenv(envVarName)
	if key == "" {
		fmt.Fprintf(w, "\nEnvironment variable %s is NOT set.\n", envVarName)
		fmt.Fprintln(w, "claudemem never stores API keys in config files. Set the env var and re-run.")
		fmt.Fprintf(w, "\n  export %s=your-key-here\n  claudemem setup\n\n", envVarName)
		fmt.Fprintln(w, "Get a key at: https://aistudio.google.com/app/apikey")
		return nil, fmt.Errorf("%s not set in environment", envVarName)
	}
	fmt.Fprintf(w, "Environment variable %s: set ✓\n", envVarName)

	fmt.Fprintln(w, "\nWhich model?")
	fmt.Fprintln(w, "  1) gemini-embedding-001  (recommended — MTEB #1 multilingual, 3072→768-dim matryoshka)")
	modelChoice, err := promptChoice(w, r, "> ", 1)
	if err != nil {
		return nil, err
	}
	model := "gemini-embedding-001"
	_ = modelChoice

	dim, err := promptDim(w, r, 768)
	if err != nil {
		return nil, err
	}

	fmt.Fprint(w, "Testing embedding ... ")
	emb := vectors.NewGeminiEmbedder(model, key, dim)
	vec, err := emb.Embed("hello world", vectors.InputTypeDocument)
	if err != nil {
		fmt.Fprintln(w, "FAIL")
		return nil, fmt.Errorf("gemini test embed failed: %w", err)
	}
	fmt.Fprintf(w, "✓ got %d-dim vector\n", len(vec))

	return &WizardResult{
		Config: vectors.BackendConfig{
			Backend: "gemini",
			Model:   model,
			APIKey:  key,
			Dim:     dim,
		},
		APIKeyEnvVar: envVarName,
		RunReindex:   confirmReindex(w, r),
	}, nil
}

// --- Voyage path ---

func setupVoyage(w io.Writer, r *bufio.Reader, storeDir string) (*WizardResult, error) {
	const envVarName = "VOYAGE_API_KEY"
	key := os.Getenv(envVarName)
	if key == "" {
		fmt.Fprintf(w, "\nEnvironment variable %s is NOT set.\n", envVarName)
		fmt.Fprintln(w, "claudemem never stores API keys in config files. Set the env var and re-run.")
		fmt.Fprintf(w, "\n  export %s=your-key-here\n  claudemem setup\n\n", envVarName)
		fmt.Fprintln(w, "Get a key at: https://dash.voyageai.com/api-keys")
		return nil, fmt.Errorf("%s not set in environment", envVarName)
	}
	fmt.Fprintf(w, "Environment variable %s: set ✓\n", envVarName)

	fmt.Fprintln(w, "\nWhich model?")
	fmt.Fprintln(w, "  1) voyage-3.5-lite   (recommended — $0.02/M, 200M free tokens)")
	fmt.Fprintln(w, "  2) voyage-3.5        (higher quality, $0.06/M)")
	fmt.Fprintln(w, "  3) voyage-3-large    (legacy — kept for users already on it)")
	choice, err := promptChoice(w, r, "> ", 3)
	if err != nil {
		return nil, err
	}
	var model string
	switch choice {
	case 1:
		model = "voyage-3.5-lite"
	case 2:
		model = "voyage-3.5"
	case 3:
		model = "voyage-3-large"
	}

	dim, err := promptDim(w, r, 1024)
	if err != nil {
		return nil, err
	}

	fmt.Fprint(w, "Testing embedding ... ")
	emb := vectors.NewVoyageEmbedder(model, key, dim)
	vec, err := emb.Embed("hello world", vectors.InputTypeDocument)
	if err != nil {
		fmt.Fprintln(w, "FAIL")
		return nil, fmt.Errorf("voyage test embed failed: %w", err)
	}
	fmt.Fprintf(w, "✓ got %d-dim vector\n", len(vec))

	return &WizardResult{
		Config: vectors.BackendConfig{
			Backend: "voyage",
			Model:   model,
			APIKey:  key,
			Dim:     dim,
		},
		APIKeyEnvVar: envVarName,
		RunReindex:   confirmReindex(w, r),
	}, nil
}

// --- OpenAI path ---

func setupOpenAI(w io.Writer, r *bufio.Reader, storeDir string) (*WizardResult, error) {
	const envVarName = "OPENAI_API_KEY"
	key := os.Getenv(envVarName)
	if key == "" {
		fmt.Fprintf(w, "\nEnvironment variable %s is NOT set.\n", envVarName)
		fmt.Fprintf(w, "\n  export %s=your-key-here\n  claudemem setup\n\n", envVarName)
		fmt.Fprintln(w, "Get a key at: https://platform.openai.com/api-keys")
		return nil, fmt.Errorf("%s not set in environment", envVarName)
	}
	fmt.Fprintf(w, "Environment variable %s: set ✓\n", envVarName)

	fmt.Fprintln(w, "\nWhich model?")
	fmt.Fprintln(w, "  1) text-embedding-3-small   (recommended — $0.02/M, 512-dim matryoshka)")
	fmt.Fprintln(w, "  2) text-embedding-3-large   (higher quality, $0.13/M, 3072-dim)")
	choice, err := promptChoice(w, r, "> ", 2)
	if err != nil {
		return nil, err
	}
	var model string
	var defaultDim int
	switch choice {
	case 1:
		model = "text-embedding-3-small"
		defaultDim = 512
	case 2:
		model = "text-embedding-3-large"
		defaultDim = 1024
	}

	dim, err := promptDim(w, r, defaultDim)
	if err != nil {
		return nil, err
	}

	fmt.Fprint(w, "Testing embedding ... ")
	emb := vectors.NewOpenAIEmbedder(model, key, dim)
	vec, err := emb.Embed("hello world", vectors.InputTypeDocument)
	if err != nil {
		fmt.Fprintln(w, "FAIL")
		return nil, fmt.Errorf("openai test embed failed: %w", err)
	}
	fmt.Fprintf(w, "✓ got %d-dim vector\n", len(vec))

	return &WizardResult{
		Config: vectors.BackendConfig{
			Backend: "openai",
			Model:   model,
			APIKey:  key,
			Dim:     dim,
		},
		APIKeyEnvVar: envVarName,
		RunReindex:   confirmReindex(w, r),
	}, nil
}

// --- TF-IDF path ---

func setupTFIDF(w io.Writer, r *bufio.Reader) (*WizardResult, error) {
	fmt.Fprintln(w, "\nSelected: TF-IDF (keyword similarity, no external dependency).")
	fmt.Fprintln(w, "Semantic search will work on vocabulary from your indexed corpus only.")
	return &WizardResult{
		Config: vectors.BackendConfig{
			Backend: "tfidf",
			Model:   "tfidf",
		},
		RunReindex: confirmReindex(w, r),
	}, nil
}

// --- helpers ---

func banner(w io.Writer) {
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "claudemem — Memory Setup")
	fmt.Fprintln(w, "")
}

func pingOllama(baseURL string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL + "/api/tags")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func listOllamaModels(baseURL string) ([]string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL + "/api/tags")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	names := make([]string, len(body.Models))
	for i, m := range body.Models {
		names[i] = m.Name
	}
	return names, nil
}

// promptChoice reads "N" from the reader, validating 1..max.
func promptChoice(w io.Writer, r *bufio.Reader, prompt string, max int) (int, error) {
	for {
		fmt.Fprint(w, prompt)
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return 0, err
		}
		line = strings.TrimSpace(line)
		n, convErr := strconv.Atoi(line)
		if convErr != nil || n < 1 || n > max {
			fmt.Fprintf(w, "  Please enter a number 1..%d\n", max)
			continue
		}
		return n, nil
	}
}

func promptLine(w io.Writer, r *bufio.Reader, prompt string) (string, error) {
	fmt.Fprint(w, prompt)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func promptDim(w io.Writer, r *bufio.Reader, defaultDim int) (int, error) {
	fmt.Fprintf(w, "Matryoshka dimension (recommended %d for <5k notes) [%d]: ", defaultDim, defaultDim)
	line, err := promptLine(w, r, "")
	if err != nil {
		return 0, err
	}
	if line == "" {
		return defaultDim, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 {
		fmt.Fprintf(w, "  Invalid dim; keeping default %d\n", defaultDim)
		return defaultDim, nil
	}
	return n, nil
}

func confirmReindex(w io.Writer, r *bufio.Reader) bool {
	fmt.Fprint(w, "\nRebuild vector index with new backend now? [Y/n] ")
	answer, _ := promptLine(w, r, "")
	return strings.ToLower(answer) != "n"
}

// trimModelLine extracts the model name from a "name  (description)" line.
func trimModelLine(s string) string {
	if i := strings.Index(s, " "); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		// Ollama returns "qwen3-embedding:4b" or "nomic-embed-text:latest"
		if h == needle || strings.HasPrefix(h, needle+":") {
			return true
		}
	}
	return false
}

// --- config persistence helpers for the CLI layer ---

// ApplyBackendToConfig writes the wizard result to ~/.claudemem/config.json.
// Secrets (API keys) are NEVER written — only the env var NAME.
func ApplyBackendToConfig(storeDir string, result *WizardResult) error {
	cfg, err := Load(storeDir)
	if err != nil {
		return err
	}
	cfg.Set("embedding.backend", result.Config.Backend)
	cfg.Set("embedding.model", result.Config.Model)
	if result.Config.Dim > 0 {
		cfg.Set("embedding.dimensions", result.Config.Dim)
	}
	if result.Config.Endpoint != "" {
		cfg.Set("embedding.endpoint", result.Config.Endpoint)
	}
	if result.APIKeyEnvVar != "" {
		cfg.Set("embedding.api_key_env", result.APIKeyEnvVar)
	}
	// Reconfirm feature flag
	cfg.Set("features.semantic_search", "true")
	return cfg.Save()
}

// IsSecretKey reports whether a config key name looks like a place someone
// might mistakenly try to store an API key. The CLI `config set` command
// rejects these to enforce env-var-only secrets.
func IsSecretKey(key string) bool {
	k := strings.ToLower(key)
	if strings.Contains(k, "api_key_env") {
		return false // that's the env var NAME, safe to store
	}
	return strings.Contains(k, "api_key") ||
		strings.Contains(k, "secret") ||
		strings.Contains(k, "password") ||
		strings.Contains(k, "token")
}
