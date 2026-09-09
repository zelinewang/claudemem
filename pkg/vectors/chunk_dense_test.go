package vectors

import (
	"fmt"
	"strings"
	"testing"
)

// uuidDenseText builds token-dense ASCII (UUID-like hex groups). Measured on
// Vertex gemini-embedding-001 (2026-09-09): 5,919 such characters = 5,380
// real tokens (~0.9 token/char), while the letters-only estimator assumed
// ~1,480 — the API silently truncated the chunk (statistics.truncated=true).
func uuidDenseText(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "%08x-%04x-%04x-%04x-%012x ", i*2654435761, i*40503, i*7919, i*104729, i*1000003)
	}
	return b.String()[:n]
}

// TestEstimateTokens_DenseASCIICountsNearOneTokenPerChar pins the estimator
// against the measurement above: dense hex/UUID text must be budgeted at
// (at least) 0.8 token per character, not 0.25.
func TestEstimateTokens_DenseASCIICountsNearOneTokenPerChar(t *testing.T) {
	text := uuidDenseText(3700)
	est := estimateTokens([]rune(text))
	if est < 3700*8/10 {
		t.Fatalf("dense ASCII estimated at %d tokens for %d chars (%.2f/char); Vertex measures ~0.9/char", est, len(text), float64(est)/float64(len(text)))
	}
}

// TestChunkForEmbed_DenseASCIISplitsUnderBudget: 6,000 UUID-dense characters
// (~5,400 real tokens) must become several chunks, each within the estimated
// budget, instead of one chunk the API truncates at 2,048 tokens.
func TestChunkForEmbed_DenseASCIISplitsUnderBudget(t *testing.T) {
	text := uuidDenseText(6000)
	chunks := ChunkForEmbed(text)
	if len(chunks) < 3 {
		t.Fatalf("want ≥3 chunks for 6000 dense chars, got %d", len(chunks))
	}
	for i, c := range chunks {
		if est := estimateTokens([]rune(c)); est > chunkTokenBudget+200 {
			t.Fatalf("chunk %d estimated at %d tokens, over budget %d", i, est, chunkTokenBudget)
		}
	}
	// Every character is still covered (overlapping chunks, no gaps).
	joined := strings.Join(chunks, "")
	for _, probe := range []string{text[:40], text[len(text)-40:]} {
		if !strings.Contains(joined, probe) {
			t.Fatalf("chunks do not cover the text around %q", probe)
		}
	}
}

// TestChunkForEmbed_ProseStillPassesThrough guards the common case: 6,000
// characters of ordinary English words (~1,500 tokens) remain ONE chunk.
func TestChunkForEmbed_ProseStillPassesThrough(t *testing.T) {
	prose := strings.Repeat("the migration keeps every existing row as chunk zero and re-embeds the tail later ", 80)[:6000]
	if got := len(ChunkForEmbed(prose)); got != 1 {
		t.Fatalf("plain prose of 6000 chars should stay a single chunk, got %d", got)
	}
}
