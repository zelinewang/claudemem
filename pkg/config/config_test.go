package config

import (
	"path/filepath"
	"testing"
)

// TestGetInt_HandlesStringValue pins the fix for the silent dimension-loss
// bug: `claudemem config set KEY VAL` stores VAL as a JSON string, and the
// pre-fix GetInt only recognised float64, so `embedding.dimensions=768`
// silently read back as 0 — causing Gemini to return native 3072d instead
// of the configured matryoshka-truncated 768d.
//
// If this test fails, the CLI path for setting int configs is broken and
// the `claudemem setup` interactive path (which Sets int directly) would
// be the ONLY way to set numeric keys correctly.
func TestGetInt_HandlesStringValue(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		path: filepath.Join(dir, "config.json"),
		data: map[string]interface{}{
			"from_json_number": float64(768), // how JSON decodes numbers
			"from_cli_set":     "768",        // how `config set` stores values
			"empty":            "",
			"not_a_number":     "abc",
			"missing_key_absent_from_map": nil, // sanity: nil value
		},
	}

	if got := cfg.GetInt("from_json_number"); got != 768 {
		t.Errorf("from_json_number: want 768, got %d", got)
	}
	if got := cfg.GetInt("from_cli_set"); got != 768 {
		t.Errorf("from_cli_set (THE bug): want 768, got %d", got)
	}
	if got := cfg.GetInt("empty"); got != 0 {
		t.Errorf("empty string: want 0, got %d", got)
	}
	if got := cfg.GetInt("not_a_number"); got != 0 {
		t.Errorf("not_a_number: want 0 (graceful), got %d", got)
	}
	if got := cfg.GetInt("missing_key"); got != 0 {
		t.Errorf("missing key: want 0, got %d", got)
	}
}
