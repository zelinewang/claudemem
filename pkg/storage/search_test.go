package storage

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
)

func TestSearch_EmptyString(t *testing.T) {
	store := setupTestStore(t)

	note := models.NewNote("test", "Test Note", "Some content here")
	_, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	// FTS5 rejects empty MATCH
	_, err = store.Search("", "", 10)
	if err == nil {
		t.Errorf("Search with empty query should return error")
	}
}

func TestSearch_FilterByType_Note(t *testing.T) {
	store := setupTestStore(t)

	note := models.NewNote("api", "Authentication Details", "Bearer token authentication guide")
	_, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	// NewSession(title, branch, project, sessionID)
	session := models.NewSession("Auth Session", "main", "myproject", "sid-1")
	session.Summary = "Discussed Bearer token implementation"
	_, err = store.SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	results, err := store.Search("Bearer", "note", 10)
	if err != nil {
		t.Fatalf("Search() failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Search filtered by 'note': got %d results, want 1", len(results))
	}
	if len(results) > 0 && results[0].Type != "note" {
		t.Errorf("Result type = %q, want 'note'", results[0].Type)
	}
}

func TestSearch_FilterByType_Session(t *testing.T) {
	store := setupTestStore(t)

	note := models.NewNote("api", "Rate Limiting Architecture", "Redis cache for rate limiting")
	_, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	session := models.NewSession("Cache Discussion", "main", "myproject", "sid-1")
	session.Summary = "Discussed Redis cache strategies"
	_, err = store.SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	results, err := store.Search("Redis", "session", 10)
	if err != nil {
		t.Fatalf("Search() failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Search filtered by 'session': got %d results, want 1", len(results))
	}
	if len(results) > 0 && results[0].Type != "session" {
		t.Errorf("Result type = %q, want 'session'", results[0].Type)
	}
}

func TestSearch_NoFilter(t *testing.T) {
	store := setupTestStore(t)

	note := models.NewNote("docs", "Database Migration Guide", "Alembic migration details")
	_, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	session := models.NewSession("Migration Planning", "main", "myproject", "sid-1")
	session.Summary = "Planning Alembic migration strategy"
	_, err = store.SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	results, err := store.Search("Alembic", "", 10)
	if err != nil {
		t.Fatalf("Search() failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Search without filter: got %d results, want 2", len(results))
	}

	typeCount := make(map[string]int)
	for _, r := range results {
		typeCount[r.Type]++
	}
	if typeCount["note"] != 1 {
		t.Errorf("Note count = %d, want 1", typeCount["note"])
	}
	if typeCount["session"] != 1 {
		t.Errorf("Session count = %d, want 1", typeCount["session"])
	}
}

func TestSearch_LimitRespected(t *testing.T) {
	store := setupTestStore(t)

	// Add notes in DIFFERENT categories with unique titles to avoid dedup
	categories := []string{"astronomy", "biology", "chemistry", "geology", "physics"}
	for i, cat := range categories {
		note := models.NewNote(cat, fmt.Sprintf("Encyclopedia of %s Volume %d", cat, i+1),
			fmt.Sprintf("This %s article contains limitkeyword information about %s", cat, cat))
		_, err := store.AddNote(note)
		if err != nil {
			t.Fatalf("AddNote() %d failed: %v", i, err)
		}
	}

	results, err := store.Search("limitkeyword", "", 2)
	if err != nil {
		t.Fatalf("Search() failed: %v", err)
	}

	if len(results) > 2 {
		t.Errorf("Search with limit 2: got %d results, want <= 2", len(results))
	}
}

func TestSearch_ResultHasScore(t *testing.T) {
	store := setupTestStore(t)

	note := models.NewNote("test", "Score Test Document", "scoretest scoretest scoretest")
	_, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	results, err := store.Search("scoretest", "", 10)
	if err != nil {
		t.Fatalf("Search() failed: %v", err)
	}

	for i, result := range results {
		if result.Score <= 0 {
			t.Errorf("Result %d: Score = %f, want > 0", i, result.Score)
		}
	}
}

