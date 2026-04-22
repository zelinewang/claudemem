package storage

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/zelinewang/claudemem/pkg/models"
)

// ─── noteSessionID helper ───

func TestNoteSessionID_WithMetadata(t *testing.T) {
	note := models.NewNote("cat", "title", "content")
	note.Metadata["session_id"] = "ref-123"
	if got := noteSessionID(note); got != "ref-123" {
		t.Errorf("noteSessionID = %q, want %q", got, "ref-123")
	}
}

func TestNoteSessionID_NilMetadata(t *testing.T) {
	note := &models.Note{Metadata: nil}
	if got := noteSessionID(note); got != "" {
		t.Errorf("noteSessionID with nil map = %q, want empty", got)
	}
}

func TestNoteSessionID_EmptyMetadata(t *testing.T) {
	note := models.NewNote("cat", "title", "content")
	// Metadata is initialized but has no session_id key
	if got := noteSessionID(note); got != "" {
		t.Errorf("noteSessionID without key = %q, want empty", got)
	}
}

// ─── FindNotesBySessionRef ───

func TestFindNotesBySessionRef_MatchingNotes(t *testing.T) {
	store := setupTestStore(t)

	// Create 2 notes with same session ref
	n1 := models.NewNote("cat-a", "Note Alpha", "content a")
	n1.Metadata["session_id"] = "ref-A"
	store.AddNote(n1)

	n2 := models.NewNote("cat-b", "Note Beta", "content b")
	n2.Metadata["session_id"] = "ref-A"
	store.AddNote(n2)

	results, err := store.FindNotesBySessionRef("ref-A")
	if err != nil {
		t.Fatalf("FindNotesBySessionRef error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("got %d results, want 2", len(results))
	}

	// Check both notes are present
	titles := map[string]bool{}
	for _, rn := range results {
		titles[rn.Title] = true
	}
	if !titles["Note Alpha"] || !titles["Note Beta"] {
		t.Errorf("missing expected notes, got titles: %v", titles)
	}
}

func TestFindNotesBySessionRef_NoMatch(t *testing.T) {
	store := setupTestStore(t)

	n := models.NewNote("cat", "Note X", "content")
	n.Metadata["session_id"] = "ref-A"
	store.AddNote(n)

	results, err := store.FindNotesBySessionRef("nonexistent")
	if err != nil {
		t.Fatalf("FindNotesBySessionRef error: %v", err)
	}
	if results != nil {
		t.Errorf("got %d results, want nil", len(results))
	}
}

func TestFindNotesBySessionRef_EmptyRef(t *testing.T) {
	store := setupTestStore(t)

	results, err := store.FindNotesBySessionRef("")
	if err != nil {
		t.Fatalf("FindNotesBySessionRef error: %v", err)
	}
	if results != nil {
		t.Errorf("got %v, want nil for empty ref", results)
	}
}

func TestFindNotesBySessionRef_ExcludesDifferentRef(t *testing.T) {
	store := setupTestStore(t)

	n1 := models.NewNote("cat", "Included", "content")
	n1.Metadata["session_id"] = "ref-X"
	store.AddNote(n1)

	n2 := models.NewNote("cat", "Excluded", "content")
	n2.Metadata["session_id"] = "ref-Y"
	store.AddNote(n2)

	results, err := store.FindNotesBySessionRef("ref-X")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Title != "Included" {
		t.Errorf("got title %q, want %q", results[0].Title, "Included")
	}
}

// ─── AddNote / UpdateNote session_id in DB ───

func TestAddNote_WritesSessionIDToDB(t *testing.T) {
	store := setupTestStore(t)

	note := models.NewNote("cat", "DB Test", "content")
	note.Metadata["session_id"] = "db-ref-001"
	result, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote error: %v", err)
	}

	// Query DB directly
	var dbSID string
	store.db.QueryRow("SELECT session_id FROM entries WHERE id = ?", result.NoteID).Scan(&dbSID)
	if dbSID != "db-ref-001" {
		t.Errorf("DB session_id = %q, want %q", dbSID, "db-ref-001")
	}
}

