package storage

import (
	"strings"
	"testing"
	"time"

	"github.com/zelinewang/claudemem/pkg/models"
)

// Helper function to compare notes
func compareNotes(t *testing.T, got, want *models.Note) {
	t.Helper()
	if got.ID != want.ID {
		t.Errorf("Note ID = %q, want %q", got.ID, want.ID)
	}
	if got.Title != want.Title {
		t.Errorf("Note Title = %q, want %q", got.Title, want.Title)
	}
	if got.Category != want.Category {
		t.Errorf("Note Category = %q, want %q", got.Category, want.Category)
	}
	if got.Content != want.Content {
		t.Errorf("Note Content = %q, want %q", got.Content, want.Content)
	}
	if len(got.Tags) != len(want.Tags) {
		t.Errorf("Note Tags length = %d, want %d", len(got.Tags), len(want.Tags))
	} else {
		for i := range got.Tags {
			if got.Tags[i] != want.Tags[i] {
				t.Errorf("Note Tags[%d] = %q, want %q", i, got.Tags[i], want.Tags[i])
			}
		}
	}
}

// Helper function to compare sessions
func compareSessions(t *testing.T, got, want *models.Session) {
	t.Helper()
	if got.ID != want.ID {
		t.Errorf("Session ID = %q, want %q", got.ID, want.ID)
	}
	if got.Title != want.Title {
		t.Errorf("Session Title = %q, want %q", got.Title, want.Title)
	}
	if got.Date != want.Date {
		t.Errorf("Session Date = %q, want %q", got.Date, want.Date)
	}
	if got.Branch != want.Branch {
		t.Errorf("Session Branch = %q, want %q", got.Branch, want.Branch)
	}
	if got.Project != want.Project {
		t.Errorf("Session Project = %q, want %q", got.Project, want.Project)
	}
	if got.SessionID != want.SessionID {
		t.Errorf("Session SessionID = %q, want %q", got.SessionID, want.SessionID)
	}
	if got.Summary != want.Summary {
		t.Errorf("Session Summary = %q, want %q", got.Summary, want.Summary)
	}
	if got.WhatHappened != want.WhatHappened {
		t.Errorf("Session WhatHappened = %q, want %q", got.WhatHappened, want.WhatHappened)
	}
}

func TestFormatParseNote_RoundTrip(t *testing.T) {
	// Create a note with all fields
	note := &models.Note{
		ID:       "test-note-123",
		Type:     "note",
		Category: "api-docs",
		Title:    "REST API Guidelines",
		Content:  "Use proper HTTP status codes.\n\nAlways version your APIs.",
		Tags:     []string{"api", "rest", "guidelines"},
		Created:  time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		Updated:  time.Date(2024, 1, 16, 14, 20, 0, 0, time.UTC),
		Metadata: map[string]string{
			"author": "john",
			"status": "draft",
		},
	}

	// Format to markdown
	markdown := FormatNoteMarkdown(note)

	// Parse back
	parsed, err := ParseNoteMarkdown([]byte(markdown))
	if err != nil {
		t.Fatalf("ParseNoteMarkdown() failed: %v", err)
	}

	// Compare all fields
	compareNotes(t, parsed, note)

	// Check metadata
	if len(parsed.Metadata) != len(note.Metadata) {
		t.Errorf("Metadata length = %d, want %d", len(parsed.Metadata), len(note.Metadata))
	}
	for k, v := range note.Metadata {
		if parsed.Metadata[k] != v {
			t.Errorf("Metadata[%q] = %q, want %q", k, parsed.Metadata[k], v)
		}
	}
}

func TestFormatParseNote_WithSessionID(t *testing.T) {
	note := models.NewNote("workflows", "Auth Flow", "JWT authentication process")
	note.Metadata["session_id"] = "ref-123"

	// Format and parse
	markdown := FormatNoteMarkdown(note)
	parsed, err := ParseNoteMarkdown([]byte(markdown))
	if err != nil {
		t.Fatalf("ParseNoteMarkdown() failed: %v", err)
	}

	// Verify session_id is preserved
	if parsed.Metadata["session_id"] != "ref-123" {
		t.Errorf("Metadata['session_id'] = %q, want 'ref-123'", parsed.Metadata["session_id"])
	}
}

