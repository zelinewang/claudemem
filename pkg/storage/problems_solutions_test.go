package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
)

// ─── Problems & Solutions round-trip integrity ───────────────────────────────
//
// These tests pin the wrapup canonical two-line format and its variants:
//
//	- **Problem**: <problem body, possibly multi-line>
//	  **Solution**: <solution body, possibly multi-line>
//
// Historical defect family: solutions were silently dropped (empty
// "**Solution**:" lines stored) and/or fused into the problem text. Every
// test below fails on the unfixed parser.

// problemsFixtureFrontmatter wraps a body in minimal valid session frontmatter.
func problemsFixtureFrontmatter(body string) string {
	return "---\n" +
		"id: 11111111-2222-3333-4444-555555555555\n" +
		"type: session\n" +
		"title: round-trip probe\n" +
		"date: \"2026-07-20\"\n" +
		"branch: main\n" +
		"project: proj\n" +
		"session_id: probe-session\n" +
		"tags: []\n" +
		"created: \"2026-07-20T00:00:00Z\"\n" +
		"---\n\n" + body
}

// canonicalProblemsBody mirrors the /wrapup canonical emission: four pairs
// covering single-line, multi-line problem, multi-line solution, and the
// bold-colon variant.
const canonicalProblemsBody = `## Summary
probe summary

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
`

// wantCanonicalProblems is the exact structured form the fixture must parse to.
// Multi-line bodies are preserved with "\n" joins; bold-colon markers are
// stripped clean (no markup residue).
var wantCanonicalProblems = []models.ProblemSolution{
	{Problem: "ORACLE-P1 single-line problem body", Solution: "ORACLE-S1 single-line solution body"},
	{
		Problem:  "ORACLE-P2 multi-line problem body first physical line\nwhich continues on a second physical line carrying marker ORACLE-P2-CONT",
		Solution: "ORACLE-S2 solution following a multi-line problem",
	},
	{
		Problem:  "ORACLE-P3 problem preceding a multi-line solution",
		Solution: "ORACLE-S3 first solution line\nwhich continues with marker ORACLE-S3-CONT on its own physical line",
	},
	{Problem: "ORACLE-P4 bold-colon variant problem body", Solution: "ORACLE-S4 bold-colon variant solution body"},
}

func assertProblemsEqual(t *testing.T, got, want []models.ProblemSolution) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(Problems) = %d, want %d\ngot:  %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Problem != want[i].Problem {
			t.Errorf("Problems[%d].Problem = %q, want %q", i, got[i].Problem, want[i].Problem)
		}
		if got[i].Solution != want[i].Solution {
			t.Errorf("Problems[%d].Solution = %q, want %q", i, got[i].Solution, want[i].Solution)
		}
	}
}

// TestParseSessionMarkdown_ProblemsCanonicalRoundTrip parses stored-format
// markdown with all four pair shapes and requires exact bodies.
func TestParseSessionMarkdown_ProblemsCanonicalRoundTrip(t *testing.T) {
	session, err := ParseSessionMarkdown([]byte(problemsFixtureFrontmatter(canonicalProblemsBody)))
	if err != nil {
		t.Fatalf("ParseSessionMarkdown() failed: %v", err)
	}
	assertProblemsEqual(t, session.Problems, wantCanonicalProblems)
}

// TestParseSessionMarkdown_BoldColonNoResidue pins that the bold-colon variant
// (**Problem:** / **Solution:**) parses into CLEAN fields — no markup residue
// may remain in the stored bodies.
func TestParseSessionMarkdown_BoldColonNoResidue(t *testing.T) {
	session, err := ParseSessionMarkdown([]byte(problemsFixtureFrontmatter(canonicalProblemsBody)))
	if err != nil {
		t.Fatalf("ParseSessionMarkdown() failed: %v", err)
	}
	for i, ps := range session.Problems {
		if strings.Contains(ps.Problem, "**") || strings.Contains(ps.Solution, "**") {
			t.Errorf("Problems[%d] contains markup residue: %#v", i, ps)
		}
		if strings.Contains(ps.Problem, "Solution:") {
			t.Errorf("Problems[%d].Problem contains fused solution text: %q", i, ps.Problem)
		}
	}
}