func TestAddNote_EmptySessionID_WritesEmptyString(t *testing.T) {
	store := setupTestStore(t)

	note := models.NewNote("cat", "No SID", "content")
	// No session_id set
	result, err := store.AddNote(note)
	if err != nil {
		t.Fatalf("AddNote error: %v", err)
	}

	var dbSID string
	store.db.QueryRow("SELECT session_id FROM entries WHERE id = ?", result.NoteID).Scan(&dbSID)
	if dbSID != "" {
		t.Errorf("DB session_id = %q, want empty", dbSID)
	}
}

func TestUpdateNote_PreservesSessionID(t *testing.T) {
	store := setupTestStore(t)

	note := models.NewNote("cat", "Update Test", "original")
	note.Metadata["session_id"] = "preserved-ref"
	store.AddNote(note)

	// Update content but don't change metadata
	note.Content = "updated content"
	store.UpdateNote(note)

	var dbSID string
	store.db.QueryRow("SELECT session_id FROM entries WHERE id = ?", note.ID).Scan(&dbSID)
	if dbSID != "preserved-ref" {
		t.Errorf("DB session_id after update = %q, want %q", dbSID, "preserved-ref")
	}
}

func TestNoteDedup_UpdatesSessionIDToNewer(t *testing.T) {
	store := setupTestStore(t)

	// First note with session_id "old"
	n1 := models.NewNote("cat", "Dedup Target", "content v1")
	n1.Metadata["session_id"] = "old-ref"
	r1, _ := store.AddNote(n1)

	// Second note with same category+title → merge, newer session_id wins
	n2 := models.NewNote("cat", "Dedup Target", "content v2")
	n2.Metadata["session_id"] = "new-ref"
	r2, _ := store.AddNote(n2)

	if r2.Action != "merged" {
		t.Fatalf("expected merge, got %q", r2.Action)
	}

	var dbSID string
	store.db.QueryRow("SELECT session_id FROM entries WHERE id = ?", r1.NoteID).Scan(&dbSID)
	if dbSID != "new-ref" {
		t.Errorf("DB session_id after merge = %q, want %q", dbSID, "new-ref")
	}
}

// ─── Auto-discovery (SaveSession + FindNotesBySessionRef) ───

func TestAutoDiscovery_FindsLinkedNotes(t *testing.T) {
	store := setupTestStore(t)

	// Create notes linked to a session ref
	n1 := models.NewNote("cat-1", "Disc Note 1", "content 1")
	n1.Metadata["session_id"] = "auto-disc-ref"
	store.AddNote(n1)

	n2 := models.NewNote("cat-2", "Disc Note 2", "content 2")
	n2.Metadata["session_id"] = "auto-disc-ref"
	store.AddNote(n2)

	// FindNotesBySessionRef should find both
	notes, err := store.FindNotesBySessionRef("auto-disc-ref")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(notes) != 2 {
		t.Errorf("FindNotesBySessionRef returned %d, want 2", len(notes))
	}

	// Now save a session with the same session_id
	session := models.NewSession("Test Session", "main", "proj", "auto-disc-ref")
	session.Summary = "test summary"
	result, err := store.SaveSession(session)
	if err != nil {
		t.Fatalf("SaveSession error: %v", err)
	}
	if result.Action != "created" {
		t.Errorf("expected created, got %q", result.Action)
	}

	// Note: auto-discovery happens in cmd/session_save.go, not in storage layer.
	// We test FindNotesBySessionRef works correctly here.
	// The integration is tested via shell tests.
}

func TestAutoDiscovery_NoMatchingNotes_NoError(t *testing.T) {
	store := setupTestStore(t)

	results, err := store.FindNotesBySessionRef("no-matching-ref")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil, got %d results", len(results))
	}
}

// ─── ResolveSessionID ───

func TestResolveSessionID_NoExistingSession_GeneratesNew(t *testing.T) {
	store := setupTestStore(t)

	sid, existing, err := store.ResolveSessionID("new-project", 4*time.Hour)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if existing {
		t.Error("expected existing=false for fresh project")
	}
	if sid == "" {
		t.Error("expected non-empty session ID")
	}
	// Verify format: YYYYMMDD-HHMMSS-hexbytes
	if !strings.Contains(sid, "-") {
		t.Errorf("session ID %q doesn't match expected format", sid)
	}
}