func TestFormatParseNote_EmptyMetadata(t *testing.T) {
	note := models.NewNote("docs", "Empty Meta", "Content here")
	note.Metadata = make(map[string]string) // explicitly empty

	// Format and parse
	markdown := FormatNoteMarkdown(note)
	parsed, err := ParseNoteMarkdown([]byte(markdown))
	if err != nil {
		t.Fatalf("ParseNoteMarkdown() failed: %v", err)
	}

	// Metadata should exist but be empty
	if parsed.Metadata == nil {
		t.Errorf("Metadata should not be nil")
	}
	if len(parsed.Metadata) != 0 {
		t.Errorf("Metadata should be empty, got %v", parsed.Metadata)
	}
}

func TestFormatParseSession_RoundTrip_AllSections(t *testing.T) {
	session := &models.Session{
		ID:        "session-456",
		Type:      "session",
		Title:     "Debug Authentication Issue",
		Date:      "2024-01-15",
		Branch:    "fix/auth-bug",
		Project:   "myapp",
		SessionID: "claude-session-789",
		Tags:      []string{"bug", "auth"},
		Created:   time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
		Summary:   "Fixed JWT token expiration bug",
		WhatHappened: `We investigated the authentication failure reports from users.
Discovered that JWT tokens were expiring 1 hour early due to timezone mismatch.
Applied fix to use UTC consistently.`,
		Decisions: []string{
			"Use UTC for all token timestamps",
			"Add timezone validation in tests",
		},
		Changes: []models.FileChange{
			{Path: "/src/auth/jwt.go", Description: "Fixed timezone handling"},
			{Path: "/tests/auth_test.go", Description: "Added timezone tests"},
		},
		Problems: []models.ProblemSolution{
			{Problem: "Tokens expiring early", Solution: "Use UTC instead of local time"},
			{Problem: "No test coverage", Solution: "Added comprehensive tests"},
		},
		Insights: []string{
			"Always use UTC for distributed systems",
			"Timezone bugs are hard to reproduce locally",
			"Need better logging for auth failures",
		},
		Questions: []string{
			"Should we migrate existing tokens?",
			"How to handle users in different timezones?",
		},
		NextSteps: []string{
			"Monitor error rates for 24 hours",
			"Write migration script if needed",
			"Update documentation",
		},
		RelatedNotes: []models.RelatedNote{
			{ID: "note-abc123", Title: "JWT Best Practices", Category: "security"},
			{ID: "note-def456789012", Title: "Timezone Handling Guide", Category: "engineering"},
		},
	}

	// Format to markdown
	markdown := FormatSessionMarkdown(session)

	// Verify all sections are present in markdown
	expectedSections := []string{
		"## Summary",
		"## What Happened",
		"## Key Decisions",
		"## What Changed",
		"## Problems & Solutions",
		"## Learning Insights",
		"## Questions Raised",
		"## Next Steps",
		"## Related Notes",
	}
	for _, section := range expectedSections {
		if !strings.Contains(markdown, section) {
			t.Errorf("Markdown missing section %q", section)
		}
	}

	// Parse back
	parsed, err := ParseSessionMarkdown([]byte(markdown))
	if err != nil {
		t.Fatalf("ParseSessionMarkdown() failed: %v", err)
	}

	// Compare basic fields
	compareSessions(t, parsed, session)

	// Check arrays have same length
	if len(parsed.Decisions) != len(session.Decisions) {
		t.Errorf("Decisions length = %d, want %d", len(parsed.Decisions), len(session.Decisions))
	}
	if len(parsed.Changes) != len(session.Changes) {
		t.Errorf("Changes length = %d, want %d", len(parsed.Changes), len(session.Changes))
	}
	if len(parsed.Problems) != len(session.Problems) {
		t.Errorf("Problems length = %d, want %d", len(parsed.Problems), len(session.Problems))
	}
	if len(parsed.Insights) != len(session.Insights) {
		t.Errorf("Insights length = %d, want %d", len(parsed.Insights), len(session.Insights))
	}
	if len(parsed.Questions) != len(session.Questions) {
		t.Errorf("Questions length = %d, want %d", len(parsed.Questions), len(session.Questions))
	}
	if len(parsed.NextSteps) != len(session.NextSteps) {
		t.Errorf("NextSteps length = %d, want %d", len(parsed.NextSteps), len(session.NextSteps))
	}
	if len(parsed.RelatedNotes) != len(session.RelatedNotes) {
		t.Errorf("RelatedNotes length = %d, want %d", len(parsed.RelatedNotes), len(session.RelatedNotes))
	}

	// Check specific content
	for i, insight := range session.Insights {
		if i < len(parsed.Insights) && parsed.Insights[i] != insight {
			t.Errorf("Insights[%d] = %q, want %q", i, parsed.Insights[i], insight)
		}
	}

	// Check related notes preserved correctly — full UUIDs now stored (not truncated)
	for i, rn := range session.RelatedNotes {
		if i < len(parsed.RelatedNotes) {
			if parsed.RelatedNotes[i].ID != rn.ID {
				t.Errorf("RelatedNotes[%d].ID = %q, want %q (full UUID should be preserved)", i, parsed.RelatedNotes[i].ID, rn.ID)
			}
			if parsed.RelatedNotes[i].Title != rn.Title {
				t.Errorf("RelatedNotes[%d].Title = %q, want %q", i, parsed.RelatedNotes[i].Title, rn.Title)
			}
			if parsed.RelatedNotes[i].Category != rn.Category {
				t.Errorf("RelatedNotes[%d].Category = %q, want %q", i, parsed.RelatedNotes[i].Category, rn.Category)
			}
		}
	}
}

