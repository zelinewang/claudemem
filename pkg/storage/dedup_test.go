package storage

import (
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
)

// ========== Note Dedup Tests ==========

func TestNoteDedupExactTitleMatch(t *testing.T) {
	store := setupTestStore(t)

	// Add first note
	note1 := models.NewNote("docs", "API Limits", "100 requests per minute")
	note1.Tags = []string{"api", "limits"}
	result1, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() first failed: %v", err)
	}
	if result1.Action != "created" {
		t.Errorf("First add: Action = %q, want 'created'", result1.Action)
	}

	// Add second note with exact same title and category
	note2 := models.NewNote("docs", "API Limits", "Updated to 200 requests per minute")
	note2.Tags = []string{"api", "rate-limit"}
	result2, err := store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() second failed: %v", err)
	}

	// Should be merged
	if result2.Action != "merged" {
		t.Errorf("Second add: Action = %q, want 'merged'", result2.Action)
	}
	if result2.NoteID != result1.NoteID {
		t.Errorf("Merged note should have same ID: got %q, want %q", result2.NoteID, result1.NoteID)
	}
}

func TestNoteDedupFuzzyMatch_Above50(t *testing.T) {
	store := setupTestStore(t)

	// Add first note
	note1 := models.NewNote("api", "TikTok API Rate Limits", "1000 requests per hour")
	result1, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() first failed: %v", err)
	}

	// Add second note with similar title (3 out of 4 words match = 75%)
	note2 := models.NewNote("api", "TikTok API Limits", "Updated limits info")
	result2, err := store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() second failed: %v", err)
	}

	// Should be merged (75% > 50% threshold)
	if result2.Action != "merged" {
		t.Errorf("Action = %q, want 'merged' (75%% word overlap)", result2.Action)
	}
	if result2.NoteID != result1.NoteID {
		t.Errorf("Should merge to same note ID")
	}
}

func TestNoteDedupFuzzyMatch_Below50(t *testing.T) {
	store := setupTestStore(t)

	// Add first note
	note1 := models.NewNote("frameworks", "Python Testing Framework", "pytest documentation")
	result1, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() first failed: %v", err)
	}

	// Add second note with completely different words (0% overlap)
	note2 := models.NewNote("frameworks", "JavaScript DOM Manipulation", "jQuery and vanilla JS")
	result2, err := store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() second failed: %v", err)
	}

	// Should NOT be merged (0% < 50% threshold)
	if result2.Action != "created" {
		t.Errorf("Action = %q, want 'created' (0%% word overlap)", result2.Action)
	}
	if result2.NoteID == result1.NoteID {
		t.Errorf("Should create separate note, not merge")
	}
}

func TestNoteDedupFuzzyMatch_Boundary(t *testing.T) {
	store := setupTestStore(t)

	// Add first note with 4 significant words
	note1 := models.NewNote("security", "OAuth Token Refresh Flow", "How to refresh OAuth tokens")
	_, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() first failed: %v", err)
	}

	// Add second note with exactly 50% overlap (2 out of 4 words match)
	// "OAuth Token" matches, "Validation Process" doesn't
	note2 := models.NewNote("security", "OAuth Token Validation Process", "Validating OAuth tokens")
	result2, err := store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() second failed: %v", err)
	}

	// Should be merged (50% >= 50% threshold)
	if result2.Action != "merged" {
		t.Errorf("Action = %q, want 'merged' (50%% boundary case)", result2.Action)
	}
}

func TestNoteDedupDifferentCategory(t *testing.T) {
	store := setupTestStore(t)

	// Add note in "api" category
	note1 := models.NewNote("api", "Rate Limits", "API rate limit documentation")
	result1, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() api category failed: %v", err)
	}

	// Add note with same title in "docs" category
	note2 := models.NewNote("docs", "Rate Limits", "General rate limit guide")
	result2, err := store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() docs category failed: %v", err)
	}

	// Should create separate notes (different categories)
	if result2.Action != "created" {
		t.Errorf("Action = %q, want 'created' (different categories)", result2.Action)
	}
	if result2.NoteID == result1.NoteID {
		t.Errorf("Different categories should create separate notes")
	}
}

