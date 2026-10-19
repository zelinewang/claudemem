package storage

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/models"
)

// ========== What Changed: no item is dropped ==========
//
// "## What Changed" items are parsed into path + description when they use the template form
// ("- `path` — description"). Every other item, in any of the shapes real reports use, is kept as a
// change without a path, so the report's text always reaches the stored session.

const whatChangedBody = "## Summary\nsummary text\n\n## What Changed\n" +
	"- `pkg/a.go` — ORACLE-C1 canonical item\n" +
	"- `pkg/b.go`: ORACLE-C2 backticked path with a colon\n" +
	"- pkg/c.go: ORACLE-C3 bare path with a colon\n" +
	"- ORACLE-C4 free text item without a path\n" +
	"- `pkg/d.go` (new) ORACLE-C5 backticked path then other text\n" +
	"- `pkg/e.go`\n" +
	"  ORACLE-C6 continuation line of the item above\n" +
	"ORACLE-C7 paragraph line under What Changed\n" +
	"* `pkg/f.go` — ORACLE-C8 star bullet\n" +
	"1. `pkg/g.go` — ORACLE-C9 numbered item\n" +
	"\n## Next Steps\n- [ ] next\n"

func TestParseSessionBody_WhatChangedKeepsEveryItem(t *testing.T) {
	session := models.NewSession("t", "b", "p", "s")
	ParseSessionBody(session, whatChangedBody)

	want := []models.FileChange{
		{Path: "pkg/a.go", Description: "ORACLE-C1 canonical item"},
		{Path: "pkg/b.go", Description: "ORACLE-C2 backticked path with a colon"},
		{Path: "", Description: "pkg/c.go: ORACLE-C3 bare path with a colon"},
		{Path: "", Description: "ORACLE-C4 free text item without a path"},
		{Path: "", Description: "`pkg/d.go` (new) ORACLE-C5 backticked path then other text"},
		{Path: "", Description: "`pkg/e.go` ORACLE-C6 continuation line of the item above"},
		{Path: "", Description: "ORACLE-C7 paragraph line under What Changed"},
		{Path: "pkg/f.go", Description: "ORACLE-C8 star bullet"},
		{Path: "pkg/g.go", Description: "ORACLE-C9 numbered item"},
	}
	if !reflect.DeepEqual(session.Changes, want) {
		t.Fatalf("Changes =\n%#v\nwant\n%#v", session.Changes, want)
	}
}