func TestFormatParseSession_OnlySummary(t *testing.T) {
	session := models.NewSession("Quick Fix", "main", "project", "sid-123")
	session.Summary = "Fixed a typo in README"
	// Leave all other fields empty

	markdown := FormatSessionMarkdown(session)

	// Should only have Summary section, not empty sections
	if !strings.Contains(markdown, "## Summary") {
		t.Errorf("Markdown should contain Summary section")
	}
	if strings.Contains(markdown, "## What Happened") {
		t.Errorf("Markdown should not contain empty What Happened section")
	}
	if strings.Contains(markdown, "## Learning Insights") {
		t.Errorf("Markdown should not contain empty Insights section")
	}
	if strings.Contains(markdown, "## Related Notes") {
		t.Errorf("Markdown should not contain empty Related Notes section")
	}

	// Parse back
	parsed, err := ParseSessionMarkdown([]byte(markdown))
	if err != nil {
		t.Fatalf("ParseSessionMarkdown() failed: %v", err)
	}

	if parsed.Summary != "Fixed a typo in README" {
		t.Errorf("Summary = %q, want 'Fixed a typo in README'", parsed.Summary)
	}
	if parsed.WhatHappened != "" {
		t.Errorf("WhatHappened should be empty, got %q", parsed.WhatHappened)
	}
	if len(parsed.Insights) != 0 {
		t.Errorf("Insights should be empty, got %v", parsed.Insights)
	}
	if len(parsed.RelatedNotes) != 0 {
		t.Errorf("RelatedNotes should be empty, got %v", parsed.RelatedNotes)
	}
}

func TestFormatParseSession_WhatHappened_Preserves(t *testing.T) {
	session := models.NewSession("Complex Debug", "main", "app", "sid")
	session.WhatHappened = `1. Started by examining the error logs
2. Found multiple timeout errors in the database layer
3. Traced the issue to a missing index on the users table

The fix was straightforward once we identified the root cause.
Performance improved by 10x after adding the index.`

	markdown := FormatSessionMarkdown(session)
	parsed, err := ParseSessionMarkdown([]byte(markdown))
	if err != nil {
		t.Fatalf("ParseSessionMarkdown() failed: %v", err)
	}

	// Should preserve the exact formatting including numbered list
	if parsed.WhatHappened != session.WhatHappened {
		t.Errorf("WhatHappened not preserved exactly.\nGot:\n%q\nWant:\n%q",
			parsed.WhatHappened, session.WhatHappened)
	}
}

