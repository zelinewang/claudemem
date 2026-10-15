package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/zelinewang/claudemem/pkg/storage"
)

// ─── CLI tests for tag filters, tag edits and tag apply ────────────────────────
//
// These run the real command tree in-process, as a filtering wrapper runs the binary. runCLI
// always passes --store: the flag's default is the live store.

func resetFlags(c *cobra.Command) {
	reset := func(f *pflag.Flag) {
		if slice, ok := f.Value.(pflag.SliceValue); ok {
			slice.Replace(nil)
		} else {
			f.Value.Set(f.DefValue)
		}
		f.Changed = false
	}
	c.Flags().VisitAll(reset)
	c.PersistentFlags().VisitAll(reset)
	for _, sub := range c.Commands() {
		resetFlags(sub)
	}
}

// runCLI runs `claudemem --store <storePath> args...` and returns what it printed on stdout.
// Cobra keeps flag values between runs, so every flag is reset to its default first.
func runCLI(t *testing.T, storePath string, args ...string) (string, error) {
	t.Helper()
	if storePath == "" || !strings.HasPrefix(storePath, os.TempDir()) {
		t.Fatalf("runCLI needs a temporary store, got %q", storePath)
	}
	resetFlags(rootCmd)
	var cobraOut bytes.Buffer
	rootCmd.SetOut(&cobraOut)
	rootCmd.SetErr(&cobraOut)
	rootCmd.SetArgs(append([]string{"--store", storePath}, args...))
	var err error
	out := captureStdout(t, func() { err = rootCmd.Execute() })
	rootCmd.SetArgs(nil)
	rootCmd.SetOut(nil)
	rootCmd.SetErr(nil)
	resetFlags(rootCmd)
	return out, err
}

