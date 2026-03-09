package vectors

import (
	"math"
	"strings"
	"unicode"
)

// Vectorizer computes TF-IDF vectors for text documents.
// It maintains a vocabulary built from all indexed documents, enabling
// semantic similarity search via cosine similarity on the resulting vectors.
//
// Thread safety: NOT safe for concurrent use. Caller must synchronize.
type Vectorizer struct {
	// vocab maps each term to its index in the vector
	vocab map[string]int
	// docFreq tracks how many documents contain each term (for IDF)
	docFreq map[string]int
	// totalDocs is the total number of documents indexed
	totalDocs int
}

// NewVectorizer creates a new TF-IDF vectorizer.
func NewVectorizer() *Vectorizer {
	return &Vectorizer{
		vocab:   make(map[string]int),
		docFreq: make(map[string]int),
	}
}

// VocabSize returns the current vocabulary size (vector dimension).
func (v *Vectorizer) VocabSize() int {
	return len(v.vocab)
}

// TotalDocs returns the number of documents in the corpus.
func (v *Vectorizer) TotalDocs() int {
	return v.totalDocs
}

// BuildVocab builds the vocabulary and document frequency table from a corpus.
// This replaces any existing vocabulary. Must be called before Vectorize.
func (v *Vectorizer) BuildVocab(documents []string) {
	v.vocab = make(map[string]int)
	v.docFreq = make(map[string]int)
	v.totalDocs = len(documents)

	// First pass: count document frequency for each term
	for _, doc := range documents {
		seen := make(map[string]bool)
		for _, term := range tokenize(doc) {
			if !seen[term] {
				seen[term] = true
				v.docFreq[term]++
			}
		}
	}

	// Build vocabulary: assign index to each term, sorted by frequency (desc)
	// to keep the most informative terms at lower indices.
	// Filter out terms that appear in >95% of docs (too common) or only once (noise).
	maxDF := int(float64(v.totalDocs) * 0.95)
	if maxDF < 1 {
		maxDF = v.totalDocs
	}

	idx := 0
	for term, df := range v.docFreq {
		if df > maxDF {
			continue // too common, skip
		}
		if df < 1 {
			continue // shouldn't happen, but guard
		}
		v.vocab[term] = idx
		idx++
	}
}

// Vectorize computes a TF-IDF vector for the given text.
// Returns a normalized float32 vector. The vector dimension equals VocabSize().
// If the vocabulary is empty, returns nil.
func (v *Vectorizer) Vectorize(text string) []float32 {
	if len(v.vocab) == 0 {
		return nil
	}

	terms := tokenize(text)
	if len(terms) == 0 {
		return make([]float32, len(v.vocab))
	}

	// Count term frequency
	tf := make(map[string]int)
	for _, term := range terms {
		tf[term]++
	}

	// Compute TF-IDF vector
	vec := make([]float32, len(v.vocab))
	for term, count := range tf {
		idx, ok := v.vocab[term]
		if !ok {
			continue // term not in vocabulary
		}
		// TF: log-normalized to dampen the effect of term frequency
		termFreq := 1.0 + math.Log(float64(count))
		// IDF: inverse document frequency with smoothing
		df := v.docFreq[term]
		if df == 0 {
			df = 1
		}
		idf := math.Log(float64(v.totalDocs+1) / float64(df+1))
		vec[idx] = float32(termFreq * idf)
	}

	// L2 normalize
	normalize(vec)
	return vec
}

// ExportState returns the vectorizer state for persistence.
func (v *Vectorizer) ExportState() *VectorizerState {
	vocab := make(map[string]int, len(v.vocab))
	for k, val := range v.vocab {
		vocab[k] = val
	}
	docFreq := make(map[string]int, len(v.docFreq))
	for k, val := range v.docFreq {
		docFreq[k] = val
	}
	return &VectorizerState{
		Vocab:     vocab,
		DocFreq:   docFreq,
		TotalDocs: v.totalDocs,
	}
}

// ImportState restores the vectorizer from persisted state.
func (v *Vectorizer) ImportState(state *VectorizerState) {
	v.vocab = state.Vocab
	v.docFreq = state.DocFreq
	v.totalDocs = state.TotalDocs
}

// VectorizerState holds the serializable state of a Vectorizer.
type VectorizerState struct {
	Vocab     map[string]int `json:"vocab"`
	DocFreq   map[string]int `json:"doc_freq"`
	TotalDocs int            `json:"total_docs"`
}

// CosineSimilarity computes the cosine similarity between two normalized vectors.
// Returns a value in [-1, 1]. Both vectors must be the same length and L2-normalized.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot float32
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}

// normalize performs in-place L2 normalization of a vector.
func normalize(vec []float32) {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	if sum == 0 {
		return
	}
	norm := float32(math.Sqrt(sum))
	for i := range vec {
		vec[i] /= norm
	}
}

// tokenize splits text into lowercase terms, removing punctuation and short words.
func tokenize(text string) []string {
	text = strings.ToLower(text)

	var tokens []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				word := current.String()
				if len(word) >= 2 && !isStopWord(word) {
					tokens = append(tokens, word)
				}
				current.Reset()
			}
		}
	}
	// Flush last word
	if current.Len() > 0 {
		word := current.String()
		if len(word) >= 2 && !isStopWord(word) {
			tokens = append(tokens, word)
		}
	}

	return tokens
}

// isStopWord returns true for common English stop words that carry little semantic meaning.
var stopWordSet = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true,
	"not": true, "you": true, "all": true, "can": true, "had": true,
	"her": true, "was": true, "one": true, "our": true, "out": true,
	"has": true, "have": true, "been": true, "from": true, "with": true,
	"they": true, "this": true, "that": true, "what": true, "when": true,
	"which": true, "their": true, "will": true, "each": true, "make": true,
	"how": true, "its": true, "may": true, "into": true, "than": true,
	"then": true, "them": true, "same": true, "some": true, "were": true,
	"who": true, "did": true, "get": true, "let": true, "say": true,
	"she": true, "too": true, "use": true, "also": true, "just": true,
	"about": true, "would": true, "there": true, "after": true, "other": true,
	"could": true, "these": true, "where": true, "being": true, "does": true,
	"more": true, "very": true, "most": true, "only": true, "such": true,
	"should": true, "over": true, "because": true, "any": true, "here": true,
}

func isStopWord(word string) bool {
	return stopWordSet[word]
}