func TestFormatParseSession_RelatedNotes_RoundTrip(t *testing.T) {
	session := models.NewSession("Test", "main", "proj", "sid")
	session.RelatedNotes = []models.RelatedNote{
		{ID: "short123", Title: "Quick Note", Category: "docs"},
		{ID: "verylongid1234567890", Title: "Long ID Note", Category: "api"},
		{ID: "note-with-special", Title: "Title (with parens)", Category: "test"},
	}

	markdown := FormatSessionMarkdown(session)
	parsed, err := ParseSessionMarkdown([]byte(markdown))
	if err != nil {
		t.Fatalf("ParseSessionMarkdown() failed: %v", err)
	}

	if len(parsed.RelatedNotes) != 3 {
		t.Fatalf("RelatedNotes length = %d, want 3", len(parsed.RelatedNotes))
	}

	// First note - full ID preserved (no truncation)
	if parsed.RelatedNotes[0].ID != "short123" {
		t.Errorf("RelatedNotes[0].ID = %q, want 'short123'", parsed.RelatedNotes[0].ID)
	}
	if parsed.RelatedNotes[0].Title != "Quick Note" {
		t.Errorf("RelatedNotes[0].Title = %q, want 'Quick Note'", parsed.RelatedNotes[0].Title)
	}

	// Third note - title with parens
	if parsed.RelatedNotes[2].Title != "Title (with parens)" {
		t.Errorf("RelatedNotes[2].Title = %q, want 'Title (with parens)'", parsed.RelatedNotes[2].Title)
	}
}

func TestFormatParseSession_Insights_RoundTrip(t *testing.T) {
	session := models.NewSession("Learning", "main", "proj", "sid")
	session.Insights = []string{
		"Database indexes are critical for performance",
		"Always profile before optimizing",
		"UTC timestamps prevent timezone bugs",
	}

	markdown := FormatSessionMarkdown(session)
	parsed, err := ParseSessionMarkdown([]byte(markdown))
	if err != nil {
		t.Fatalf("ParseSessionMarkdown() failed: %v", err)
	}

	if len(parsed.Insights) != 3 {
		t.Fatalf("Insights length = %d, want 3", len(parsed.Insights))
	}

	for i, insight := range session.Insights {
		if parsed.Insights[i] != insight {
			t.Errorf("Insights[%d] = %q, want %q", i, parsed.Insights[i], insight)
		}
	}
}

func TestParseRelatedNoteLine_Standard(t *testing.T) {
	line := "`abc12345` — \"My Note\" (api-specs)"
	rn := parseRelatedNoteLine(line)

	if rn.ID != "abc12345" {
		t.Errorf("ID = %q, want 'abc12345'", rn.ID)
	}
	if rn.Title != "My Note" {
		t.Errorf("Title = %q, want 'My Note'", rn.Title)
	}
	if rn.Category != "api-specs" {
		t.Errorf("Category = %q, want 'api-specs'", rn.Category)
	}
}

func TestParseRelatedNoteLine_NoCategory(t *testing.T) {
	line := "`abc12345` — \"My Note\""
	rn := parseRelatedNoteLine(line)

	if rn.ID != "abc12345" {
		t.Errorf("ID = %q, want 'abc12345'", rn.ID)
	}
	if rn.Title != "My Note" {
		t.Errorf("Title = %q, want 'My Note'", rn.Title)
	}
	if rn.Category != "" {
		t.Errorf("Category = %q, want empty", rn.Category)
	}
}