// TestSessionProblemsFormatParseRoundTrip is the round-trip law at the storage
// site: for any Session s, Parse(Format(s)) preserves every Problem and
// Solution body exactly.
func TestSessionProblemsFormatParseRoundTrip(t *testing.T) {
	session := models.NewSession("rt", "main", "proj", "rt-session")
	session.Summary = "round-trip law probe"
	session.Problems = []models.ProblemSolution{
		{Problem: "single-line problem", Solution: "single-line solution"},
		{Problem: "multi-line problem first\nsecond physical line", Solution: "multi-line solution first\nsecond physical line"},
		{Problem: "problem with no solution yet", Solution: ""},
	}

	formatted := FormatSessionMarkdown(session)
	parsed, err := ParseSessionMarkdown([]byte(formatted))
	if err != nil {
		t.Fatalf("ParseSessionMarkdown(Format(s)) failed: %v", err)
	}
	assertProblemsEqual(t, parsed.Problems, session.Problems)
}

// TestSaveSession_MergeResavePreservesProblems exercises the dedup/merge path
// in-process: two saves under one session-id must yield a stored file whose
// problems still parse exactly — no empty solutions, no fusion, no
// duplication.
func TestSaveSession_MergeResavePreservesProblems(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore() failed: %v", err)
	}
	defer store.Close()

	build := func(title string) *models.Session {
		s := models.NewSession(title, "moa-test", "moa-oracle", "moa-oracle-1")
		s.Summary = "merge probe"
		s.Problems = append([]models.ProblemSolution(nil), wantCanonicalProblems...)
		return s
	}

	if _, err := store.SaveSession(build("oracle fresh")); err != nil {
		t.Fatalf("SaveSession() fresh failed: %v", err)
	}
	res, err := store.SaveSession(build("oracle merged"))
	if err != nil {
		t.Fatalf("SaveSession() merge failed: %v", err)
	}
	if res.Action != "updated" {
		t.Errorf("second save Action = %q, want %q", res.Action, "updated")
	}

	// Locate the single stored session file and re-parse it.
	matches, err := filepath.Glob(filepath.Join(store.baseDir, "sessions", "*.md"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected exactly 1 stored session file, got %v (err=%v)", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile() failed: %v", err)
	}

	// Every marker must survive storage verbatim.
	for _, m := range []string{"ORACLE-S1", "ORACLE-S2", "ORACLE-S3", "ORACLE-S3-CONT", "ORACLE-S4", "ORACLE-P2-CONT"} {
		if !strings.Contains(string(data), m) {
			t.Errorf("stored file lost marker %q\n--- stored ---\n%s", m, data)
		}
	}
	// No empty **Solution**: lines may be stored.
	if strings.Contains(string(data), "**Solution**: \n") || strings.HasSuffix(strings.TrimRight(string(data), "\n"), "**Solution**:") {
		t.Errorf("stored file contains an empty **Solution**: line\n--- stored ---\n%s", data)
	}

	reparsed, err := ParseSessionMarkdown(data)
	if err != nil {
		t.Fatalf("re-parse of stored file failed: %v", err)
	}
	assertProblemsEqual(t, reparsed.Problems, wantCanonicalProblems)
}

// TestParseSessionMarkdown_CorruptedHistoricalLoads is the R4 backward-compat
// guard: files already corrupted by the historical defect (empty solution
// lines, solution text fused into problem lines) must still load without
// error, retaining whatever text they hold.
func TestParseSessionMarkdown_CorruptedHistoricalLoads(t *testing.T) {
	corrupted := `## Problems & Solutions
- **Problem**: real problem text, solution destroyed at save time
  **Solution**:
- **Problem**: older fused entry **Solution**: solution text glued into the problem line
  **Solution**:
`
	session, err := ParseSessionMarkdown([]byte(problemsFixtureFrontmatter(corrupted)))
	if err != nil {
		t.Fatalf("corrupted historical file must still load: %v", err)
	}
	if len(session.Problems) != 2 {
		t.Fatalf("len(Problems) = %d, want 2: %#v", len(session.Problems), session.Problems)
	}
	if !strings.Contains(session.Problems[0].Problem, "real problem text") {
		t.Errorf("problem text lost: %q", session.Problems[0].Problem)
	}
	// The fused historical entry keeps all its text (nothing further destroyed).
	if !strings.Contains(session.Problems[1].Problem, "older fused entry") ||
		!strings.Contains(session.Problems[1].Problem, "solution text glued into the problem line") {
		t.Errorf("fused historical text not preserved: %q", session.Problems[1].Problem)
	}
}
