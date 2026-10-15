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
// These run the real command tree in-process, as a script runs the binary. runCLI
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

// isTempPath reports whether path lies inside a directory that t.TempDir creates under: GOTMPDIR when
// it is set (go test puts test directories there), else the system temp directory.
func isTempPath(path string) bool {
	for _, root := range []string{os.Getenv("GOTMPDIR"), os.TempDir()} {
		if root == "" {
			continue
		}
		if rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path)); err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

// The runCLI guard takes every directory t.TempDir makes, also under a GOTMPDIR outside TMPDIR, and
// nothing outside the temp roots.
func TestIsTempPath(t *testing.T) {
	if dir := t.TempDir(); !isTempPath(dir) {
		t.Errorf("t.TempDir() = %q is not taken as temporary", dir)
	}
	for _, path := range []string{"/", "/no-such-root/.claudemem", os.TempDir()} {
		if isTempPath(path) {
			t.Errorf("%q is taken as temporary", path)
		}
	}
}

// runCLI runs `claudemem --store <storePath> args...` and returns what it printed on stdout.
// Cobra keeps flag values between runs, so every flag is reset to its default first.
func runCLI(t *testing.T, storePath string, args ...string) (string, error) {
	t.Helper()
	if storePath == "" || !isTempPath(storePath) {
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
type taggedStore struct {
	path                           string
	sharedNote, personalNote       string
	sharedSession, personalSession string
}

func newTaggedStore(t *testing.T) taggedStore {
	t.Helper()
	s := taggedStore{path: t.TempDir()}
	s.sharedNote = addNoteCLI(t, s.path, "ops", "Deploy runbook", "labelkw how to deploy", "team,ops")
	s.personalNote = addNoteCLI(t, s.path, "life", "Private holiday", "labelkw beach in May", "personal")
	s.sharedSession = saveSessionCLI(t, s.path, "Release review", "sid-shared", "labelkw shipped the release", "team")
	s.personalSession = saveSessionCLI(t, s.path, "Secret diary", "sid-personal", "labelkw private thoughts", "personal")
	return s
}

func TestCLI_ContextInjectTag(t *testing.T) {
	s := newTaggedStore(t)

	out := mustRunCLI(t, s.path, "context", "inject", "--tag", "team", "--format", "json")
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
		t.Errorf("filtered inject shows an entry without the tag:\n%s", out)
	}

	text := mustRunCLI(t, s.path, "context", "inject", "--tag", "team")
	if !strings.HasPrefix(text, "claudemem context (1 notes, 1 sessions tagged team)") {
		t.Errorf("text header:\n%s", text)
	}
	if strings.Contains(text, "Private") || strings.Contains(text, "Secret") {
		t.Errorf("filtered inject text shows an entry without the tag:\n%s", text)
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
	s := newTaggedStore(t)

	out := mustRunCLI(t, s.path, "session", "get", s.sharedSession, "--format", "json")
	if !strings.Contains(out, `"team"`) {
		t.Errorf("session save --tags did not store the tag:\n%s", out)
	}
	id := saveSessionCLI(t, s.path, "Comma tags", "sid-commas", "x", "a,,b,")
	out = mustRunCLI(t, s.path, "session", "get", id, "--format", "json")
	if !strings.Contains(out, "\"tags\": [\n    \"a\",\n    \"b\"\n  ]") {
		t.Errorf("empty items in --tags should be dropped:\n%s", out)
	}

	out = mustRunCLI(t, s.path, "session", "list", "--tag", "team")
	if !strings.Contains(out, "Release review") || strings.Contains(out, "Secret diary") {
		t.Errorf("session list --tag team:\n%s", out)
	}
	if out := mustRunCLI(t, s.path, "session", "list", "--tag", "no-such-tag"); !strings.Contains(out, "No sessions found.") {
		t.Errorf("session list --tag no-such-tag:\n%s", out)
	}
	out = mustRunCLI(t, s.path, "session", "search", "labelkw", "--tag", "team")
	if !strings.Contains(out, "Release review") || strings.Contains(out, "Secret diary") {
		t.Errorf("session search --tag team:\n%s", out)
	}

	out = mustRunCLI(t, s.path, "search", "labelkw", "--type", "session", "--tag", "team", "--format", "json")
	var results []storage.SearchResult
	json.Unmarshal([]byte(out), &results)
	if len(results) != 1 || results[0].ID != s.sharedSession {
		t.Errorf("search --type session --tag team = %s", out)
	}

	if out := mustRunCLI(t, s.path, "session", "get", s.sharedSession[:8], "--tag", "team"); !strings.Contains(out, "Release review") {
		t.Errorf("session get --tag team on a shared session:\n%s", out)
	}
	// tags filter, they do not hide entries: a session without the tags is reported as such
	_, untagged := runCLI(t, s.path, "session", "get", s.personalSession[:8], "--tag", "team")
	_, absent := runCLI(t, s.path, "session", "get", "ffffffff", "--tag", "team")
	if untagged == nil || !strings.Contains(untagged.Error(), "session "+s.personalSession[:8]+" does not carry the tag team") {
		t.Errorf("session get --tag on a session without the tag: %v", untagged)
	}
	if absent == nil || !strings.Contains(absent.Error(), "session not found: ffffffff") {
		t.Errorf("session get --tag on an absent id: %v", absent)
	}
}

func TestCLI_NoteGetTag(t *testing.T) {
	s := newTaggedStore(t)

	if out := mustRunCLI(t, s.path, "note", "get", s.sharedNote[:8], "--tag", "team"); !strings.Contains(out, "Deploy runbook") {
		t.Errorf("note get --tag on a shared note:\n%s", out)
	}
	// tags filter, they do not hide entries: a note without the tags is reported as such
	_, untagged := runCLI(t, s.path, "note", "get", s.personalNote, "--tag", "team")
	_, absent := runCLI(t, s.path, "note", "get", "ffffffff-ffff-ffff-ffff-ffffffffffff", "--tag", "team")
	if untagged == nil || !strings.Contains(untagged.Error(), "note "+s.personalNote+" does not carry the tag team") {
		t.Errorf("note get --tag on a note without the tag: %v", untagged)
	}
	if absent == nil || !strings.Contains(absent.Error(), "note not found: ffffffff-ffff-ffff-ffff-ffffffffffff") {
		t.Errorf("note get --tag on an absent id: %v", absent)
	}

	// category + title, and the title-pattern fallback, consider only tagged notes
	_, exact := runCLI(t, s.path, "note", "get", "life", "Private holiday", "--tag", "team")
	if exact == nil || !strings.Contains(exact.Error(), `no notes found in life matching "Private holiday" that carry the tags team`) {
		t.Errorf("exact title of a note without the tag: %v", exact)
	}
	addNoteCLI(t, s.path, "ops", "Ops diary", "x", "personal")
	out := mustRunCLI(t, s.path, "note", "get", "ops", "o", "--tag", "team")
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

	out := mustRunCLI(t, storePath, "note", "update", id[:8], "--add-tag", "team, d")
	if !strings.Contains(out, "Updated tags of note \"Label me\"") {
		t.Errorf("--add-tag output: %s", out)
	}
	after := noteJSON(t, storePath, id)
	if got := fmt.Sprint(after["tags"]); got != "[c team d]" {
		t.Errorf("--add-tag: tags = %s, want [c team d]", got)
	}
	if before["updated"] != after["updated"] || before["content"] != after["content"] {
		t.Errorf("a tags-only edit changed updated or content: %v -> %v", before["updated"], after["updated"])
	}
	mustRunCLI(t, storePath, "note", "update", id, "--remove-tag", "C")
	if got := fmt.Sprint(noteJSON(t, storePath, id)["tags"]); got != "[team d]" {
		t.Errorf("--remove-tag C: tags = %s", got)
	}
	if out := mustRunCLI(t, storePath, "note", "update", id, "--add-tag", "d"); !strings.Contains(out, "already as requested") {
		t.Errorf("a no-op edit should say so: %s", out)
	}

	mustRunCLI(t, storePath, "note", "update", id, "--title", "Labelled", "--add-tag", "z", "--remove-tag", "d")
	note := noteJSON(t, storePath, id)
	if note["title"] != "Labelled" || fmt.Sprint(note["tags"]) != "[team z]" {
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
	s := newTaggedStore(t)
	out := mustRunCLI(t, s.path, "session", "update", s.personalSession[:8], "--add-tag", "team", "--remove-tag", "personal")
	if !strings.Contains(out, "Updated tags of session \"Secret diary\"") {
		t.Errorf("session update output: %s", out)
	}
	if out := mustRunCLI(t, s.path, "session", "get", s.personalSession, "--tag", "team"); !strings.Contains(out, "Secret diary") {
		t.Errorf("retagged session not found by its new tag:\n%s", out)
	}
	if _, err := runCLI(t, s.path, "session", "get", s.personalSession, "--tag", "personal"); err == nil {
		t.Error("removed tag still matches")
	}
	if _, err := runCLI(t, s.path, "session", "update", s.personalSession); err == nil {
		t.Error("session update without --add-tag/--remove-tag was accepted")
	}
}

func TestCLI_TagApply(t *testing.T) {
	s := newTaggedStore(t)
	thirdNote := addNoteCLI(t, s.path, "misc", "Loose ends", "labelkw todo", "")

	write := func(name, content string) string {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	edits := write("labels.jsonl", fmt.Sprintf(`{"id": %q, "kind": "note", "add": ["team"]}
{"id": %q, "kind": "note", "add": ["team"], "remove": ["personal"]}
{"id": %q, "kind": "note", "add": ["team"]}
{"id": %q, "kind": "session", "add": ["team"]}
{"id": %q, "kind": "session", "add": ["team"], "remove": ["personal"]}
`, s.sharedNote, s.personalNote, thirdNote, s.sharedSession, s.personalSession))

	tagged := func() int {
		var results []storage.SearchResult
		json.Unmarshal([]byte(mustRunCLI(t, s.path, "search", "labelkw", "--tag", "team", "--format", "json")), &results)
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

func TestCLI_StatsTag(t *testing.T) {
	s := newTaggedStore(t)
	out := mustRunCLI(t, s.path, "stats", "--tag", "team", "--format", "json")
	var stats struct {
		storage.TaggedStats
		RecentEntries []storage.SearchResult `json:"recent_entries"`
	}
	if err := json.Unmarshal([]byte(out), &stats); err != nil {
		t.Fatalf("parse stats: %v\n%s", err, out)
	}
	if stats.TotalNotes != 1 || stats.TotalSessions != 1 || len(stats.Categories) != 1 || stats.Categories[0].Name != "ops" || len(stats.RecentEntries) != 2 {
		t.Errorf("stats --tag team = %+v", stats)
	}

	// the dashboard over the tagged entries: their titles, never the others'
	text := mustRunCLI(t, s.path, "stats", "--tag", "team")
	if !strings.Contains(text, "Notes:    1\nSessions: 1\n") || strings.Contains(text, "Storage:") ||
		!strings.Contains(text, "Recent Activity:") || !strings.Contains(text, "Deploy runbook") || !strings.Contains(text, "Release review") {
		t.Errorf("stats --tag text:\n%s", text)
	}
	for _, title := range []string{"Private holiday", "Secret diary"} {
		if strings.Contains(out+text, title) {
			t.Errorf("stats --tag team printed the untagged %q:\n%s", title, text)
		}
	}

	// --top-accessed lists the most read tagged entries
	mustRunCLI(t, s.path, "note", "get", s.personalNote)
	mustRunCLI(t, s.path, "note", "get", s.personalNote)
	mustRunCLI(t, s.path, "note", "get", s.sharedNote)
	var accessed struct {
		TopAccessed []storage.AccessStat `json:"top_accessed"`
	}
	if err := json.Unmarshal([]byte(mustRunCLI(t, s.path, "stats", "--tag", "team", "--top-accessed", "--format", "json")), &accessed); err != nil {
		t.Fatal(err)
	}
	if len(accessed.TopAccessed) != 1 || accessed.TopAccessed[0].ID != s.sharedNote {
		t.Errorf("stats --tag team --top-accessed = %+v, want the shared note only", accessed.TopAccessed)
	}
	if text := mustRunCLI(t, s.path, "stats", "--tag", "team", "--top-accessed"); !strings.Contains(text, "Top Accessed:") || strings.Contains(text, "Private holiday") {
		t.Errorf("stats --tag --top-accessed text:\n%s", text)
	}
}

// --tag adds up when repeated; a --tag that names no tag is an error, never "no filter".
func TestCLI_TagFlagsRepeatAndRefuseEmpty(t *testing.T) {
	s := newTaggedStore(t)
	teamOnly := addNoteCLI(t, s.path, "misc", "Team only", "labelkw team only", "team")

	var results []storage.SearchResult
	json.Unmarshal([]byte(mustRunCLI(t, s.path, "search", "labelkw", "--tag", "team", "--tag", "ops", "--format", "json")), &results)
	var ids []string
	for _, r := range results {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != s.sharedNote {
		t.Errorf("--tag team --tag ops = %v, want only the note carrying both (%s; %s carries team only)", ids, s.sharedNote, teamOnly)
	}

	for _, args := range [][]string{
		{"search", "labelkw"},
		{"note", "search", "labelkw"},
		{"note", "get", s.sharedNote},
		{"session", "list"},
		{"session", "search", "labelkw"},
		{"session", "get", s.sharedSession},
		{"context", "inject"},
		{"stats"},
	} {
		for _, empty := range []string{"", " , "} {
			out, err := runCLI(t, s.path, append(args, "--tag", "team", "--tag", empty)...)
			if err == nil || !strings.Contains(err.Error(), "--tag was given but names no tag") {
				t.Errorf("%v --tag team --tag %q: %v\n%s", args, empty, err, out)
			}
		}
	}

	mustRunCLI(t, s.path, "note", "update", s.personalNote, "--add-tag", "a", "--add-tag", "b,c", "--remove-tag", "personal")
	if got := fmt.Sprint(noteJSON(t, s.path, s.personalNote)["tags"]); got != "[a b c]" {
		t.Errorf("repeated --add-tag: tags = %s, want [a b c]", got)
	}
	if _, err := runCLI(t, s.path, "note", "update", s.personalNote, "--add-tag", ""); err == nil {
		t.Error("note update --add-tag \"\" was accepted")
	}
	if _, err := runCLI(t, s.path, "session", "update", s.personalSession, "--remove-tag", " , "); err == nil {
		t.Error("session update --remove-tag \" , \" was accepted")
	}
}

// A tag is one word on every write path; the error names a one-word form.
func TestCLI_TagsAreOneWord(t *testing.T) {
	storePath := t.TempDir()
	for _, args := range [][]string{
		{"note", "add", "work", "--title", "Two words", "--content", "x", "--tags", "machine learning"},
		{"session", "save", "--title", "Two words", "--branch", "main", "--project", "p", "--session-id", "sid-x", "--summary", "x", "--tags", "machine learning"},
	} {
		out, err := runCLI(t, storePath, args...)
		if err == nil || !strings.Contains(err.Error(), `a tag is one word, for example "machine-learning"`) {
			t.Errorf("%v: %v\n%s", args[:2], err, out)
		}
	}
	id := addNoteCLI(t, storePath, "work", "One word", "x", "machine-learning")
	if _, err := runCLI(t, storePath, "note", "update", id, "--tags", "deep learning"); err == nil {
		t.Error("note update --tags accepted a tag with a space")
	}
	if _, err := runCLI(t, storePath, "note", "update", id, "--add-tag", "deep learning"); err == nil {
		t.Error("note update --add-tag accepted a tag with a space")
	}
}

// With several stores, a get --tag on an entry that sits in a store read without the tags says so,
// naming that store.
func TestCLI_ReadStores_GetTagNamesTheStoreWithoutTheTags(t *testing.T) {
	s := newTwoStores(t)
	theirs := addNoteCLI(t, s.other, "ops", "Their note", "x", "personal")
	sid := saveSessionCLI(t, s.other, "Their session", "sid-x", "x", "personal")
	if _, err := runCLI(t, s.primary, "note", "get", theirs, "--tag", "team"); err == nil || !strings.Contains(err.Error(), "does not carry the tag team (in "+s.otherName+")") {
		t.Errorf("note get --tag on the other store's note: %v", err)
	}
	if _, err := runCLI(t, s.primary, "session", "get", sid, "--tag", "team"); err == nil || !strings.Contains(err.Error(), "does not carry the tag team (in "+s.otherName+")") {
		t.Errorf("session get --tag on the other store's session: %v", err)
	}
	if out := mustRunCLI(t, s.primary, "note", "get", theirs, "--tag", "personal"); !strings.Contains(out, "Their note") {
		t.Errorf("note get --tag personal:\n%s", out)
	}
}