func TestNoteDedupContentAppend(t *testing.T) {
	store := setupTestStore(t)

	// Add first note
	note1 := models.NewNote("workflows", "Deploy Process", "Step 1: Build\nStep 2: Test")
	result1, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() first failed: %v", err)
	}

	// Add second note with same title
	note2 := models.NewNote("workflows", "Deploy Process", "Step 3: Deploy\nStep 4: Monitor")
	_, err = store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() second failed: %v", err)
	}

	// Retrieve merged note
	merged, err := store.GetNote(result1.NoteID)
	if err != nil {
		t.Fatalf("GetNote() failed: %v", err)
	}

	// Content should contain both versions with separator
	content := merged.Content
	if !strings.Contains(content, "Step 1: Build") {
		t.Errorf("Merged content missing first version")
	}
	if !strings.Contains(content, "Step 3: Deploy") {
		t.Errorf("Merged content missing second version")
	}
	if !strings.Contains(content, "--- Updated") {
		t.Errorf("Merged content missing timestamp separator")
	}
}

func TestNoteDedupTagMerge(t *testing.T) {
	store := setupTestStore(t)

	// Add first note with tags
	note1 := models.NewNote("api", "REST Endpoints", "GET and POST methods")
	note1.Tags = []string{"api", "rest"}
	result1, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() first failed: %v", err)
	}

	// Add second note with overlapping and new tags
	note2 := models.NewNote("api", "REST Endpoints", "PUT and DELETE methods")
	note2.Tags = []string{"api", "Auth"} // "api" overlaps, "Auth" is new
	_, err = store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() second failed: %v", err)
	}

	// Retrieve merged note
	merged, err := store.GetNote(result1.NoteID)
	if err != nil {
		t.Fatalf("GetNote() failed: %v", err)
	}

	// Check tags are merged (case-insensitive dedup)
	tagMap := make(map[string]bool)
	for _, tag := range merged.Tags {
		tagMap[strings.ToLower(tag)] = true
	}

	if len(merged.Tags) != 3 {
		t.Errorf("Merged tags count = %d, want 3", len(merged.Tags))
	}
	if !tagMap["api"] {
		t.Errorf("Missing tag 'api' after merge")
	}
	if !tagMap["rest"] {
		t.Errorf("Missing tag 'rest' after merge")
	}
	if !tagMap["auth"] {
		t.Errorf("Missing tag 'auth' after merge (case-insensitive)")
	}
}

func TestNoteDedupMetadataPreserved(t *testing.T) {
	store := setupTestStore(t)

	// Add first note with metadata
	note1 := models.NewNote("config", "Database Settings", "Connection pool settings")
	note1.Metadata = map[string]string{
		"key1": "val1",
		"key2": "val2",
	}
	result1, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() first failed: %v", err)
	}

	// Add second note with overlapping and new metadata
	note2 := models.NewNote("config", "Database Settings", "Updated pool settings")
	note2.Metadata = map[string]string{
		"key1": "updated", // Override
		"key3": "val3",     // New
	}
	_, err = store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() second failed: %v", err)
	}

	// Retrieve merged note
	merged, err := store.GetNote(result1.NoteID)
	if err != nil {
		t.Fatalf("GetNote() failed: %v", err)
	}

	// Check metadata is properly merged
	if merged.Metadata["key1"] != "updated" {
		t.Errorf("Metadata['key1'] = %q, want 'updated'", merged.Metadata["key1"])
	}
	if merged.Metadata["key2"] != "val2" {
		t.Errorf("Metadata['key2'] = %q, want 'val2' (preserved)", merged.Metadata["key2"])
	}
	if merged.Metadata["key3"] != "val3" {
		t.Errorf("Metadata['key3'] = %q, want 'val3' (new)", merged.Metadata["key3"])
	}
}