func TestSearch_NoteSearch_CategoryFilter(t *testing.T) {
	store := setupTestStore(t)

	note1 := models.NewNote("api", "Elasticsearch API Reference", "Elasticsearch integration docs")
	_, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() api failed: %v", err)
	}

	note2 := models.NewNote("docs", "Elasticsearch User Guide", "Elasticsearch documentation")
	_, err = store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() docs failed: %v", err)
	}

	results, err := store.SearchNotes("Elasticsearch", "docs", nil)
	if err != nil {
		t.Fatalf("SearchNotes() failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("SearchNotes with category filter: got %d results, want 1", len(results))
	}
	if len(results) > 0 && results[0].Category != "docs" {
		t.Errorf("Result category = %q, want 'docs'", results[0].Category)
	}
}

func TestSearch_SessionSearch_BranchFilter(t *testing.T) {
	store := setupTestStore(t)

	// NewSession(title, branch, project, sessionID) — branch is the 2nd arg
	session1 := models.NewSession("Architecture Review Meeting", "main", "backend-svc", "sid-1")
	session1.Summary = "Reviewed microservice branchfilter architecture"
	_, err := store.SaveSession(session1)
	if err != nil {
		t.Fatalf("SaveSession() main failed: %v", err)
	}

	session2 := models.NewSession("Feature Design Session", "feature-new", "backend-svc", "sid-2")
	session2.Summary = "Designed new branchfilter feature architecture"
	_, err = store.SaveSession(session2)
	if err != nil {
		t.Fatalf("SaveSession() feature failed: %v", err)
	}

	// Search for "branchfilter" on "main" branch only
	opts := SessionListOpts{Branch: "main"}
	results, err := store.SearchSessions("branchfilter", opts)
	if err != nil {
		t.Fatalf("SearchSessions() failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("SearchSessions with branch filter: got %d results, want 1", len(results))
	}
	if len(results) > 0 && results[0].Branch != "main" {
		t.Errorf("Result branch = %q, want 'main'", results[0].Branch)
	}
}

func TestSearch_Preview(t *testing.T) {
	store := setupTestStore(t)

	longContent := "This is content. "
	for i := 0; i < 20; i++ {
		longContent += fmt.Sprintf("Additional text block %d. ", i)
	}
	longContent += "The previewkeyword appears here. "

	note := models.NewNote("test", "Long Document For Preview", longContent)
	_, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	results, err := store.Search("previewkeyword", "", 10)
	if err != nil {
		t.Fatalf("Search() failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	preview := results[0].Preview
	if len(preview) == 0 {
		t.Errorf("Preview should not be empty")
	}
	if len(preview) > 103 {
		t.Errorf("Preview too long: %d chars", len(preview))
	}
}

func TestSearchNotes_WithTags(t *testing.T) {
	store := setupTestStore(t)

	// Use very distinct titles in different categories to avoid dedup
	note1 := models.NewNote("astronomy", "Stellar Classification Reference", "tagfiltertest content about stars")
	note1.Tags = []string{"api", "rest", "v1"}
	_, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() 1 failed: %v", err)
	}

	note2 := models.NewNote("biology", "Cellular Division Patterns", "tagfiltertest content about cells")
	note2.Tags = []string{"api", "graphql", "v2"}
	_, err = store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() 2 failed: %v", err)
	}

	note3 := models.NewNote("chemistry", "Molecular Bond Analysis", "tagfiltertest content about bonds")
	note3.Tags = []string{"api", "rest", "v2"}
	_, err = store.AddNote(note3)
	if err != nil {
		t.Fatalf("AddNote() 3 failed: %v", err)
	}

	// Search for ["api", "rest"] — should match notes 1 and 3
	results, err := store.SearchNotes("tagfiltertest", "", []string{"api", "rest"})
	if err != nil {
		t.Fatalf("SearchNotes() failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("SearchNotes with tag filter: got %d results, want 2", len(results))
	}

	for _, note := range results {
		hasAPI := false
		hasREST := false
		for _, tag := range note.Tags {
			if strings.EqualFold(tag, "api") {
				hasAPI = true
			}
			if strings.EqualFold(tag, "rest") {
				hasREST = true
			}
		}
		if !hasAPI || !hasREST {
			t.Errorf("Note %q missing required tags: tags=%v", note.Title, note.Tags)
		}
	}
}

func TestSanitizeFTSQuery(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"two words", "two words"},
		{"dev-orchestrator", `"dev-orchestrator"`},
		{"auto-amy", `"auto-amy"`},
		{"claude-code-config", `"claude-code-config"`},
		{`"already quoted"`, `"already quoted"`},
		{"dev OR orchestrator", "dev OR orchestrator"},
		{"dev AND orchestrator", "dev AND orchestrator"},
		{"dev NOT orchestrator", "dev NOT orchestrator"},
		{"orchestr*", "orchestr*"},
		{"dev-orch*", `"dev-orch"*`},
		{"prefix:value", `"prefix:value"`},
		{"^initial", `"^initial"`},
		{"mixed-hyphen normal", `"mixed-hyphen" normal`},
		{"", ""},
		{"  spaces  ", "spaces"},
		// Review findings: embedded quotes and parens must be quoted
		{`dev"orchestrator`, `"dev""orchestrator"`},
		{"(grouped)", `"(grouped)"`},
		{`he said "hello"`, `he said "hello"`},
	}

	for _, tt := range tests {
		got := sanitizeFTSQuery(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeFTSQuery(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSearch_HyphenatedQuery(t *testing.T) {
	store := setupTestStore(t)

	note := models.NewNote("tools", "Dev Orchestrator Setup", "The dev-orchestrator manages workflows end-to-end")
	_, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	results, err := store.Search("dev-orchestrator", "", 10)
	if err != nil {
		t.Fatalf("Search('dev-orchestrator') should not error, got: %v", err)
	}
	if len(results) == 0 {
		t.Errorf("Search('dev-orchestrator') returned 0 results, want >= 1")
	}
}

func TestSearchNotes_HyphenatedQuery(t *testing.T) {
	store := setupTestStore(t)

	note := models.NewNote("config", "Claude Code Config Sync", "claude-code-config manages cross-machine settings")
	_, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	results, err := store.SearchNotes("claude-code-config", "", nil)
	if err != nil {
		t.Fatalf("SearchNotes('claude-code-config') should not error, got: %v", err)
	}
	if len(results) == 0 {
		t.Errorf("SearchNotes('claude-code-config') returned 0 results, want >= 1")
	}
}

func TestSearchSessions_HyphenatedQuery(t *testing.T) {
	store := setupTestStore(t)

	session := models.NewSession("Auto-Amy Session", "feat/auto-amy", "vispie", "sid-hyphen")
	session.Summary = "Working on auto-amy pipeline improvements"
	_, err := store.SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession() failed: %v", err)
	}

	opts := SessionListOpts{}
	results, err := store.SearchSessions("auto-amy", opts)
	if err != nil {
		t.Fatalf("SearchSessions('auto-amy') should not error, got: %v", err)
	}
	if len(results) == 0 {
		t.Errorf("SearchSessions('auto-amy') returned 0 results, want >= 1")
	}
}

func TestSearch_EmbeddedQuoteDoesNotCrash(t *testing.T) {
	store := setupTestStore(t)

	note := models.NewNote("test", "Quote Test", "some content with dev orchestrator")
	_, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote() failed: %v", err)
	}

	// This crashed before the fix: embedded " produced unterminated FTS5 string
	_, err = store.Search(`dev"orchestrator`, "", 10)
	if err != nil {
		t.Fatalf("Search with embedded quote should not error, got: %v", err)
	}

	// Parentheses should not be interpreted as FTS5 grouping
	_, err = store.Search("(test)", "", 10)
	if err != nil {
		t.Fatalf("Search with parens should not error, got: %v", err)
	}
}

func TestParseCreatedTimestamp_ISOvsUnix(t *testing.T) {
	tests := []struct {
		input string
		want  string
		err   bool
	}{
		{"2026-04-22T09:12:54Z", "2026-04-22", false},
		{"2026-04-22T04:26:35Z", "2026-04-22", false},
		{"2026-04-22 15:30:00", "2026-04-22", false},
		{"2026-04-22", "2026-04-22", false},
		{"1745309574", "2025-04-22", false},
		{"0", "1970-01-01", false},
		{"", "", true},
		{"garbage", "", true},
	}

	for _, tt := range tests {
		got, err := parseCreatedTimestamp(tt.input)
		if tt.err {
			if err == nil {
				t.Errorf("parseCreatedTimestamp(%q) want error, got %v", tt.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseCreatedTimestamp(%q) unexpected error: %v", tt.input, err)
			continue
		}
		dateStr := got.Format("2006-01-02")
		if dateStr != tt.want {
			t.Errorf("parseCreatedTimestamp(%q) = %s, want %s", tt.input, dateStr, tt.want)
		}
	}
}
