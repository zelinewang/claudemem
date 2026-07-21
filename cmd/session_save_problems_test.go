package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/storage"
)

// ─── session save Problems & Solutions integrity (CLI level) ────────────────
//
// These tests drive runSessionSave exactly as /wrapup does — full markdown
// report piped on stdin — and assert on the stored session file. They pin the
// cmd-side parser site (the second of the two duplicated parsers).

const wrapupReportFixture = `## Summary

This report exercises the canonical wrapup two-line problem/solution format
across single-line, multi-line, and bold-colon-variant pairs so that the save
pipeline can be verified end to end. Every marker string prefixed ORACLE- must
survive storage verbatim, including through a merge re-save under the same
session id. This paragraph pads the summary to a realistic length.

## Problems & Solutions

- **Problem**: ORACLE-P1 single-line problem body
  **Solution**: ORACLE-S1 single-line solution body
- **Problem**: ORACLE-P2 multi-line problem body first physical line
  which continues on a second physical line carrying marker ORACLE-P2-CONT
  **Solution**: ORACLE-S2 solution following a multi-line problem
- **Problem**: ORACLE-P3 problem preceding a multi-line solution
  **Solution**: ORACLE-S3 first solution line
  which continues with marker ORACLE-S3-CONT on its own physical line
- **Problem:** ORACLE-P4 bold-colon variant problem body
  **Solution:** ORACLE-S4 bold-colon variant solution body

## Learning Insights

- ORACLE-I1 insight line retained verbatim through storage

## Next Steps

- [ ] ORACLE-N1 next step retained verbatim through storage
`

// setupSessionSaveTest redirects the store into a temp dir, pipes stdin, sets
// the save flags, and restores everything on cleanup.
func setupSessionSaveTest(t *testing.T, stdinContent string) (storePath string) {
	t.Helper()

	storePath = t.TempDir()

	origStoreDir := storeDir
	origStdin := os.Stdin
	origTitle, origBranch, origProject, origSessionID := sessionTitle, sessionBranch, sessionProject, sessionSessionID
	origTags, origContent, origSummary, origWhatHappened := sessionTags, sessionContent, sessionSummary, sessionWhatHappened
	origDecisions, origChanges, origProblems := sessionDecisions, sessionChanges, sessionProblems
	origInsights, origQuestions, origNextSteps := sessionInsights, sessionQuestions, sessionNextSteps
	origRelatedNotes := sessionRelatedNotes

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() failed: %v", err)
	}
	if _, err := w.WriteString(stdinContent); err != nil {
		t.Fatalf("pipe write failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("pipe close failed: %v", err)
	}

	storeDir = storePath
	os.Stdin = r
	sessionTitle = "oracle"
	sessionBranch = "moa-test"
	sessionProject = "moa-oracle"
	sessionSessionID = "moa-oracle-1"
	sessionTags = "moa"
	sessionContent = ""
	sessionSummary = ""
	sessionWhatHappened = ""
	sessionDecisions = nil
	sessionChanges = nil
	sessionProblems = nil
	sessionInsights = nil
	sessionQuestions = nil
	sessionNextSteps = nil
	sessionRelatedNotes = nil

	t.Cleanup(func() {
		storeDir = origStoreDir
		os.Stdin = origStdin
		r.Close()
		sessionTitle, sessionBranch, sessionProject, sessionSessionID = origTitle, origBranch, origProject, origSessionID
		sessionTags, sessionContent, sessionSummary, sessionWhatHappened = origTags, origContent, origSummary, origWhatHappened
		sessionDecisions, sessionChanges, sessionProblems = origDecisions, origChanges, origProblems
		sessionInsights, sessionQuestions, sessionNextSteps = origInsights, origQuestions, origNextSteps
		sessionRelatedNotes = origRelatedNotes
	})
	return storePath
}

// readOnlyStoredSession returns the single stored session markdown file.
func readOnlyStoredSession(t *testing.T, storePath string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(storePath, "sessions", "*.md"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected exactly 1 stored session file, got %v (err=%v)", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile() failed: %v", err)
	}
	return string(data)
}

var oracleMarkers = []string{
	"ORACLE-S1", "ORACLE-S2", "ORACLE-S3", "ORACLE-S3-CONT",
	"ORACLE-S4", "ORACLE-P2-CONT", "ORACLE-I1", "ORACLE-N1",
}

func assertStoredMarkers(t *testing.T, stored string) {
	t.Helper()
	for _, m := range oracleMarkers {
		if !strings.Contains(stored, m) {
			t.Errorf("stored session lost marker %q", m)
		}
	}
	// No empty **Solution**: lines.
	for _, line := range strings.Split(stored, "\n") {
		if strings.TrimSpace(line) == "**Solution**:" {
			t.Errorf("stored session contains an empty **Solution**: line")
		}
	}
	// No Problem line may carry fused Solution text.
	for _, line := range strings.Split(stored, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- **Problem**") && strings.Contains(trimmed, "ORACLE-S") {
			t.Errorf("solution text fused into problem line: %q", trimmed)
		}
	}
}

// TestSessionSave_StdinCanonicalProblemsSurvive: a fresh save of a canonical
// wrapup report must persist every problem and solution body.
func TestSessionSave_StdinCanonicalProblemsSurvive(t *testing.T) {
	storePath := setupSessionSaveTest(t, wrapupReportFixture)

	if err := runSessionSave(sessionSaveCmd, nil); err != nil {
		t.Fatalf("runSessionSave() failed: %v", err)
	}

	stored := readOnlyStoredSession(t, storePath)
	assertStoredMarkers(t, stored)
}

// TestSessionSave_MergeResaveIdempotent: re-saving the same report under the
// same session-id must not corrupt, fuse, or duplicate problem entries.
func TestSessionSave_MergeResaveIdempotent(t *testing.T) {
	storePath := setupSessionSaveTest(t, wrapupReportFixture)

	if err := runSessionSave(sessionSaveCmd, nil); err != nil {
		t.Fatalf("first runSessionSave() failed: %v", err)
	}

	// Second save: stdin is consumed, so re-pipe the same content.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() failed: %v", err)
	}
	if _, err := w.WriteString(wrapupReportFixture); err != nil {
		t.Fatalf("pipe write failed: %v", err)
	}
	w.Close()
	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin; r.Close() }()

	if err := runSessionSave(sessionSaveCmd, nil); err != nil {
		t.Fatalf("second runSessionSave() failed: %v", err)
	}

	stored := readOnlyStoredSession(t, storePath)
	assertStoredMarkers(t, stored)

	// The stored file must parse back to exactly the four clean pairs.
	session, err := storage.ParseSessionMarkdown([]byte(stored))
	if err != nil {
		t.Fatalf("stored file does not re-parse: %v", err)
	}
	if len(session.Problems) != 4 {
		t.Fatalf("len(Problems) = %d, want 4 (no duplication/fusion): %#v", len(session.Problems), session.Problems)
	}
	for i, ps := range session.Problems {
		if strings.Contains(ps.Problem, "**") || strings.Contains(ps.Solution, "**") {
			t.Errorf("Problems[%d] contains markup residue: %#v", i, ps)
		}
	}
}