func TestParseRelatedNoteLine_TitleWithParens(t *testing.T) {
	line := "`abc12345` — \"TikTok Limits (v2)\" (api-specs)"
	rn := parseRelatedNoteLine(line)

	if rn.ID != "abc12345" {
		t.Errorf("ID = %q, want 'abc12345'", rn.ID)
	}
	if rn.Title != "TikTok Limits (v2)" {
		t.Errorf("Title = %q, want 'TikTok Limits (v2)'", rn.Title)
	}
	if rn.Category != "api-specs" {
		t.Errorf("Category = %q, want 'api-specs'", rn.Category)
	}
}

func TestParseRelatedNoteLine_EmptyLine(t *testing.T) {
	rn := parseRelatedNoteLine("")
	if rn.ID != "" {
		t.Errorf("ID should be empty for empty line, got %q", rn.ID)
	}
}

func TestParseRelatedNoteLine_NoBackticks(t *testing.T) {
	line := "just plain text without proper format"
	rn := parseRelatedNoteLine(line)
	if rn.ID != "" {
		t.Errorf("ID should be empty for improperly formatted line, got %q", rn.ID)
	}
}

func TestParseSessionMarkdown_BackwardCompat(t *testing.T) {
	// Old format session with only Summary, Key Decisions, What Changed
	oldMarkdown := `---
id: old-session-123
type: session
title: Old Format Session
date: "2024-01-01"
branch: main
project: oldproject
session_id: old-sid
tags: [legacy]
created: "2024-01-01T10:00:00Z"
---

## Summary
This is an old format session.

## Key Decisions
- Decision one
- Decision two

## What Changed
- ` + "`/old/file.go`" + ` — Updated logic
`

	parsed, err := ParseSessionMarkdown([]byte(oldMarkdown))
	if err != nil {
		t.Fatalf("ParseSessionMarkdown() should handle old format, got error: %v", err)
	}

	// Old fields should be parsed
	if parsed.Summary != "This is an old format session." {
		t.Errorf("Summary = %q, want 'This is an old format session.'", parsed.Summary)
	}
	if len(parsed.Decisions) != 2 {
		t.Errorf("Should parse old Decisions, got %d items", len(parsed.Decisions))
	}
	if len(parsed.Changes) != 1 {
		t.Errorf("Should parse old Changes, got %d items", len(parsed.Changes))
	}

	// New fields should be empty/default
	if parsed.WhatHappened != "" {
		t.Errorf("WhatHappened should be empty for old format, got %q", parsed.WhatHappened)
	}
	if parsed.Insights == nil {
		t.Errorf("Insights should not be nil")
	}
	if len(parsed.Insights) != 0 {
		t.Errorf("Insights should be empty for old format, got %v", parsed.Insights)
	}
	if parsed.RelatedNotes == nil {
		t.Errorf("RelatedNotes should not be nil")
	}
	if len(parsed.RelatedNotes) != 0 {
		t.Errorf("RelatedNotes should be empty for old format, got %v", parsed.RelatedNotes)
	}
}

func TestParseNoteMarkdown_BackwardCompat(t *testing.T) {
	// Old format note without metadata field
	oldMarkdown := `---
id: old-note-123
type: note
category: docs
title: Old Note
tags: [old]
created: "2024-01-01T10:00:00Z"
updated: "2024-01-01T10:00:00Z"
---

This is the content of an old note without metadata field.`

	parsed, err := ParseNoteMarkdown([]byte(oldMarkdown))
	if err != nil {
		t.Fatalf("ParseNoteMarkdown() should handle old format, got error: %v", err)
	}

	// Basic fields should be parsed
	if parsed.ID != "old-note-123" {
		t.Errorf("ID = %q, want 'old-note-123'", parsed.ID)
	}
	if parsed.Title != "Old Note" {
		t.Errorf("Title = %q, want 'Old Note'", parsed.Title)
	}
	if parsed.Content != "This is the content of an old note without metadata field." {
		t.Errorf("Content not parsed correctly")
	}

	// Metadata should exist but be empty
	if parsed.Metadata == nil {
		t.Errorf("Metadata should not be nil for backward compat")
	}
	if len(parsed.Metadata) != 0 {
		t.Errorf("Metadata should be empty for old format, got %v", parsed.Metadata)
	}
}
