package vectors

import "unicode"

// Chunking for embedding long documents.
//
// gemini-embedding-001 caps input at 2048 tokens; the pre-v23 behavior
// hard-truncated every document at maxEmbedChars (7500 bytes, a constant
// written for the local nomic-embed-text model), so the tail of any long
// note/session — 11% of the corpus, including every long session postmortem —
// was invisible to semantic search. ChunkForEmbed instead splits long texts
// into overlapping chunks under an ESTIMATED token budget; each chunk is
// embedded separately and Search aggregates back to the document (max).
//
// Token estimation is deliberately crude (no tokenizer dependency — the
// project is pure-Go, CGO_ENABLED=0): CJK runes count as 1 token, letters in
// ordinary words as 1/4 token, and every rune of a token-dense word (digits,
// paths, hashes, UUIDs, code) as 1 token — see runeCosts. Internally we
// accumulate quarter-tokens so the math stays integral.

const (
	// chunkTokenBudget is the per-chunk estimated-token target. The model
	// hard limit is 2048; we aim well under it so the estimator's error
	// margin (and a boundary line that overshoots) can never blow the cap.
	chunkTokenBudget = 1600
	// chunkOverlapTokens straddles boundaries so content spanning a cut is
	// still fully contained in at least one chunk.
	chunkOverlapTokens = 160
)

// isCJK reports whether a rune is CJK/Hangul/Kana or CJK punctuation —
// roughly one token per rune on every backend we use.
func isCJK(r rune) bool {
	switch {
	case r >= 0x2E80 && r <= 0x9FFF,
		r >= 0xAC00 && r <= 0xD7AF,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFF00 && r <= 0xFF65,
		r >= 0x3000 && r <= 0x303F:
		return true
	}
	return false
}

// isDenseRune marks the characters that turn a word into token-dense text:
// digits and structural symbols (paths, URLs, hashes, UUIDs, code, markup).
// Ordinary sentence punctuation, hyphens and markdown emphasis are excluded
// so "afternoon.", "re-embeds" and "**bold**" stay cheap; UUIDs, hashes and
// dates are caught by their digits.
func isDenseRune(r rune) bool {
	if r >= '0' && r <= '9' {
		return true
	}
	switch r {
	case '/', '\\', '_', '=', '#', '@', '[', ']', '{', '}', '<', '>', '|', '~', '^', '+', '%', '$', '&', '`':
		return true
	}
	return false
}

// runeCosts returns the estimated cost of every rune in quarter-tokens.
// Letters inside ordinary words cost 1 (≈4 chars per token), whitespace 1,
// CJK runes 4, and every rune of a "dense" word — one containing a digit or
// a structural symbol — costs 4: UUIDs, hashes, paths and minified code
// tokenize at roughly one token per character (measured on
// gemini-embedding-001, 2026-09-09: 5,919 UUID chars → 5,380 tokens; the
// letters-only estimate was 1,480 and the API truncated the chunk silently).
// Overestimating only yields smaller chunks; underestimating loses the tail.
func runeCosts(runes []rune) []int {
	costs := make([]int, len(runes))
	start, dense := -1, false
	flush := func(end int) {
		for i := start; i < end; i++ {
			switch {
			case isCJK(runes[i]), dense:
				costs[i] = 4
			default:
				costs[i] = 1
			}
		}
	}
	for i, r := range runes {
		if unicode.IsSpace(r) {
			if start >= 0 {
				flush(i)
				start, dense = -1, false
			}
			costs[i] = 1
			continue
		}
		if start < 0 {
			start = i
		}
		if isDenseRune(r) {
			dense = true
		}
	}
	if start >= 0 {
		flush(len(runes))
	}
	return costs
}

// ChunkForEmbed splits text into overlapping chunks sized by estimated token
// budget. Texts at or under the budget pass through as a single chunk,
// unchanged — the common case (most notes) embeds exactly as before.
// Every rune of the input is covered by at least one chunk.
func ChunkForEmbed(text string) []string {
	runes := []rune(text)
	costs := runeCosts(runes)
	if tokensFromCosts(costs) <= chunkTokenBudget {
		return []string{text}
	}

	budget := chunkTokenBudget * 4 // quarter-tokens
	overlap := chunkOverlapTokens * 4

	var chunks []string
	start := 0 // rune index
	for start < len(runes) {
		cut := chunkCut(runes, costs, start, budget)
		chunks = append(chunks, string(runes[start:cut]))
		if cut >= len(runes) {
			break
		}
		next := stepBack(costs, cut, overlap)
		if next <= start { // guarantee progress even in pathological cases
			next = start + 1
		}
		start = next
	}
	return chunks
}

// estimateTokens returns the estimated token cost of a rune slice.
func estimateTokens(runes []rune) int {
	return tokensFromCosts(runeCosts(runes))
}

func tokensFromCosts(costs []int) int {
	q := 0
	for _, c := range costs {
		q += c
	}
	return (q + 3) / 4
}

// chunkCut picks the end rune index for a chunk starting at `start`, at most
// `budget` quarter-tokens wide, preferring to break at (in order) a paragraph
// boundary, line boundary, sentence-ending punctuation, or whitespace within
// the trailing 25% of the window; falling back to a hard cut at budget.
func chunkCut(runes []rune, costs []int, start, budget int) int {
	q := 0
	i := start
	for i < len(runes) {
		w := costs[i]
		if q+w > budget {
			break
		}
		q += w
		i++
	}
	if i >= len(runes) {
		return len(runes)
	}
	if i == start { // single rune wider than budget — impossible, but never wedge
		return start + 1
	}
	return preferBoundary(runes, start, i)
}

// preferBoundary scans the trailing quarter of [start, hard) for a nice break
// point and returns the index just AFTER the boundary rune(s). Returns hard
// when no boundary is found.
func preferBoundary(runes []rune, start, hard int) int {
	window := (hard - start) / 4
	lo := hard - window
	if lo < start {
		lo = start
	}
	isSentenceEnd := func(r rune) bool {
		switch r {
		case '.', '!', '?', '。', '！', '？', '；', ';':
			return true
		}
		return false
	}
	// Pass 1: paragraph break — return after the blank line.
	for i := hard - 2; i >= lo; i-- {
		if runes[i] == '\n' && runes[i+1] == '\n' {
			return i + 2
		}
	}
	// Pass 2: line break.
	for i := hard - 1; i >= lo; i-- {
		if runes[i] == '\n' {
			return i + 1
		}
	}
	// Pass 3: sentence punctuation, then any whitespace.
	for i := hard - 1; i >= lo; i-- {
		if isSentenceEnd(runes[i]) {
			return i + 1
		}
	}
	for i := hard - 1; i >= lo; i-- {
		if runes[i] == ' ' || runes[i] == '\t' {
			return i + 1
		}
	}
	return hard
}

// stepBack moves cut back by approximately `overlap` quarter-tokens,
// returning the start index for the next chunk.
func stepBack(costs []int, cut, overlap int) int {
	q := 0
	i := cut
	for i > 0 && q < overlap {
		i--
		q += costs[i]
	}
	return i
}