func TestResolveSessionID_ExistingSession_Reuses(t *testing.T) {
	store := setupTestStore(t)

	// Create a session first
	session := models.NewSession("Existing", "main", "my-project", "original-sid")
	session.Summary = "existing session"
	store.SaveSession(session)

	// Resolve should find it
	sid, existing, err := store.ResolveSessionID("my-project", 4*time.Hour)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !existing {
		t.Error("expected existing=true")
	}
	if sid != "original-sid" {
		t.Errorf("got %q, want %q", sid, "original-sid")
	}
}

func TestResolveSessionID_ExpiredWindow_GeneratesNew(t *testing.T) {
	store := setupTestStore(t)

	session := models.NewSession("Old", "main", "proj", "old-sid")
	session.Summary = "old session"
	store.SaveSession(session)

	// Window of 0s should always expire
	sid, existing, err := store.ResolveSessionID("proj", 0)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if existing {
		t.Error("expected existing=false with 0s window")
	}
	if sid == "old-sid" {
		t.Error("should not reuse old-sid with expired window")
	}
}

func TestResolveSessionID_DifferentProject_Separate(t *testing.T) {
	store := setupTestStore(t)

	session := models.NewSession("Proj A", "main", "project-a", "sid-a")
	session.Summary = "project a"
	store.SaveSession(session)

	// Different project should get new ID
	sid, existing, err := store.ResolveSessionID("project-b", 4*time.Hour)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if existing {
		t.Error("expected existing=false for different project")
	}
	if sid == "sid-a" {
		t.Error("should not reuse sid from different project")
	}
}

func TestResolveSessionID_EmptyProject(t *testing.T) {
	store := setupTestStore(t)

	// Should not crash with empty project
	sid, _, err := store.ResolveSessionID("", 4*time.Hour)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if sid == "" {
		t.Error("expected non-empty ID even for empty project")
	}
}

func TestResolveSessionID_TimezoneCorrect(t *testing.T) {
	store := setupTestStore(t)

	// Create session — its "updated" is stored as local time with Z suffix
	session := models.NewSession("TZ Test", "main", "tz-proj", "tz-sid")
	session.Summary = "timezone test"
	store.SaveSession(session)

	// With 1-second window: should still find it (just created)
	sid, existing, err := store.ResolveSessionID("tz-proj", 1*time.Second)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !existing {
		t.Error("expected existing=true — session was just created, 1s window should suffice")
	}
	if sid != "tz-sid" {
		t.Errorf("got %q, want %q", sid, "tz-sid")
	}
}

func TestResolveSessionID_GeneratesUniqueIDs(t *testing.T) {
	store := setupTestStore(t)

	sid1, _, _ := store.ResolveSessionID("unique-test", 0)
	sid2, _, _ := store.ResolveSessionID("unique-test", 0)

	if sid1 == sid2 {
		t.Errorf("two generated IDs should differ: both %q", sid1)
	}
}

// ─── Reindex backfill ───

func TestReindex_PreservesNoteSessionID(t *testing.T) {
	store := setupTestStore(t)

	// Create note with session_id
	note := models.NewNote("cat", "Reindex Test", "content")
	note.Metadata["session_id"] = "reindex-ref"
	store.AddNote(note)

	// Verify it's in DB
	var before string
	store.db.QueryRow("SELECT session_id FROM entries WHERE id = ?", note.ID).Scan(&before)
	if before != "reindex-ref" {
		t.Fatalf("pre-reindex session_id = %q", before)
	}

	// Reindex (clears and rebuilds DB from files)
	count, err := store.Reindex()
	if err != nil {
		t.Fatalf("Reindex error: %v", err)
	}
	if count == 0 {
		t.Fatal("Reindex returned 0 entries")
	}

	// Verify session_id survived reindex
	var after string
	store.db.QueryRow("SELECT session_id FROM entries WHERE title = ?", "Reindex Test").Scan(&after)
	if after != "reindex-ref" {
		t.Errorf("post-reindex session_id = %q, want %q", after, "reindex-ref")
	}
}

