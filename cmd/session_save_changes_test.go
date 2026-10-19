package cmd

import (
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/storage"
)

// ─── session save keeps every list item (CLI level) ─────────────────────────
//
// The report is piped on stdin as /wrapup does. Items of "## What Changed" outside the
// "- `path` — description" form, and continuation lines of list items, used to be dropped by the
// command's own copy of the section parser; every marker must now reach the stored file.

const listItemsReportFixture = "## Summary\n\n" +
	"This report exercises the list sections of a wrap-up with the item shapes real reports use, so\n" +
	"the save pipeline can be checked end to end. Every marker prefixed ORACLE- must survive storage.\n\n" +
	"## Key Decisions\n\n" +
	"- **ORACLE-D1 decision**: chosen because of the first reason,\n" +
	"  and ORACLE-D1-CONT on a continuation line\n\n" +
	"## What Changed\n\n" +
	"- `pkg/a.go` — ORACLE-C1 canonical item\n" +
	"- `pkg/b.go`: ORACLE-C2 backticked path with a colon\n" +
	"- pkg/c.go: ORACLE-C3 bare path with a colon\n" +
	"- ORACLE-C4 free text item without a path\n" +
	"- `pkg/d.go` (new) ORACLE-C5 backticked path then other text\n" +
	"ORACLE-C7 paragraph line under What Changed\n\n" +
	"## Learning Insights\n\n" +
	"- ORACLE-I1 insight first line\n" +
	"  ORACLE-I1-CONT insight continuation\n\n" +
	"## Next Steps\n\n" +
	"- [ ] ORACLE-N1 next step\n"

var listItemMarkers = []string{
	"ORACLE-D1 ", "ORACLE-D1-CONT", "ORACLE-C1 ", "ORACLE-C2 ", "ORACLE-C3 ", "ORACLE-C4 ", "ORACLE-C5 ",
	"ORACLE-C7 ", "ORACLE-I1 ", "ORACLE-I1-CONT", "ORACLE-N1 ",
}

func TestSessionSave_StdinKeepsEveryListItem(t *testing.T) {
	storePath := setupSessionSaveTest(t, listItemsReportFixture)

	if err := runSessionSave(sessionSaveCmd, nil); err != nil {
		t.Fatalf("runSessionSave() failed: %v", err)
	}

	stored := readOnlyStoredSession(t, storePath)
	for _, m := range listItemMarkers {
		if !strings.Contains(stored, m) {
			t.Errorf("stored session lost marker %q\n%s", strings.TrimSpace(m), stored)
		}
	}
	if !strings.Contains(stored, "- `pkg/b.go` — ORACLE-C2") {
		t.Errorf("a backticked path with a colon was not stored in the template form:\n%s", stored)
	}
}

func TestSessionSave_ChangesFlagKeepsAnItemWithoutAColon(t *testing.T) {
	storePath := setupSessionSaveTest(t, "")
	sessionSummary = "structured flags"
	sessionChanges = []string{"pkg/a.go:ORACLE-F1 flag item with a colon", "ORACLE-F2 flag item without a colon"}

	if err := runSessionSave(sessionSaveCmd, nil); err != nil {
		t.Fatalf("runSessionSave() failed: %v", err)
	}

	stored := readOnlyStoredSession(t, storePath)
	for _, m := range []string{"ORACLE-F1", "ORACLE-F2"} {
		if !strings.Contains(stored, m) {
			t.Errorf("stored session lost --changes item %q\n%s", m, stored)
		}
	}
}

// session get prints an item without a path, or without a description, without an empty half.
func TestSessionGet_TextShowsItemsWithoutAPathOrDescription(t *testing.T) {
	storePath := setupSessionSaveTest(t, "## Summary\n\nA session whose What Changed has items without a path or a description.\n\n"+
		"## What Changed\n\n"+
		"- `pkg/a.go` — ORACLE-G1 canonical item\n"+
		"- ORACLE-G2 free text item without a path\n"+
		"- `pkg/only-path.go`\n")
	if err := runSessionSave(sessionSaveCmd, nil); err != nil {
		t.Fatalf("runSessionSave() failed: %v", err)
	}
	stored, err := storage.ParseSessionMarkdown([]byte(readOnlyStoredSession(t, storePath)))
	if err != nil {
		t.Fatalf("ParseSessionMarkdown() failed: %v", err)
	}

	origFormat := outputFormat
	outputFormat = "text"
	t.Cleanup(func() { outputFormat = origFormat })
	out := captureStdout(t, func() {
		if err := runSessionGet(sessionGetCmd, []string{stored.ID}); err != nil {
			t.Errorf("runSessionGet() failed: %v", err)
		}
	})
	for _, line := range []string{"- pkg/a.go — ORACLE-G1 canonical item", "- ORACLE-G2 free text item without a path", "- pkg/only-path.go"} {
		if !strings.Contains(out, line+"\n") {
			t.Errorf("session get does not print the line %q:\n%s", line, out)
		}
	}
	if strings.Contains(out, "-  — ") || strings.Contains(out, " — \n") {
		t.Errorf("session get printed an empty path or description:\n%s", out)
	}
}
