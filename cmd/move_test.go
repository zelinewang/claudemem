package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/storage"
)

// ─── CLI tests for moving entries between stores ──────────────────────────────

func writeIDs(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ids.txt")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func saveSessionWithRelatedCLI(t *testing.T, storePath, title, sessionID, related string) string {
	t.Helper()
	out := mustRunCLI(t, storePath, "session", "save", "--title", title, "--branch", "main", "--project", "proj",
		"--session-id", sessionID, "--summary", "movekw "+title, "--related-notes", related, "--format", "json")
	var res storage.SaveSessionResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Action != "created" {
		t.Fatalf("session save %q: %v (%s)", title, err, out)
	}
	return res.SessionID
}

// newStoreDir is an empty store: a directory that a command has run in once.
func newStoreDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustRunCLI(t, dir, "stats")
	return dir
}

func TestCLI_Move(t *testing.T) {
	from, to := t.TempDir(), newStoreDir(t)
	note := addNoteCLI(t, from, "ops", "Team runbook", "movekw steps", "team")
	keep := addNoteCLI(t, from, "life", "Holiday plan", "movekw beach", "personal")
	session := saveSessionWithRelatedCLI(t, from, "Team review", "sid-team", note+":Team runbook:ops")
	missing := "ffffffff-0000-0000-0000-000000000000"
	ids := writeIDs(t, "# for the team store", session+"  # Team review", note, "", missing)

	// a dry run reports, changes nothing, and fails on the missing id
	out, err := runCLI(t, from, "move", "--to", to, "--file", ids, "--dry-run", "--format", "json")
	if err == nil || !strings.Contains(err.Error(), "1 missing, 0 refused") {
		t.Errorf("dry run error: %v", err)
	}
	var report storage.MoveReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v (%s)", err, out)
	}
	if !report.DryRun || len(report.Moved) != 2 || report.Moved[0].ID != note || report.Moved[1].ID != session ||
		len(report.Missing) != 1 || report.Missing[0].Line != 5 {
		t.Errorf("dry run report %+v", report)
	}
	if _, err := runCLI(t, to, "note", "get", note); err == nil {
		t.Error("the dry run moved the note")
	}

	out, err = runCLI(t, from, "move", "--to", to, "--file", ids)
	if err == nil || !strings.Contains(err.Error(), "1 missing, 0 refused") {
		t.Errorf("move error: %v", err)
	}
	header := "move: 3 ids from " + displayStoreName(from) + " to " + displayStoreName(to) + ": 2 moved, 0 already in the target, 1 missing, 0 refused"
	if !strings.Contains(out, header) || !strings.Contains(out, "moved note "+note+` "Team runbook" -> notes/ops/team-runbook.md`) ||
		!strings.Contains(out, "missing, line 5: "+missing) {
		t.Errorf("move report:\n%s", out)
	}

	// the target holds them under their ids, this store does not
	if got := noteJSON(t, to, note); got["id"] != note || got["content"] != "movekw steps" {
		t.Errorf("the moved note in the target: %v", got)
	}
	var moved struct {
		ID           string `json:"id"`
		RelatedNotes []struct {
			ID string `json:"id"`
		} `json:"related_notes"`
	}
	if err := json.Unmarshal([]byte(mustRunCLI(t, to, "session", "get", session, "--format", "json")), &moved); err != nil ||
		moved.ID != session || len(moved.RelatedNotes) != 1 || moved.RelatedNotes[0].ID != note {
		t.Errorf("the moved session in the target: %+v (%v)", moved, err)
	}
	for _, args := range [][]string{{"note", "get", note}, {"session", "get", session}} {
		if _, err := runCLI(t, from, args...); err == nil {
			t.Errorf("%v still finds the entry in this store", args)
		}
	}
	if got := noteJSON(t, from, keep); got["id"] != keep {
		t.Errorf("a note not listed moved: %v", got)
	}

	// a store that reads the target still finds them, named by the target
	mustRunCLI(t, from, "config", "set", readStoresKey, to)
	_, stores := entryStores(t, mustRunCLI(t, from, "search", "movekw", "--format", "json"))
	if stores[note] != displayStoreName(to) || stores[session] != displayStoreName(to) || stores[keep] != displayStoreName(from) {
		t.Errorf("search after the move: %v", stores)
	}

	// the same file again changes nothing
	out, _ = runCLI(t, from, "move", "--to", to, "--file", ids, "--format", "json")
	report = storage.MoveReport{}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v (%s)", err, out)
	}
	if len(report.Moved) != 0 || len(report.AlreadyInTarget) != 2 || len(report.Missing) != 1 {
		t.Errorf("second run %+v", report)
	}
}