func TestNoteDedupEmptyNewContent(t *testing.T) {
	store := setupTestStore(t)

	// Add first note with content
	note1 := models.NewNote("docs", "Setup Guide", "Installation steps here")
	result1, err := store.AddNote(note1)
	if err != nil {
		t.Fatalf("AddNote() first failed: %v", err)
	}

	// Add second note with empty content
	note2 := models.NewNote("docs", "Setup Guide", "")
	_, err = store.AddNote(note2)
	if err != nil {
		t.Fatalf("AddNote() second failed: %v", err)
	}

	// Retrieve merged note
	merged, err := store.GetNote(result1.NoteID)
	if err != nil {
		t.Fatalf("GetNote() failed: %v", err)
	}

	// Original content should be preserved without empty append
	if !strings.Contains(merged.Content, "Installation steps here") {
		t.Errorf("Original content should be preserved")
	}
	// Should not have added separator for empty content
	if strings.Count(merged.Content, "--- Updated") > 0 {
		t.Errorf("Should not append separator for empty content")
	}
}

// ========== Session Dedup Tests ==========

func TestSessionDedupSameKey(t *testing.T) {
	store := setupTestStore(t)

	// Create first session — NewSession(title, branch, project, sessionID)
	session1 := models.NewSession("Test Session 1", "main", "project-a", "sid-1")
	session1.Summary = "First version"
	session1.Tags = []string{"test", "v1"}
	result1, err := store.SaveSession(session1)
	if err != nil {
		t.Fatalf("SaveSession() first failed: %v", err)
	}
	if result1.Action != "created" {
		t.Errorf("First save: Action = %q, want 'created'", result1.Action)
	}

	// Save another session with same date+project+branch (dedup key)
	session2 := models.NewSession("Test Session 2", "main", "project-a", "sid-2")
	session2.Date = session1.Date // same date as first
	session2.Summary = "Second version"
	session2.Tags = []string{"test", "v2"}
	result2, err := store.SaveSession(session2)
	if err != nil {
		t.Fatalf("SaveSession() second failed: %v", err)
	}

	// Should be updated
	if result2.Action != "updated" {
		t.Errorf("Second save: Action = %q, want 'updated'", result2.Action)
	}
	if result2.SessionID != result1.SessionID {
		t.Errorf("Updated session should preserve same ID: got %q, want %q", result2.SessionID, result1.SessionID)
	}
}

func TestSessionDedupDifferentDate(t *testing.T) {
	store := setupTestStore(t)

	// NewSession(title, branch, project, sessionID). Date is auto-set from time.Now()
	session1 := models.NewSession("Daily Standup", "main", "myproject", "sid-1")
	session1.Date = "2024-01-15" // override date
	result1, err := store.SaveSession(session1)
	if err != nil {
		t.Fatalf("SaveSession() first failed: %v", err)
	}

	session2 := models.NewSession("Daily Standup", "main", "myproject", "sid-2")
	session2.Date = "2024-01-16" // different date
	result2, err := store.SaveSession(session2)
	if err != nil {
		t.Fatalf("SaveSession() second failed: %v", err)
	}

	if result2.Action != "created" {
		t.Errorf("Action = %q, want 'created' (different dates)", result2.Action)
	}
	if result2.SessionID == result1.SessionID {
		t.Errorf("Different dates should create separate sessions")
	}
}

func TestSessionDedupDifferentBranch(t *testing.T) {
	store := setupTestStore(t)

	session1 := models.NewSession("Feature Work", "main", "myproject", "sid-1")
	result1, err := store.SaveSession(session1)
	if err != nil {
		t.Fatalf("SaveSession() first failed: %v", err)
	}

	session2 := models.NewSession("Feature Work", "feature-new", "myproject", "sid-2")
	result2, err := store.SaveSession(session2)
	if err != nil {
		t.Fatalf("SaveSession() second failed: %v", err)
	}

	if result2.Action != "created" {
		t.Errorf("Action = %q, want 'created' (different branches)", result2.Action)
	}
	if result2.SessionID == result1.SessionID {
		t.Errorf("Different branches should create separate sessions")
	}
}