func TestReindex_NoteWithoutSessionID_GetsEmptyString(t *testing.T) {
	store := setupTestStore(t)

	note := models.NewNote("cat", "No SID Reindex", "content")
	store.AddNote(note)

	store.Reindex()

	var sid string
	err := store.db.QueryRow("SELECT session_id FROM entries WHERE title = ?", "No SID Reindex").Scan(&sid)
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	if sid != "" {
		t.Errorf("session_id = %q, want empty", sid)
	}
}

// ─── Schema: session_id index ───

func TestSessionIDIndex_Exists(t *testing.T) {
	store := setupTestStore(t)

	var count int
	err := store.db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_entries_session_id'",
	).Scan(&count)
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	if count != 1 {
		t.Errorf("idx_entries_session_id: found %d, want 1", count)
	}
}

// ─── ValidateSessionMarkdown ───

func TestValidate_GoodSession_Passes(t *testing.T) {
	content := `## Summary
This session focused on implementing a comprehensive notification system for the platform backend
service. The work involved designing the database schema with proper indexes for fast recipient
lookups and unread count queries, building eight FastAPI endpoints for notification CRUD operations
and user preference management, creating WebSocket handlers for real-time push delivery to all
connected clients across multiple server processes, and integrating with the existing authentication
middleware for secure role-based access control. We chose Redis pub/sub for cross-process notification
fanout after evaluating three alternatives including database polling and dedicated message queues.
The complete notification system was deployed to the staging environment and verified with fifteen
integration tests covering creation, delivery, read receipts, and preference management workflows.

## What Happened
1. **Designed schema** — Created notifications table at ` + "`alembic/versions/20260307.py`" + `.
2. **Built API** — 8 FastAPI endpoints at ` + "`app/api/v1/notifications.py`" + `.
3. **Added WebSocket** — Real-time delivery at ` + "`app/ws/handler.py`" + `.

## Problems & Solutions
- **Problem**: WebSocket dropped after 60s
  **Solution**: Added 30s heartbeat matching Nginx timeout

## Learning Insights
- Match heartbeat interval to reverse proxy timeout

## Next Steps
- [ ] Add email channel
`
	result := ValidateSessionMarkdown(content)
	if !result.Valid {
		for _, c := range result.Checks {
			if !c.Passed {
				t.Errorf("check failed: %s — %s", c.Section, c.Message)
			}
		}
	}
}

func TestValidate_ShortSummary_Fails(t *testing.T) {
	content := `## Summary
Fixed a bug.

## What Happened
1. Found it.
2. Fixed it.
3. Deployed it.

## Learning Insights
- Testing is good

## Next Steps
- [ ] Monitor
`
	result := ValidateSessionMarkdown(content)
	if result.Valid {
		t.Error("expected validation to fail for short summary")
	}
	found := false
	for _, c := range result.Checks {
		if c.Section == "Summary" && !c.Passed {
			found = true
		}
	}
	if !found {
		t.Error("expected Summary check to fail")
	}
}

func TestValidate_TooFewPhases_Fails(t *testing.T) {
	content := `## Summary
` + longSummary() + `

## What Happened
1. **Did one thing** — Only one phase here.

## Learning Insights
- Something

## Next Steps
- [ ] Next
`
	result := ValidateSessionMarkdown(content)
	if result.Valid {
		t.Error("expected fail for < 3 phases")
	}
	for _, c := range result.Checks {
		if c.Section == "What Happened" && c.Passed {
			t.Error("What Happened should fail with only 1 phase")
		}
	}
}

func TestValidate_EmptySolution_Fails(t *testing.T) {
	content := `## Summary
` + longSummary() + `

## What Happened
1. **Phase one** — Did X.
2. **Phase two** — Did Y.
3. **Phase three** — Did Z.

## Problems & Solutions
- **Problem**: Something broke
  **Solution**: Fixed it properly
- **Problem**: Another thing broke
  **Solution**:

## Learning Insights
- Insight here

## Next Steps
- [ ] Do next
`
	result := ValidateSessionMarkdown(content)
	if result.Valid {
		t.Error("expected fail for empty solution")
	}
	for _, c := range result.Checks {
		if c.Section == "Problems & Solutions" && c.Passed {
			t.Error("Problems & Solutions should fail with empty solution")
		}
	}
}