func TestCLI_MoveRefusals(t *testing.T) {
	from, to := t.TempDir(), newStoreDir(t)
	note := addNoteCLI(t, from, "life", "Private detail", "x", "personal")
	session := saveSessionWithRelatedCLI(t, from, "Mixed review", "sid-mixed", note[:8]+":Private detail:life")

	// a session whose related note stays here is refused, and stays
	out, err := runCLI(t, from, "move", "--to", to, "--file", writeIDs(t, session))
	if err == nil || !strings.Contains(err.Error(), "0 missing, 1 refused") || !strings.Contains(out, "refused, line 1: session "+session+": it lists the related note") {
		t.Errorf("a session with a note that stays: %v\n%s", err, out)
	}
	if _, err := runCLI(t, from, "session", "get", session); err != nil {
		t.Errorf("the refused session left: %v", err)
	}

	ids := writeIDs(t, note)
	plain := t.TempDir() // a directory, not a store
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"move", "--to", from, "--file", ids}, "this store itself"},
		{[]string{"move", "--to", filepath.Join(to, "nope"), "--file", ids}, "not found"},
		{[]string{"move", "--to", plain, "--file", ids}, "is not a claudemem store"},
		{[]string{"move", "--to", "relative/dir", "--file", ids}, "must be absolute"},
		{[]string{"move", "--file", ids}, "needs --to"},
		{[]string{"move", "--to", to}, "needs --to"},
		{[]string{"move", "--to", to, "--file", writeIDs(t, note, note+" extra", note)}, "2 invalid lines, nothing moved"},
	} {
		if _, err := runCLI(t, from, tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: %v, want %q", tc.args, err, tc.want)
		}
	}
	if got := noteJSON(t, from, note); got["id"] != note {
		t.Errorf("a refused run moved the note: %v", got)
	}
	if _, err := os.Stat(filepath.Join(to, "nope")); !os.IsNotExist(err) {
		t.Errorf("a missing target was created: %v", err)
	}
	if entries, err := os.ReadDir(plain); err != nil || len(entries) != 0 {
		t.Errorf("a directory that is not a store was written to: %v %v", entries, err)
	}
}

func TestCLI_MoveReadsStdinAndHomePaths(t *testing.T) {
	from := t.TempDir()
	note := addNoteCLI(t, from, "ops", "Piped note", "x", "team")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w.WriteString(note + "\n")
	w.Close()
	to := newStoreDir(t)
	orig := os.Stdin
	os.Stdin = r
	out := mustRunCLI(t, from, "move", "--to", to, "--file", "-")
	os.Stdin = orig
	if !strings.Contains(out, "1 moved") {
		t.Errorf("--file -:\n%s", out)
	}

	// --to takes a ~/ path (a child process, for its own home)
	home := t.TempDir()
	if out, err := runCLIChild(t, home, "--store", filepath.Join(home, "team"), "stats"); err != nil {
		t.Fatalf("start ~/team: %v\n%s", err, out)
	}
	other := addNoteCLI(t, from, "ops", "Home note", "y", "team")
	out, err = runCLIChild(t, home, "--store", from, "move", "--to", "~/team", "--file", writeIDs(t, other))
	if err != nil || !strings.Contains(out, " to ~/team: 1 moved") {
		t.Errorf("--to ~/team: %v\n%s", err, out)
	}
	if got := noteJSON(t, filepath.Join(home, "team"), other); got["id"] != other {
		t.Errorf("the note in ~/team: %v", got)
	}
}

// A mistyped --store is refused before anything is created, a store inside the other is refused, a
// refusal prints no usage block, and a different copy of a moved file left here makes the exit code 1.
func TestCLI_MoveChecks(t *testing.T) {
	from, to := t.TempDir(), newStoreDir(t)
	note := addNoteCLI(t, from, "ops", "Checked runbook", "checkkw steps", "team")
	ids := writeIDs(t, note)

	typo := filepath.Join(t.TempDir(), "typo")
	if _, err := runCLI(t, typo, "move", "--to", to, "--file", ids, "--dry-run"); err == nil || !strings.Contains(err.Error(), "is not a claudemem store") {
		t.Errorf("a mistyped --store: %v", err)
	}
	if _, err := os.Stat(typo); !os.IsNotExist(err) {
		t.Errorf("the dry run created the mistyped store: %v", err)
	}

	nested := filepath.Join(from, "notes", "team")
	mustRunCLI(t, nested, "stats")
	for _, args := range [][]string{{"--store", from, "move", "--to", nested}, {"--store", nested, "move", "--to", from}} {
		if _, err := runCLI(t, args[1], append(args[2:], "--file", ids)...); err == nil || !strings.Contains(err.Error(), "overlap") {
			t.Errorf("%v: %v", args, err)
		}
	}

	out, err := runCLIChild(t, t.TempDir(), "--store", from, "move", "--to", filepath.Join(to, "nope"), "--file", ids)
	if err == nil || strings.Contains(out, "Usage:") {
		t.Errorf("a refused move printed the usage block: %v\n%s", err, out)
	}

	mustRunCLI(t, from, "move", "--to", to, "--file", ids)
	rel := filepath.Join("notes", "ops", "checked-runbook.md")
	moved, err := os.ReadFile(filepath.Join(to, rel))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(from, rel), []byte(strings.Replace(string(moved), "checkkw steps", "edited here", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	out, err = runCLI(t, from, "move", "--to", to, "--file", ids)
	if err == nil || !strings.Contains(err.Error(), "1 left to clean up") || !strings.Contains(out, "left to clean up, line 1: note "+note+": a different copy") {
		t.Errorf("a different copy left here: %v\n%s", err, out)
	}
}

// A --to inside this store is refused for overlapping before anything else, so the errors never
// suggest making a store there, and nothing is created there.
func TestCLI_MoveNeverSuggestsANestedStore(t *testing.T) {
	from := t.TempDir()
	note := addNoteCLI(t, from, "ops", "Nested check", "x", "team")
	nested := filepath.Join(from, "notes", "team")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	_, err := runCLI(t, from, "move", "--to", nested, "--file", writeIDs(t, note))
	if err == nil || !strings.Contains(err.Error(), "overlap") || strings.Contains(err.Error(), "--store") {
		t.Errorf("--to a directory inside this store: %v", err)
	}
	if entries, readErr := os.ReadDir(nested); readErr != nil || len(entries) != 0 {
		t.Errorf("something was written in the nested directory: %v %v", entries, readErr)
	}
}