// An em dash inside the description must not move the split: the backticked path is read first.
func TestParseChangeItems_EmDashInsideTheDescription(t *testing.T) {
	got := ParseChangeItems("- `cmd/root.go`: added the --store flag — defaults to the home store\n" +
		"- `pkg/x.go` — first part — second part\n" +
		"- `a.go` and `b.go` — both changed\n" +
		"- pkg/c.go — bare path in the template form\n" +
		"- see `pkg/d.go` — text before the dash is not a path\n" +
		"- `pkg/e.go — the closing backtick is missing\n")
	want := []models.FileChange{
		{Path: "cmd/root.go", Description: "added the --store flag — defaults to the home store"},
		{Path: "pkg/x.go", Description: "first part — second part"},
		{Path: "", Description: "`a.go` and `b.go` — both changed"},
		{Path: "pkg/c.go", Description: "bare path in the template form"},
		{Path: "", Description: "see `pkg/d.go` — text before the dash is not a path"},
		{Path: "pkg/e.go", Description: "the closing backtick is missing"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseChangeItems =\n%#v\nwant\n%#v", got, want)
	}
	for _, c := range got {
		if strings.Contains(c.Path, "`") {
			t.Errorf("a backtick leaked into the path %q", c.Path)
		}
		if again := ParseChangeItems("- " + formatChangeItem(c)); len(again) != 1 || again[0] != c {
			t.Errorf("round trip of %#v gave %#v", c, again)
		}
	}
}

// Text without a path reads back as the same text: a " — " in it does not turn its first words into
// a path on the next read.
func TestFormatChangeItem_TextWithoutAPathReadsBackTheSame(t *testing.T) {
	for _, c := range []models.FileChange{
		{Description: "renamed the flag — the old name still works"},
		{Description: "see `a.go` — fixed"},
		{Description: "`a.go` and `b.go` — both changed"},
		{Description: "plain text"},
	} {
		written := formatChangeItem(c)
		if again := ParseChangeItems("- " + written); len(again) != 1 || again[0] != c {
			t.Errorf("%#v was written as %q and read back as %#v", c, written, again)
		}
	}
}

// A change with neither a path nor a description is not written, so it cannot come back as "-".
func TestFormatSessionMarkdown_SkipsAnEmptyChange(t *testing.T) {
	session := models.NewSession("Empty change", "b", "p", "s")
	session.Summary = "summary"
	session.Changes = []models.FileChange{{}, {Path: "pkg/a.go", Description: "kept"}}

	parsed, err := ParseSessionMarkdown([]byte(FormatSessionMarkdown(session)))
	if err != nil {
		t.Fatalf("ParseSessionMarkdown failed: %v", err)
	}
	want := []models.FileChange{{Path: "pkg/a.go", Description: "kept"}}
	if !reflect.DeepEqual(parsed.Changes, want) {
		t.Fatalf("Changes = %#v, want %#v", parsed.Changes, want)
	}
}

func TestFormatSessionMarkdown_ChangesRoundTrip(t *testing.T) {
	session := models.NewSession("Round trip", "b", "p", "s")
	session.Summary = "summary"
	session.Changes = []models.FileChange{
		{Path: "pkg/a.go", Description: "canonical"},
		{Path: "", Description: "free text kept without a path"},
		{Path: "", Description: "`pkg/d.go` (new) text after a backticked path"},
		{Path: "pkg/only-path.go", Description: ""},
	}

	stored := FormatSessionMarkdown(session)
	if strings.Contains(stored, "``") {
		t.Errorf("an empty path rendered as an empty code span:\n%s", stored)
	}
	parsed, err := ParseSessionMarkdown([]byte(stored))
	if err != nil {
		t.Fatalf("ParseSessionMarkdown failed: %v", err)
	}
	if !reflect.DeepEqual(parsed.Changes, session.Changes) {
		t.Fatalf("round trip changed the items:\ngot  %#v\nwant %#v\nfile:\n%s", parsed.Changes, session.Changes, stored)
	}
}

func TestMergeFileChanges_KeepsEveryItem(t *testing.T) {
	existing := []models.FileChange{
		{Path: "pkg/a.go", Description: "added X"},
		{Path: "pkg/a.go", Description: "fixed Y"}, // the same path twice in one report
		{Path: "", Description: "free item one"},
		{Path: "", Description: "free item two"},
	}
	added := []models.FileChange{
		{Path: "", Description: "free item one"}, // repeated by a second wrap-up
		{Path: "", Description: "free item three"},
		{Path: "pkg/a.go", Description: "also Z"},
	}

	got := mergeFileChanges(existing, added)
	want := []models.FileChange{
		{Path: "pkg/a.go", Description: "added X; also Z"},
		{Path: "pkg/a.go", Description: "fixed Y"},
		{Path: "", Description: "free item one"},
		{Path: "", Description: "free item two"},
		{Path: "", Description: "free item three"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeFileChanges =\n%#v\nwant\n%#v", got, want)
	}
}

func TestSaveSession_MergeKeepsItemsWithoutAPath(t *testing.T) {
	store := setupTestStore(t)

	first := models.NewSession("Merge keeps changes", "main", "proj", "sess-changes")
	first.Summary = "first wrap-up"
	ParseSessionBody(first, whatChangedBody)
	if _, err := store.SaveSession(first); err != nil {
		t.Fatalf("first SaveSession failed: %v", err)
	}

	second := models.NewSession("Merge keeps changes", "main", "proj", "sess-changes")
	second.Summary = "second wrap-up"
	second.Changes = []models.FileChange{{Path: "", Description: "ORACLE-C10 added by the second wrap-up"}}
	result, err := store.SaveSession(second)
	if err != nil {
		t.Fatalf("second SaveSession failed: %v", err)
	}
	if result.Action != "updated" {
		t.Fatalf("second save: Action = %q, want 'updated'", result.Action)
	}

	merged, err := store.GetSession(result.SessionID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	text := ""
	for _, c := range merged.Changes {
		text += c.Path + " " + c.Description + "\n"
	}
	for i := 1; i <= 10; i++ {
		marker := fmt.Sprintf("ORACLE-C%d ", i) // the trailing space keeps C1 from matching C10
		if !strings.Contains(text, marker) {
			t.Errorf("merged session lost %q; changes:\n%s", strings.TrimSpace(marker), text)
		}
	}
}
