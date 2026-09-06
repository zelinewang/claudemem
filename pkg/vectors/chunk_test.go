package vectors

import (
	"fmt"
	"strings"
	"testing"
)

// longASCIIDoc builds a document of numbered paragraphs, comfortably over the
// chunk budget, with a unique marker in its final paragraph.
func longASCIIDoc(paragraphs int) (string, string) {
	var b strings.Builder
	for i := 0; i < paragraphs; i++ {
		fmt.Fprintf(&b, "paragraph %04d: the quick brown fox jumps over the lazy dog and keeps running through the field all afternoon long.\n\n", i)
	}
	marker := "TAILMARKER-ZEBRA-42: the migration rollback procedure lives at the very end of this document."
	b.WriteString(marker)
	return b.String(), marker
}

func TestChunkForEmbed_ShortPassthrough(t *testing.T) {
	short := "a normal-sized note about database migrations and rollback safety"
	chunks := ChunkForEmbed(short)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for short text, got %d", len(chunks))
	}
	if chunks[0] != short {
		t.Error("short text must pass through unchanged")
	}
}

func TestChunkForEmbed_Empty(t *testing.T) {
	if chunks := ChunkForEmbed(""); len(chunks) != 1 || chunks[0] != "" {
		t.Errorf("empty text should produce exactly one empty chunk, got %v", chunks)
	}
}

func TestChunkForEmbed_LongDocCoversTail(t *testing.T) {
	text, marker := longASCIIDoc(120) // ~13k chars, well over budget
	chunks := ChunkForEmbed(text)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for long doc, got %d", len(chunks))
	}
	// The pre-v23 behavior dropped this marker (hard truncation at 7500
	// bytes); chunking must keep the tail searchable.
	if got := chunks[len(chunks)-1]; !strings.Contains(got, marker) {
		t.Error("tail marker missing from final chunk — tail coverage regression")
	}
	if !strings.HasPrefix(chunks[0], "paragraph 0000:") {
		t.Error("first chunk must start at the document head")
	}
	// Every chunk within the token budget.
	for i, c := range chunks {
		if est := estimateTokens([]rune(c)); est > chunkTokenBudget+200 { // +200 slack for boundary overshoot
			t.Errorf("chunk %d over budget: %d est tokens", i, est)
		}
	}
}

func TestChunkForEmbed_Overlap(t *testing.T) {
	text, _ := longASCIIDoc(120)
	chunks := ChunkForEmbed(text)
	if len(chunks) < 2 {
		t.Fatalf("need multiple chunks, got %d", len(chunks))
	}
	for i := 1; i < len(chunks); i++ {
		prev := []rune(chunks[i-1])
		tail := string(prev[len(prev)-80:]) // last 80 runes of previous chunk
		if !strings.Contains(chunks[i], tail) {
			t.Errorf("chunks %d/%d do not overlap — boundary content could be lost", i-1, i)
		}
	}
}

func TestChunkForEmbed_CJKBudget(t *testing.T) {
	// Dense CJK costs ~1 token per rune — the naive 7500-byte cut could
	// exceed the model's 2048-token cap. Budgeted chunking must split it.
	text := strings.Repeat("数据库迁移安全性检查", 300) // 3000 runes ≈ 3000 tokens
	chunks := ChunkForEmbed(text)
	if len(chunks) < 2 {
		t.Fatalf("expected CJK long text to split, got %d chunk(s)", len(chunks))
	}
	for i, c := range chunks {
		if est := estimateTokens([]rune(c)); est > chunkTokenBudget+50 {
			t.Errorf("CJK chunk %d over budget: %d est tokens", i, est)
		}
	}
	// Coverage: concatenating chunk cores must reach the end.
	if last := chunks[len(chunks)-1]; !strings.HasSuffix(text, last[len(last)/2:]) {
		t.Error("CJK tail coverage broken")
	}
}