func TestSessionDedupDifferentProject(t *testing.T) {
	store := setupTestStore(t)

	session1 := models.NewSession("Code Review", "main", "project-alpha", "sid-1")
	result1, err := store.SaveSession(session1)
	if err != nil {
		t.Fatalf("SaveSession() first failed: %v", err)
	}

	session2 := models.NewSession("Code Review", "main", "project-beta", "sid-2")
	result2, err := store.SaveSession(session2)
	if err != nil {
		t.Fatalf("SaveSession() second failed: %v", err)
	}

	if result2.Action != "created" {
		t.Errorf("Action = %q, want 'created' (different projects)", result2.Action)
	}
	if result2.SessionID == result1.SessionID {
		t.Errorf("Different projects should create separate sessions")
	}
}

func TestSessionDedupOverwritesAllContent(t *testing.T) {
	store := setupTestStore(t)

	// Create first session
	session1 := models.NewSession("Planning Meeting", "main", "project-a", "sid-1")
	session1.Summary = "Version 1 summary"
	session1.WhatHappened = "What happened v1"
	session1.Insights = []string{"Insight v1"}
	result1, err := store.SaveSession(session1)
	if err != nil {
		t.Fatalf("SaveSession() first failed: %v", err)
	}

	// Update with second session — same date+project+branch triggers dedup
	session2 := models.NewSession("Updated Planning", "main", "project-a", "sid-2")
	session2.Date = session1.Date
	session2.Summary = "Version 2 summary"
	session2.WhatHappened = "What happened v2"
	session2.Insights = []string{"Insight v2"}
	_, err = store.SaveSession(session2)
	if err != nil {
		t.Fatalf("SaveSession() second failed: %v", err)
	}

	// Retrieve updated session
	retrieved, err := store.GetSession(result1.SessionID)
	if err != nil {
		t.Fatalf("GetSession() failed: %v", err)
	}

	// Session dedup now MERGES content (not overwrites) to prevent data loss
	// Summary should contain BOTH versions with separator
	if !strings.Contains(retrieved.Summary, "Version 1 summary") {
		t.Errorf("Summary should contain v1, got %q", retrieved.Summary)
	}
	if !strings.Contains(retrieved.Summary, "Version 2 summary") {
		t.Errorf("Summary should contain v2, got %q", retrieved.Summary)
	}
	// WhatHappened should contain both versions
	if !strings.Contains(retrieved.WhatHappened, "What happened v1") {
		t.Errorf("WhatHappened should contain v1, got %q", retrieved.WhatHappened)
	}
	if !strings.Contains(retrieved.WhatHappened, "What happened v2") {
		t.Errorf("WhatHappened should contain v2, got %q", retrieved.WhatHappened)
	}
	// Insights should be merged (both v1 and v2)
	if len(retrieved.Insights) != 2 {
		t.Errorf("Insights should have 2 items (merged), got %v", retrieved.Insights)
	}
	// Title uses the newest
	if retrieved.Title != "Updated Planning" {
		t.Errorf("Title = %q, want 'Updated Planning'", retrieved.Title)
	}
}

// ========== Internal Helper Function Tests ==========