func mustRunCLI(t *testing.T, storePath string, args ...string) string {
	t.Helper()
	out, err := runCLI(t, storePath, args...)
	if err != nil {
		t.Fatalf("claudemem %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func addNoteCLI(t *testing.T, storePath, category, title, content, tags string) string {
	t.Helper()
	out := mustRunCLI(t, storePath, "note", "add", category, "--title", title, "--content", content, "--tags", tags, "--format", "json")
	var res storage.AddNoteResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Action != "created" {
		t.Fatalf("note add %q: %v (%s)", title, err, out)
	}
	return res.NoteID
}

func saveSessionCLI(t *testing.T, storePath, title, sessionID, summary, tags string) string {
	t.Helper()
	out := mustRunCLI(t, storePath, "session", "save", "--title", title, "--branch", "main", "--project", "proj",
		"--session-id", sessionID, "--summary", summary, "--tags", tags, "--format", "json")
	var res storage.SaveSessionResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Action != "created" {
		t.Fatalf("session save %q: %v (%s)", title, err, out)
	}
	return res.SessionID
}

// a store with one shared and one personal note, and one shared and one personal session
type labelledStore struct {
	path                           string
	sharedNote, personalNote       string
	sharedSession, personalSession string
}

func newLabelledStore(t *testing.T) labelledStore {
	t.Helper()
	s := labelledStore{path: t.TempDir()}
	s.sharedNote = addNoteCLI(t, s.path, "ops", "Deploy runbook", "labelkw how to deploy", "vis-shared,ops")
	s.personalNote = addNoteCLI(t, s.path, "life", "Private holiday", "labelkw beach in May", "vis-personal")
	s.sharedSession = saveSessionCLI(t, s.path, "Release review", "sid-shared", "labelkw shipped the release", "vis-shared")
	s.personalSession = saveSessionCLI(t, s.path, "Secret diary", "sid-personal", "labelkw private thoughts", "vis-personal")
	return s
}

func TestCLI_ContextInjectTag(t *testing.T) {
	s := newLabelledStore(t)

	out := mustRunCLI(t, s.path, "context", "inject", "--tag", "vis-shared", "--format", "json")
	var payload contextPayload
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("parse inject JSON: %v\n%s", err, out)
	}
	if len(payload.RecentNotes) != 1 || payload.RecentNotes[0].ID != s.sharedNote {
		t.Errorf("recent notes = %+v, want only the shared note", payload.RecentNotes)
	}
	if len(payload.RecentSessions) != 1 || payload.RecentSessions[0].ID != s.sharedSession {
		t.Errorf("recent sessions = %+v, want only the shared session", payload.RecentSessions)
	}
	if payload.Stats.TotalNotes != 1 || payload.Stats.TotalSessions != 1 {
		t.Errorf("stats = %+v, want the tagged counts 1 and 1", payload.Stats)
	}
	if strings.Contains(out, "Private") || strings.Contains(out, "Secret") {
		t.Errorf("filtered inject leaks a personal title:\n%s", out)
	}

	text := mustRunCLI(t, s.path, "context", "inject", "--tag", "vis-shared")
	if !strings.HasPrefix(text, "claudemem context (1 notes, 1 sessions tagged vis-shared)") {
		t.Errorf("text header:\n%s", text)
	}
	if strings.Contains(text, "Private") || strings.Contains(text, "Secret") {
		t.Errorf("filtered inject text leaks a personal title:\n%s", text)
	}

	// Without the flag nothing changes: everything, the whole store's counts, no "tags" key
	out = mustRunCLI(t, s.path, "context", "inject", "--format", "json")
	var all map[string]json.RawMessage
	json.Unmarshal([]byte(out), &all)
	if _, ok := all["tags"]; ok {
		t.Errorf("unfiltered inject JSON gained a tags key:\n%s", out)
	}
	payload = contextPayload{}
	json.Unmarshal([]byte(out), &payload)
	if len(payload.RecentNotes) != 2 || len(payload.RecentSessions) != 2 || payload.Stats.TotalNotes != 2 || payload.Stats.TotalSessions != 2 {
		t.Errorf("unfiltered inject = %+v", payload)
	}
	if text := mustRunCLI(t, s.path, "context", "inject"); !strings.HasPrefix(text, "claudemem context (2 notes, 2 sessions)\n") {
		t.Errorf("unfiltered text header changed:\n%s", text)
	}
}

func TestCLI_SessionTagsSaveListSearchGet(t *testing.T) {
	s := newLabelledStore(t)

	out := mustRunCLI(t, s.path, "session", "get", s.sharedSession, "--format", "json")
	if !strings.Contains(out, `"vis-shared"`) {
		t.Errorf("session save --tags did not store the tag:\n%s", out)
	}
	id := saveSessionCLI(t, s.path, "Comma tags", "sid-commas", "x", "a,,b,")
	out = mustRunCLI(t, s.path, "session", "get", id, "--format", "json")
	if !strings.Contains(out, "\"tags\": [\n    \"a\",\n    \"b\"\n  ]") {
		t.Errorf("empty items in --tags should be dropped:\n%s", out)
	}

	out = mustRunCLI(t, s.path, "session", "list", "--tag", "vis-shared")
	if !strings.Contains(out, "Release review") || strings.Contains(out, "Secret diary") {
		t.Errorf("session list --tag vis-shared:\n%s", out)
	}
	if out := mustRunCLI(t, s.path, "session", "list", "--tag", "no-such-tag"); !strings.Contains(out, "No sessions found.") {
		t.Errorf("session list --tag no-such-tag:\n%s", out)
	}
	out = mustRunCLI(t, s.path, "session", "search", "labelkw", "--tag", "vis-shared")
	if !strings.Contains(out, "Release review") || strings.Contains(out, "Secret diary") {
		t.Errorf("session search --tag vis-shared:\n%s", out)
	}

	out = mustRunCLI(t, s.path, "search", "labelkw", "--type", "session", "--tag", "vis-shared", "--format", "json")
	var results []storage.SearchResult
	json.Unmarshal([]byte(out), &results)
	if len(results) != 1 || results[0].ID != s.sharedSession {
		t.Errorf("search --type session --tag vis-shared = %s", out)
	}

	if out := mustRunCLI(t, s.path, "session", "get", s.sharedSession[:8], "--tag", "vis-shared"); !strings.Contains(out, "Release review") {
		t.Errorf("session get --tag vis-shared on a shared session:\n%s", out)
	}
	untaggedOut, untagged := runCLI(t, s.path, "session", "get", s.personalSession[:8], "--tag", "vis-shared")
	absentOut, absent := runCLI(t, s.path, "session", "get", "ffffffff", "--tag", "vis-shared")
	if untagged == nil || absent == nil {
		t.Fatalf("errors: untagged %v, absent %v", untagged, absent)
	}
	if strings.Replace(absent.Error(), "ffffffff", s.personalSession[:8], 1) != untagged.Error() || untaggedOut != absentOut {
		t.Errorf("a personal session must read as absent:\nuntagged: %v %q\nabsent:   %v %q", untagged, untaggedOut, absent, absentOut)
	}
	if strings.Contains(untagged.Error()+untaggedOut, "Secret") {
		t.Errorf("not-found error leaks the title: %v", untagged)
	}
}

func TestCLI_NoteGetTag(t *testing.T) {
	s := newLabelledStore(t)

	if out := mustRunCLI(t, s.path, "note", "get", s.sharedNote[:8], "--tag", "vis-shared"); !strings.Contains(out, "Deploy runbook") {
		t.Errorf("note get --tag on a shared note:\n%s", out)
	}
	untaggedOut, untagged := runCLI(t, s.path, "note", "get", s.personalNote, "--tag", "vis-shared")
	absentOut, absent := runCLI(t, s.path, "note", "get", "ffffffff-ffff-ffff-ffff-ffffffffffff", "--tag", "vis-shared")
	if untagged == nil || absent == nil {
		t.Fatalf("errors: untagged %v, absent %v", untagged, absent)
	}
	if strings.Replace(absent.Error(), "ffffffff-ffff-ffff-ffff-ffffffffffff", s.personalNote, 1) != untagged.Error() || untaggedOut != absentOut {
		t.Errorf("a personal note must read as absent:\nuntagged: %v\nabsent:   %v", untagged, absent)
	}

	// category + title, and the title-pattern fallback, see only tagged notes
	_, exact := runCLI(t, s.path, "note", "get", "life", "Private holiday", "--tag", "vis-shared")
	_, none := runCLI(t, s.path, "note", "get", "life", "No such title", "--tag", "vis-shared")
	if exact == nil || none == nil || strings.Replace(none.Error(), "No such title", "Private holiday", 1) != exact.Error() {
		t.Errorf("exact title of a personal note: %v (absent title: %v)", exact, none)
	}
	addNoteCLI(t, s.path, "ops", "Ops diary", "x", "vis-personal")
	out := mustRunCLI(t, s.path, "note", "get", "ops", "o", "--tag", "vis-shared")
	if !strings.Contains(out, "Deploy runbook") || strings.Contains(out, "Ops diary") {
		t.Errorf("title pattern with --tag listed a personal note:\n%s", out)
	}
	if out := mustRunCLI(t, s.path, "note", "get", "ops", "o"); !strings.Contains(out, "Ops diary") || !strings.Contains(out, "Deploy runbook") {
		t.Errorf("title pattern without --tag should list both:\n%s", out)
	}
}

func noteJSON(t *testing.T, storePath, id string) map[string]interface{} {
	t.Helper()
	var note map[string]interface{}
	out := mustRunCLI(t, storePath, "note", "get", id, "--format", "json")
	if err := json.Unmarshal([]byte(out), &note); err != nil {
		t.Fatalf("parse note: %v\n%s", err, out)
	}
	return note
}

func TestCLI_NoteUpdateTags(t *testing.T) {
	storePath := t.TempDir()
	id := addNoteCLI(t, storePath, "work", "Label me", "body", "a,b")

	// --tags replaces the whole list (this is what it always did)
	mustRunCLI(t, storePath, "note", "update", id, "--tags", "c")
	if got := fmt.Sprint(noteJSON(t, storePath, id)["tags"]); got != "[c]" {
		t.Errorf("--tags c: tags = %s, want [c]", got)
	}
	before := noteJSON(t, storePath, id)

	out := mustRunCLI(t, storePath, "note", "update", id[:8], "--add-tag", "vis-shared, d")
	if !strings.Contains(out, "Updated tags of note \"Label me\"") {
		t.Errorf("--add-tag output: %s", out)
	}
	after := noteJSON(t, storePath, id)
	if got := fmt.Sprint(after["tags"]); got != "[c vis-shared d]" {
		t.Errorf("--add-tag: tags = %s, want [c vis-shared d]", got)
	}
	if before["updated"] != after["updated"] || before["content"] != after["content"] {
		t.Errorf("a tags-only edit changed updated or content: %v -> %v", before["updated"], after["updated"])
	}
	mustRunCLI(t, storePath, "note", "update", id, "--remove-tag", "C")
	if got := fmt.Sprint(noteJSON(t, storePath, id)["tags"]); got != "[vis-shared d]" {
		t.Errorf("--remove-tag C: tags = %s", got)
	}
	if out := mustRunCLI(t, storePath, "note", "update", id, "--add-tag", "d"); !strings.Contains(out, "already as requested") {
		t.Errorf("a no-op edit should say so: %s", out)
	}

	mustRunCLI(t, storePath, "note", "update", id, "--title", "Labelled", "--add-tag", "z", "--remove-tag", "d")
	note := noteJSON(t, storePath, id)
	if note["title"] != "Labelled" || fmt.Sprint(note["tags"]) != "[vis-shared z]" {
		t.Errorf("--title with --add-tag/--remove-tag: %v %v", note["title"], note["tags"])
	}

	for _, args := range [][]string{
		{"--tags", "x", "--add-tag", "y"},
		{"--add-tag", "two words"},
		{"--add-tag", "x", "--remove-tag", "x"},
		{},
	} {
		if _, err := runCLI(t, storePath, append([]string{"note", "update", id}, args...)...); err == nil {
			t.Errorf("note update %v was accepted", args)
		}
	}
}

func TestCLI_SessionUpdateTags(t *testing.T) {
	s := newLabelledStore(t)
	out := mustRunCLI(t, s.path, "session", "update", s.personalSession[:8], "--add-tag", "vis-shared", "--remove-tag", "vis-personal")
	if !strings.Contains(out, "Updated tags of session \"Secret diary\"") {
		t.Errorf("session update output: %s", out)
	}
	if out := mustRunCLI(t, s.path, "session", "get", s.personalSession, "--tag", "vis-shared"); !strings.Contains(out, "Secret diary") {
		t.Errorf("relabelled session not found by its new tag:\n%s", out)
	}
	if _, err := runCLI(t, s.path, "session", "get", s.personalSession, "--tag", "vis-personal"); err == nil {
		t.Error("removed tag still matches")
	}
	if _, err := runCLI(t, s.path, "session", "update", s.personalSession); err == nil {
		t.Error("session update without --add-tag/--remove-tag was accepted")
	}
}

func TestCLI_TagApply(t *testing.T) {
	s := newLabelledStore(t)
	thirdNote := addNoteCLI(t, s.path, "misc", "Loose ends", "labelkw todo", "")

	write := func(name, content string) string {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	edits := write("labels.jsonl", fmt.Sprintf(`{"id": %q, "kind": "note", "add": ["vis-shared"]}
{"id": %q, "kind": "note", "add": ["vis-shared"], "remove": ["vis-personal"]}
{"id": %q, "kind": "note", "add": ["vis-shared"]}
{"id": %q, "kind": "session", "add": ["vis-shared"]}
{"id": %q, "kind": "session", "add": ["vis-shared"], "remove": ["vis-personal"]}
`, s.sharedNote, s.personalNote, thirdNote, s.sharedSession, s.personalSession))

	tagged := func() int {
		var results []storage.SearchResult
		json.Unmarshal([]byte(mustRunCLI(t, s.path, "search", "labelkw", "--tag", "vis-shared", "--format", "json")), &results)
		return len(results)
	}

	var report storage.TagApplyReport
	out := mustRunCLI(t, s.path, "tag", "apply", "--file", edits, "--dry-run", "--format", "json")
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parse report: %v\n%s", err, out)
	}
	if !report.DryRun || report.Entries != 5 || report.Changed != 3 || report.NotesChanged != 2 || report.SessionsChanged != 1 || report.Unchanged != 2 {
		t.Errorf("dry run report = %+v", report)
	}
	if n := tagged(); n != 2 {
		t.Errorf("after the dry run %d entries are tagged, want the original 2", n)
	}

	out = mustRunCLI(t, s.path, "tag", "apply", "--file", edits)
	if !strings.HasPrefix(out, "tag apply: 5 entries, 3 changed (2 notes, 1 sessions), 2 unchanged, 0 unknown, 0 failed") {
		t.Errorf("apply report:\n%s", out)
	}
	if n := tagged(); n != 5 {
		t.Errorf("after apply %d entries are tagged, want 5", n)
	}
	if out := mustRunCLI(t, s.path, "tag", "apply", "--file", edits); !strings.Contains(out, "0 changed (0 notes, 0 sessions), 5 unchanged") {
		t.Errorf("second apply is not a no-op:\n%s", out)
	}

	unknown := write("unknown.jsonl", fmt.Sprintf(`{"id": %q, "kind": "session", "add": ["x"]}
{"id": "00000000-0000-0000-0000-000000000000", "kind": "note", "add": ["x"]}
`, s.sharedNote))
	out, err := runCLI(t, s.path, "tag", "apply", "--file", unknown)
	if err == nil || !strings.Contains(err.Error(), "2 unknown ids") {
		t.Errorf("unknown ids must fail the run: %v", err)
	}
	if !strings.Contains(out, "unknown, line 1: entry is of the other kind") || !strings.Contains(out, "unknown, line 2: entry not found: note 00000000-0000-0000-0000-000000000000") {
		t.Errorf("unknown ids report:\n%s", out)
	}

	broken := write("broken.jsonl", fmt.Sprintf(`{"id": %q, "kind": "note", "add": ["y"]}
{"id": "x", "kind": "page"}
`, s.sharedNote))
	if _, err := runCLI(t, s.path, "tag", "apply", "--file", broken); err == nil || !strings.Contains(err.Error(), "1 invalid lines, nothing applied") {
		t.Errorf("an invalid line must stop the run: %v", err)
	}
	if _, err := runCLI(t, s.path, "note", "get", s.sharedNote, "--tag", "y"); err == nil {
		t.Errorf("a valid line of a rejected file was applied")
	}
}

// Writes never merge across the vis-shared label: note add dedup, session save merge, session resolve.
func TestCLI_LabelAwareWrites(t *testing.T) {
	s := newLabelledStore(t)

	// note add with a personal note's exact title, from the shared side: a new note, nothing about the other
	out := mustRunCLI(t, s.path, "note", "add", "life", "--title", "Private holiday", "--content", "SHARED", "--tags", "vis-shared", "--format", "json")
	var res storage.AddNoteResult
	json.Unmarshal([]byte(out), &res)
	if res.Action != "created" || res.NoteID == s.personalNote || strings.Contains(out, s.personalNote[:8]) {
		t.Errorf("a shared add merged into or named the personal note: %s", out)
	}
	if out := mustRunCLI(t, s.path, "note", "get", s.personalNote); strings.Contains(out, "SHARED") {
		t.Errorf("shared text reached the personal note:\n%s", out)
	}

	// session save with a personal session's session id, from the shared side: refused, nothing named
	out, err := runCLI(t, s.path, "session", "save", "--title", "Company wrapup", "--branch", "main", "--project", "proj",
		"--session-id", "sid-personal", "--summary", "SHARED", "--tags", "vis-shared")
	if err == nil || !strings.Contains(err.Error(), "refusing to save") {
		t.Fatalf("cross-label session save: %v\n%s", err, out)
	}
	if strings.Contains(err.Error()+out, s.personalSession[:8]) || strings.Contains(err.Error()+out, "Secret diary") {
		t.Errorf("the refusal names the personal session: %v %s", err, out)
	}

	// session resolve resumes only a session on the caller's side of the label
	resolve := func(args ...string) (string, bool) {
		var r struct {
			SessionID string `json:"session_id"`
			Existing  bool   `json:"existing"`
		}
		json.Unmarshal([]byte(mustRunCLI(t, s.path, append([]string{"session", "resolve", "--project", "proj", "--format", "json"}, args...)...)), &r)
		return r.SessionID, r.Existing
	}
	if id, existing := resolve("--tag", "vis-shared"); !existing || id != "sid-shared" {
		t.Errorf("resolve --tag vis-shared = %q existing=%v, want sid-shared", id, existing)
	}
	if id, existing := resolve(); !existing || id != "sid-personal" {
		t.Errorf("resolve without --tag = %q existing=%v, want sid-personal", id, existing)
	}
}

func TestCLI_StatsTag(t *testing.T) {
	s := newLabelledStore(t)
	out := mustRunCLI(t, s.path, "stats", "--tag", "vis-shared", "--format", "json")
	var stats storage.TaggedStats
	if err := json.Unmarshal([]byte(out), &stats); err != nil {
		t.Fatalf("parse stats: %v\n%s", err, out)
	}
	if stats.TotalNotes != 1 || stats.TotalSessions != 1 || len(stats.Categories) != 1 || stats.Categories[0].Name != "ops" {
		t.Errorf("stats --tag vis-shared = %+v", stats)
	}
	text := mustRunCLI(t, s.path, "stats", "--tag", "vis-shared")
	for _, title := range []string{"Deploy runbook", "Release review", "Private holiday", "Secret diary"} {
		if strings.Contains(out+text, title) {
			t.Errorf("stats --tag printed the title %q:\n%s", title, text)
		}
	}
	if !strings.Contains(text, "Notes:    1\nSessions: 1\n") || strings.Contains(text, "Storage:") {
		t.Errorf("stats --tag text:\n%s", text)
	}
	if _, err := runCLI(t, s.path, "stats", "--tag", "vis-shared", "--top-accessed"); err == nil {
		t.Error("--top-accessed with --tag was accepted")
	}
}
