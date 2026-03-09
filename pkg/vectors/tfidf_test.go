package vectors

import (
	"math"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		input    string
		expected int // minimum expected token count
	}{
		{"hello world", 2},
		{"The quick brown fox", 3}, // "the" is stop word
		{"API rate-limiting config", 3},
		{"a b c", 0}, // all too short
		{"", 0},
		{"Go is great for building CLI tools", 5},
	}

	for _, tt := range tests {
		tokens := tokenize(tt.input)
		if len(tokens) < tt.expected {
			t.Errorf("tokenize(%q) returned %d tokens, expected at least %d", tt.input, len(tokens), tt.expected)
		}
	}
}

func TestTokenize_StopWords(t *testing.T) {
	tokens := tokenize("the and for are but not")
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens from stop words, got %d: %v", len(tokens), tokens)
	}
}

func TestVectorizer_BuildVocab(t *testing.T) {
	v := NewVectorizer()
	docs := []string{
		"authentication via OAuth tokens",
		"database migration with Alembic",
		"API rate limiting configuration",
	}

	v.BuildVocab(docs)

	if v.VocabSize() == 0 {
		t.Fatal("vocabulary should not be empty after BuildVocab")
	}
	if v.TotalDocs() != 3 {
		t.Errorf("expected 3 total docs, got %d", v.TotalDocs())
	}
}

func TestVectorizer_Vectorize(t *testing.T) {
	v := NewVectorizer()
	docs := []string{
		"authentication via OAuth tokens",
		"database migration with Alembic",
		"API rate limiting configuration",
		"user authentication and session management",
		"Redis cache configuration for sessions",
	}

	v.BuildVocab(docs)

	vec := v.Vectorize("authentication tokens OAuth")
	if vec == nil {
		t.Fatal("Vectorize returned nil")
	}
	if len(vec) != v.VocabSize() {
		t.Errorf("vector dimension %d != vocab size %d", len(vec), v.VocabSize())
	}

	// Vector should be L2-normalized (magnitude ~= 1.0)
	var mag float64
	for _, val := range vec {
		mag += float64(val) * float64(val)
	}
	mag = math.Sqrt(mag)
	if mag > 0 && math.Abs(mag-1.0) > 0.01 {
		t.Errorf("vector magnitude = %f, expected ~1.0", mag)
	}
}

func TestVectorizer_Vectorize_EmptyVocab(t *testing.T) {
	v := NewVectorizer()
	vec := v.Vectorize("some text")
	if vec != nil {
		t.Errorf("expected nil from empty vocab, got %v", vec)
	}
}

func TestCosineSimilarity_Identical(t *testing.T) {
	a := []float32{0.5, 0.5, 0.5, 0.5}
	normalize(a)
	sim := CosineSimilarity(a, a)
	if math.Abs(float64(sim)-1.0) > 0.001 {
		t.Errorf("self-similarity should be ~1.0, got %f", sim)
	}
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	sim := CosineSimilarity(a, b)
	if math.Abs(float64(sim)) > 0.001 {
		t.Errorf("orthogonal similarity should be ~0, got %f", sim)
	}
}

func TestCosineSimilarity_DifferentLengths(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{1, 0, 0}
	sim := CosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("different length vectors should return 0, got %f", sim)
	}
}

func TestSemanticSimilarity_RelatedDocs(t *testing.T) {
	v := NewVectorizer()
	docs := []string{
		"authentication via OAuth tokens and JWT",
		"database migration with Alembic and SQLAlchemy",
		"API rate limiting and throttling configuration",
		"user login and session authentication management",
		"PostgreSQL database schema and table design",
		"Redis cache setup and configuration tuning",
	}

	v.BuildVocab(docs)

	// "auth" query should be more similar to auth-related docs than DB docs
	queryVec := v.Vectorize("authentication login session tokens")
	authVec := v.Vectorize("authentication via OAuth tokens and JWT")
	dbVec := v.Vectorize("database migration with Alembic and SQLAlchemy")

	authSim := CosineSimilarity(queryVec, authVec)
	dbSim := CosineSimilarity(queryVec, dbVec)

	if authSim <= dbSim {
		t.Errorf("auth query should be more similar to auth doc (%.4f) than db doc (%.4f)", authSim, dbSim)
	}
}

func TestVectorizer_ExportImportState(t *testing.T) {
	v := NewVectorizer()
	docs := []string{
		"authentication via OAuth tokens",
		"database migration with Alembic",
	}
	v.BuildVocab(docs)

	state := v.ExportState()

	v2 := NewVectorizer()
	v2.ImportState(state)

	if v2.VocabSize() != v.VocabSize() {
		t.Errorf("imported vocab size %d != original %d", v2.VocabSize(), v.VocabSize())
	}
	if v2.TotalDocs() != v.TotalDocs() {
		t.Errorf("imported total docs %d != original %d", v2.TotalDocs(), v.TotalDocs())
	}

	// Vectors from original and imported should be identical
	vec1 := v.Vectorize("authentication tokens")
	vec2 := v2.Vectorize("authentication tokens")
	sim := CosineSimilarity(vec1, vec2)
	if math.Abs(float64(sim)-1.0) > 0.001 {
		t.Errorf("vectors from original and imported should be identical, got similarity %f", sim)
	}
}

func TestNormalize_ZeroVector(t *testing.T) {
	vec := []float32{0, 0, 0}
	normalize(vec) // should not panic
	for i, v := range vec {
		if v != 0 {
			t.Errorf("zero vector element %d should remain 0, got %f", i, v)
		}
	}
}