func TestTitleWords(t *testing.T) {
	tests := []struct {
		input    string
		expected map[string]bool
	}{
		{
			// "the" is a stop word — should be EXCLUDED even though len >= 3
			input: "The Quick Brown Fox",
			expected: map[string]bool{
				"quick": true,
				"brown": true,
				"fox":   true,
				// "the" is filtered as stop word
			},
		},
		{
			input:    "",
			expected: map[string]bool{},
		},
		{
			input: "A B CD EFG",
			expected: map[string]bool{
				"efg": true,
				// "a", "b", "cd" skipped (< 3 chars)
			},
		},
		{
			input: "API OAuth REST",
			expected: map[string]bool{
				"api":   true,
				"oauth": true,
				"rest":  true,
			},
		},
		{
			// Stop words filtered: "and", "for", "with" removed
			input: "Design and Build for Production with Docker",
			expected: map[string]bool{
				"design":     true,
				"build":      true,
				"production": true,
				"docker":     true,
				// "and", "for", "with" are stop words
			},
		},
	}

	for _, tt := range tests {
		result := titleWords(tt.input)
		if len(result) != len(tt.expected) {
			t.Errorf("titleWords(%q): got %d words, want %d", tt.input, len(result), len(tt.expected))
		}
		for word := range tt.expected {
			if !result[word] {
				t.Errorf("titleWords(%q): missing word %q", tt.input, word)
			}
		}
	}
}

func TestTitleWords_Empty(t *testing.T) {
	result := titleWords("")
	if len(result) != 0 {
		t.Errorf("titleWords(\"\"): got %d words, want 0", len(result))
	}
}

func TestWordOverlap_Full(t *testing.T) {
	a := map[string]bool{"api": true, "rest": true, "oauth": true}
	b := map[string]bool{"api": true, "rest": true, "oauth": true}

	overlap := wordOverlap(a, b)
	if overlap != 1.0 {
		t.Errorf("wordOverlap full match: got %f, want 1.0", overlap)
	}
}

func TestWordOverlap_Half(t *testing.T) {
	a := map[string]bool{"api": true, "rest": true, "oauth": true, "token": true}
	b := map[string]bool{"api": true, "rest": true, "json": true, "xml": true}

	// 2 shared (api, rest) out of min(4,4) = 0.5
	overlap := wordOverlap(a, b)
	if overlap != 0.5 {
		t.Errorf("wordOverlap half match: got %f, want 0.5", overlap)
	}
}

func TestWordOverlap_Zero(t *testing.T) {
	a := map[string]bool{"python": true, "django": true}
	b := map[string]bool{"javascript": true, "react": true}

	overlap := wordOverlap(a, b)
	if overlap != 0.0 {
		t.Errorf("wordOverlap no match: got %f, want 0.0", overlap)
	}
}

func TestMergeTags_CaseInsensitive(t *testing.T) {
	existing := []string{"API", "rest"}
	new := []string{"api", "Auth"}

	merged := mergeTags(existing, new)

	// Should have 3 unique tags (API/api deduplicated)
	if len(merged) != 3 {
		t.Errorf("mergeTags count = %d, want 3", len(merged))
	}

	// Convert to lowercase map for checking
	tagMap := make(map[string]bool)
	for _, tag := range merged {
		tagMap[strings.ToLower(tag)] = true
	}

	if !tagMap["api"] {
		t.Errorf("Missing 'api' in merged tags")
	}
	if !tagMap["rest"] {
		t.Errorf("Missing 'rest' in merged tags")
	}
	if !tagMap["auth"] {
		t.Errorf("Missing 'auth' in merged tags")
	}
}

func TestMergeTags_Empty(t *testing.T) {
	// Empty + non-empty
	merged1 := mergeTags([]string{}, []string{"tag1"})
	if len(merged1) != 1 || merged1[0] != "tag1" {
		t.Errorf("mergeTags([], [tag1]): got %v, want [tag1]", merged1)
	}

	// Non-empty + empty
	merged2 := mergeTags([]string{"tag1"}, []string{})
	if len(merged2) != 1 || merged2[0] != "tag1" {
		t.Errorf("mergeTags([tag1], []): got %v, want [tag1]", merged2)
	}

	// Both empty
	merged3 := mergeTags([]string{}, []string{})
	if len(merged3) != 0 {
		t.Errorf("mergeTags([], []): got %v, want []", merged3)
	}
}