func TestValidate_AllSolutionsFilled_Passes(t *testing.T) {
	content := `## Summary
` + longSummary() + `

## What Happened
1. **Phase one** — Did X.
2. **Phase two** — Did Y.
3. **Phase three** — Did Z.

## Problems & Solutions
- **Problem**: Bug A
  **Solution**: Fixed by doing X
- **Problem**: Bug B
  **Solution**: Fixed by doing Y

## Learning Insights
- Useful insight

## Next Steps
- [ ] Follow up
`
	result := ValidateSessionMarkdown(content)
	for _, c := range result.Checks {
		if c.Section == "Problems & Solutions" && !c.Passed {
			t.Errorf("Problems & Solutions should pass: %s", c.Message)
		}
	}
}

func TestValidate_FreeFormProblems_NotRejected(t *testing.T) {
	// Free-form format (no **Problem**:/**Solution**: template) should not be rejected
	content := `## Summary
` + longSummary() + `

## What Happened
1. **Phase one** — Did X.
2. **Phase two** — Did Y.
3. **Phase three** — Did Z.

## Problems & Solutions
- **cp -r trailing slash**: Created nested directory instead of copying contents. Fix: remove trailing slash.
- **Silent errors**: 2>/dev/null hid failures. Fix: remove suppression, add proper error handling.

## Learning Insights
- Always check cp behavior with trailing slashes

## Next Steps
- [ ] Audit other scripts for silent error suppression
`
	result := ValidateSessionMarkdown(content)
	for _, c := range result.Checks {
		if c.Section == "Problems & Solutions" && !c.Passed {
			t.Errorf("free-form problems should pass: %s", c.Message)
		}
	}
}

func TestValidate_MissingSections_Fails(t *testing.T) {
	content := `## Summary
` + longSummary() + `

## What Happened
1. **Phase one** — X.
2. **Phase two** — Y.
3. **Phase three** — Z.
`
	result := ValidateSessionMarkdown(content)
	if result.Valid {
		t.Error("expected fail for missing Learning Insights and Next Steps")
	}
	missingCount := 0
	for _, c := range result.Checks {
		if !c.Passed && (c.Section == "Learning Insights" || c.Section == "Next Steps") {
			missingCount++
		}
	}
	if missingCount != 2 {
		t.Errorf("expected 2 missing sections, got %d", missingCount)
	}
}

func TestValidate_NoProblemsSection_OK(t *testing.T) {
	// No Problems & Solutions section is fine (no problems encountered)
	content := `## Summary
` + longSummary() + `

## What Happened
1. **Phase one** — X.
2. **Phase two** — Y.
3. **Phase three** — Z.

## Learning Insights
- Good insight

## Next Steps
- [ ] Do next
`
	result := ValidateSessionMarkdown(content)
	for _, c := range result.Checks {
		if c.Section == "Problems & Solutions" && !c.Passed {
			t.Errorf("missing P&S section should be OK: %s", c.Message)
		}
	}
}

func TestValidate_SectionAliases(t *testing.T) {
	// "Insights" should match "Learning Insights"
	content := `## Summary
` + longSummary() + `

## What Happened
1. **A** — X.
2. **B** — Y.
3. **C** — Z.

## Insights
- Good insight

## Next Steps
- [ ] Do next
`
	result := ValidateSessionMarkdown(content)
	for _, c := range result.Checks {
		if c.Section == "Learning Insights" && !c.Passed {
			t.Errorf("'Insights' alias should match 'Learning Insights': %s", c.Message)
		}
	}
}

// longSummary returns a >100 word summary for tests that don't focus on summary length
func longSummary() string {
	return `This session was a comprehensive effort to implement and test the full notification system
for the VisPie platform backend service. The work involved designing the database schema with
proper indexes for fast recipient lookups, building eight FastAPI endpoints for notification
CRUD operations and preference management, creating WebSocket handlers for real-time push
delivery to connected clients, and integrating everything with the existing authentication
middleware for secure access control. We chose Redis pub/sub for cross-process notification
fanout after evaluating three alternatives. The complete implementation was deployed to the
staging environment and verified end-to-end with integration tests covering all major workflows.`
}

// Ensure sql import is used
var _ = sql.ErrNoRows
