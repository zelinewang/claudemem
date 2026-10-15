package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/storage"
)

// ─── CLI tests for reading several stores ─────────────────────────────────────

// twoStores is a primary store whose stores.read setting names a second store.
type twoStores struct {
	primary, other         string
	primaryName, otherName string
}

func newTwoStores(t *testing.T) twoStores {
	t.Helper()
	s := twoStores{primary: t.TempDir(), other: t.TempDir()}
	s.primaryName, s.otherName = displayStoreName(s.primary), displayStoreName(s.other)
	mustRunCLI(t, s.primary, "config", "set", readStoresKey, s.other)
	return s
}

// entryStores maps each listed entry's id to the store it names, from a JSON list.
func entryStores(t *testing.T, out string) (ids []string, stores map[string]string) {
	t.Helper()
	var list []struct {
		ID    string `json:"id"`
		Store string `json:"store"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("parse %s: %v", out, err)
	}
	stores = make(map[string]string)
	for _, e := range list {
		ids = append(ids, e.ID)
		stores[e.ID] = e.Store
	}
	return ids, stores
}

func TestCLI_ReadStores_SearchReadsEveryStore(t *testing.T) {
	s := newTwoStores(t)
	mine := addNoteCLI(t, s.primary, "ops", "Primary runbook", "storekw primary text", "team")
	theirs := addNoteCLI(t, s.other, "ops", "Other runbook", "storekw other text", "team")
	addNoteCLI(t, s.other, "misc", "Grocery checklist", "storekw untagged list", "misc")

	ids, stores := entryStores(t, mustRunCLI(t, s.primary, "search", "storekw", "--tag", "team", "--format", "json"))
	if len(ids) != 2 || ids[0] != mine || ids[1] != theirs {
		t.Fatalf("search --tag team = %v, want %s then %s (merged by rank, the primary first)", ids, mine, theirs)
	}
	if stores[mine] != s.primaryName || stores[theirs] != s.otherName {
		t.Errorf("stores named: %v", stores)
	}
	if ids, _ := entryStores(t, mustRunCLI(t, s.primary, "search", "storekw", "--format", "json")); len(ids) != 3 {
		t.Errorf("search without --tag found %d entries, want 3", len(ids))
	}
	if ids, _ := entryStores(t, mustRunCLI(t, s.primary, "search", "storekw", "--limit", "2", "--format", "json")); len(ids) != 2 {
		t.Errorf("search --limit 2 over two stores returned %d entries", len(ids))
	}
	if _, stores := entryStores(t, mustRunCLI(t, s.primary, "search", "storekw", "--compact", "--format", "json")); stores[theirs] != s.otherName {
		t.Errorf("compact JSON names %q for the other store's note", stores[theirs])
	}
	if out := mustRunCLI(t, s.primary, "search", "storekw"); !strings.Contains(out, "store: "+s.otherName) {
		t.Errorf("text output does not name the store:\n%s", out)
	}
	if out := mustRunCLI(t, s.primary, "search", "storekw", "--compact"); !strings.Contains(out, "["+s.otherName+"]") {
		t.Errorf("compact text does not name the store:\n%s", out)
	}

	// --read-stores "" reads the primary alone, with the output of a single store
	out := mustRunCLI(t, s.primary, "--read-stores", "", "search", "storekw", "--format", "json")
	if ids, _ := entryStores(t, out); len(ids) != 1 || ids[0] != mine {
		t.Errorf(`--read-stores "" found %v, want only %s`, ids, mine)
	}
	if strings.Contains(out, `"store"`) {
		t.Errorf("one store read, yet the output names stores:\n%s", out)
	}
}

func TestCLI_ReadStores_WritesGoToThePrimary(t *testing.T) {
	s := newTwoStores(t)
	mine := addNoteCLI(t, s.primary, "ops", "Written here", "writekw", "team")
	if _, err := runCLI(t, s.other, "note", "get", mine); err == nil {
		t.Error("a note added through the primary landed in the store it reads")
	}

	theirs := addNoteCLI(t, s.other, "ops", "Kept there", "theirs", "team")
	_, err := runCLI(t, s.primary, "note", "update", theirs, "--content", "changed")
	if err == nil {
		t.Fatal("note update through the primary changed a note of the store it reads")
	}
	if !strings.Contains(err.Error(), "the note is in "+s.otherName) || !strings.Contains(err.Error(), "--store "+s.otherName) {
		t.Errorf("the error does not say where the note is: %v", err)
	}
	if got := noteJSON(t, s.other, theirs)["content"]; got != "theirs" {
		t.Errorf("the other store's note changed: %v", got)
	}
	for _, args := range [][]string{
		{"note", "append", theirs, "more"},
		{"note", "delete", theirs},
		{"note", "update", theirs, "--add-tag", "x"},
	} {
		if _, err := runCLI(t, s.primary, args...); err == nil || !strings.Contains(err.Error(), "--store "+s.otherName) {
			t.Errorf("%v: %v", args, err)
		}
	}
	session := saveSessionCLI(t, s.other, "Their session", "sid-their", "theirs", "team")
	if _, err := runCLI(t, s.primary, "session", "update", session, "--add-tag", "x"); err == nil || !strings.Contains(err.Error(), "the session is in "+s.otherName) {
		t.Errorf("session update: %v", err)
	}
	if _, err := runCLI(t, s.primary, "note", "update", "no-such-note", "--content", "x"); err == nil || strings.Contains(err.Error(), "--store") {
		t.Errorf("a note in no store: %v", err)
	}
}

func TestCLI_ReadStores_NoteGetAndList(t *testing.T) {
	s := newTwoStores(t)
	mine := addNoteCLI(t, s.primary, "ops", "Primary runbook", "x", "team")
	theirs := addNoteCLI(t, s.other, "ops", "Other runbook", "y", "team")

	for _, args := range [][]string{
		{"note", "get", theirs},
		{"note", "get", theirs[:8]},
		{"note", "get", "ops", "Other runbook"},
		{"note", "get", "ops", "other"},
	} {
		got := map[string]interface{}{}
		out := mustRunCLI(t, s.primary, append(args, "--format", "json")...)
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%v: %v (%s)", args, err, out)
		}
		if got["id"] != theirs || got["store"] != s.otherName {
			t.Errorf("%v: id %v store %v", args, got["id"], got["store"])
		}
	}
	if out := mustRunCLI(t, s.primary, "note", "get", theirs); !strings.Contains(out, "Store: "+s.otherName) {
		t.Errorf("text output does not name the store:\n%s", out)
	}
	if _, err := runCLI(t, s.primary, "note", "get", theirs, "--tag", "personal"); err == nil {
		t.Error("note get --tag personal returned a note without the tag")
	}
	if _, err := runCLI(t, s.primary, "note", "get", "no-such-note"); err == nil {
		t.Error("note get of a note in no store succeeded")
	}
	if out := mustRunCLI(t, s.primary, "note", "get", "ops", "runbook"); !strings.Contains(out, "Multiple notes") || !strings.Contains(out, s.otherName) {
		t.Errorf("two matches in two stores:\n%s", out)
	}

	if ids, stores := entryStores(t, mustRunCLI(t, s.primary, "note", "list", "--format", "json")); len(ids) != 2 || ids[0] != theirs || stores[mine] != s.primaryName {
		t.Errorf("note list = %v %v, want both, by title", ids, stores)
	}
	if ids, stores := entryStores(t, mustRunCLI(t, s.primary, "note", "search", "runbook", "--format", "json")); len(ids) != 2 || stores[theirs] != s.otherName {
		t.Errorf("note search = %v %v", ids, stores)
	}
	if out := mustRunCLI(t, s.primary, "note", "search", "runbook"); !strings.Contains(out, "["+s.otherName+"]") {
		t.Errorf("note search text does not name the store:\n%s", out)
	}
	var cats []storage.CategoryInfo
	if err := json.Unmarshal([]byte(mustRunCLI(t, s.primary, "note", "categories", "--format", "json")), &cats); err != nil || len(cats) != 1 || cats[0].Count != 2 {
		t.Errorf("note categories = %+v (%v), want ops with 2 notes", cats, err)
	}
}

// The same id in two stores (a copy, or a move cut short): the primary's entry is shown, with a
// warning that names the other store.
func TestCLI_ReadStores_SameIDInTwoStores(t *testing.T) {
	s := newTwoStores(t)
	id := addNoteCLI(t, s.primary, "ops", "Copied note", "copykw", "team")
	files, _ := filepath.Glob(filepath.Join(s.primary, "notes", "ops", "*.md"))
	if len(files) != 1 {
		t.Fatalf("note files: %v", files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.other, "notes", "ops"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.other, "notes", "ops", filepath.Base(files[0])), data, 0600); err != nil {
		t.Fatal(err)
	}
	mustRunCLI(t, s.other, "reindex")

	// a child process, to read stderr; its home holds neither store, so it names them by full path
	out, err := runCLIChild(t, t.TempDir(), "--store", s.primary, "note", "get", id, "--format", "json")
	if err != nil {
		t.Fatalf("note get: %v\n%s", err, out)
	}
	if !strings.Contains(out, "warning: "+id+" also matches a note in "+s.other+"; showing the one in "+s.primary) {
		t.Errorf("no warning about the second copy:\n%s", out)
	}
	if !strings.Contains(out, `"store": "`+s.primary+`"`) {
		t.Errorf("the note shown is not the primary's:\n%s", out)
	}
}

func TestCLI_ReadStores_Sessions(t *testing.T) {
	s := newTwoStores(t)
	mine := saveSessionCLI(t, s.primary, "Primary session", "sid-p", "sesskw primary", "team")
	theirs := saveSessionCLI(t, s.other, "Other session", "sid-o", "sesskw other", "team")

	ids, stores := entryStores(t, mustRunCLI(t, s.primary, "session", "list", "--format", "json"))
	if len(ids) != 2 || stores[mine] != s.primaryName || stores[theirs] != s.otherName {
		t.Errorf("session list = %v %v", ids, stores)
	}
	if ids, _ := entryStores(t, mustRunCLI(t, s.primary, "session", "list", "--last", "1", "--format", "json")); len(ids) != 1 {
		t.Errorf("session list --last 1 over two stores returned %d sessions", len(ids))
	}
	if out := mustRunCLI(t, s.primary, "session", "list"); !strings.Contains(out, s.otherName) {
		t.Errorf("session list text does not name the store:\n%s", out)
	}
	ids, stores = entryStores(t, mustRunCLI(t, s.primary, "session", "search", "sesskw", "--format", "json"))
	if len(ids) != 2 || stores[theirs] != s.otherName {
		t.Errorf("session search = %v %v", ids, stores)
	}
	got := map[string]interface{}{}
	if err := json.Unmarshal([]byte(mustRunCLI(t, s.primary, "session", "get", theirs, "--format", "json")), &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != theirs || got["store"] != s.otherName {
		t.Errorf("session get: id %v store %v", got["id"], got["store"])
	}
	if out := mustRunCLI(t, s.primary, "session", "get", theirs); !strings.Contains(out, "Store:      "+s.otherName) {
		t.Errorf("session get text does not name the store:\n%s", out)
	}
}

func TestCLI_ReadStores_ContextInjectAndStats(t *testing.T) {
	s := newTwoStores(t)
	addNoteCLI(t, s.primary, "ops", "Primary note", "ctxkw", "team")
	theirs := addNoteCLI(t, s.other, "ops", "Other note", "ctxkw", "team,ops")
	saveSessionCLI(t, s.primary, "Primary session", "sid-p", "ctxkw", "team")
	saveSessionCLI(t, s.other, "Other session", "sid-o", "ctxkw", "team")

	var payload contextPayload
	if err := json.Unmarshal([]byte(mustRunCLI(t, s.primary, "context", "inject", "--format", "json")), &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Join(payload.Stores, ",") != s.primaryName+","+s.otherName {
		t.Errorf("stores = %v", payload.Stores)
	}
	if payload.Stats.TotalNotes != 2 || payload.Stats.TotalSessions != 2 || len(payload.RecentNotes) != 2 || len(payload.RecentSessions) != 2 {
		t.Errorf("context = %+v", payload)
	}
	for _, n := range payload.RecentNotes {
		if n.ID == theirs && n.Store != s.otherName {
			t.Errorf("the other store's note names %q", n.Store)
		}
	}
	payload = contextPayload{}
	if err := json.Unmarshal([]byte(mustRunCLI(t, s.primary, "context", "inject", "--tag", "ops", "--format", "json")), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Stats.TotalNotes != 1 || payload.Stats.TotalSessions != 0 || len(payload.RecentNotes) != 1 || payload.RecentNotes[0].ID != theirs {
		t.Errorf("context --tag ops = %+v", payload)
	}
	if out := mustRunCLI(t, s.primary, "context", "inject", "--limit", "1"); !strings.Contains(out, "in 2 stores") || strings.Count(out, "  - [") != 2 {
		t.Errorf("context inject --limit 1 text:\n%s", out)
	}

	var stats struct {
		storage.StoreStats
		Stores []storeTotals `json:"stores"`
	}
	if err := json.Unmarshal([]byte(mustRunCLI(t, s.primary, "stats", "--format", "json")), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.TotalNotes != 2 || stats.TotalSessions != 2 || len(stats.Stores) != 2 || stats.Stores[1].Store != s.otherName || stats.Stores[1].TotalNotes != 1 {
		t.Errorf("stats = %+v", stats)
	}
	if len(stats.TopTags) == 0 || stats.TopTags[0] != (storage.TagInfo{Name: "team", Count: 4}) {
		t.Errorf("top tags = %+v, want team 4 first", stats.TopTags)
	}
	if out := mustRunCLI(t, s.primary, "stats"); !strings.Contains(out, "Stores:") || !strings.Contains(out, s.otherName+": 1 notes, 1 sessions") {
		t.Errorf("stats text:\n%s", out)
	}

	var tagged struct {
		storage.TaggedStats
		Stores []storeTotals `json:"stores"`
	}
	if err := json.Unmarshal([]byte(mustRunCLI(t, s.primary, "stats", "--tag", "team", "--format", "json")), &tagged); err != nil {
		t.Fatal(err)
	}
	if tagged.TotalNotes != 2 || tagged.TotalSessions != 2 || len(tagged.Stores) != 2 || tagged.Stores[0].StorageSize != 0 {
		t.Errorf("stats --tag team = %+v", tagged)
	}

	// reading the other store through the primary writes nothing there
	mustRunCLI(t, s.primary, "note", "get", theirs)
	mustRunCLI(t, s.primary, "search", "ctxkw")
	var accessed struct {
		TopAccessed []storage.AccessStat `json:"top_accessed"`
	}
	if err := json.Unmarshal([]byte(mustRunCLI(t, s.other, "stats", "--top-accessed", "--format", "json")), &accessed); err != nil {
		t.Fatal(err)
	}
	if len(accessed.TopAccessed) != 0 {
		t.Errorf("reads through the primary logged accesses in the other store: %+v", accessed.TopAccessed)
	}
	// the accesses the other store logged itself are listed with its name
	mustRunCLI(t, s.other, "note", "get", theirs)
	if err := json.Unmarshal([]byte(mustRunCLI(t, s.primary, "stats", "--top-accessed", "--format", "json")), &accessed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range accessed.TopAccessed {
		if a.ID == theirs {
			found = a.Store == s.otherName && a.AccessCount == 1
		}
	}
	if !found {
		t.Errorf("top accessed = %+v, want the other store's note once, named with its store", accessed.TopAccessed)
	}
}

func TestCLI_ReadStores_Settings(t *testing.T) {
	primary, other, third := t.TempDir(), t.TempDir(), t.TempDir()
	addNoteCLI(t, other, "c", "Other", "setkw", "x")
	addNoteCLI(t, third, "c", "Third", "setkw", "x")

	// a store named twice, through a symlink, or the primary itself is read once
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	mustRunCLI(t, primary, "config", "set", readStoresKey, other+", "+link+","+primary)
	if ids, _ := entryStores(t, mustRunCLI(t, primary, "search", "setkw", "--format", "json")); len(ids) != 1 {
		t.Errorf("search read %d entries, want the other store's one, once", len(ids))
	}

	// --read-stores replaces the setting
	_, stores := entryStores(t, mustRunCLI(t, primary, "--read-stores", third, "search", "setkw", "--format", "json"))
	if len(stores) != 1 {
		t.Errorf("--read-stores read %v, want only the third store", stores)
	}
	for _, store := range stores {
		if store != displayStoreName(third) {
			t.Errorf("the entry names %q, want the third store", store)
		}
	}

	// a relative path is refused
	mustRunCLI(t, primary, "config", "set", readStoresKey, "relative/dir")
	if _, err := runCLI(t, primary, "search", "setkw"); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Errorf("a relative store path: %v", err)
	}

	// a store that does not exist is skipped with a warning, and stays uncreated (a child process,
	// to read stderr; its home holds no store, so it names them by full path)
	gone := filepath.Join(t.TempDir(), "gone")
	mustRunCLI(t, primary, "config", "set", readStoresKey, gone+","+other)
	out, err := runCLIChild(t, t.TempDir(), "--store", primary, "search", "setkw", "--format", "json")
	if err != nil || !strings.Contains(out, "warning: store "+gone+" ("+readStoresKey+") not found") || !strings.Contains(out, `"store"`) {
		t.Errorf("a missing store: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(gone); !os.IsNotExist(statErr) {
		t.Errorf("reading created the missing store: %v", statErr)
	}

	// a directory that is not a store is skipped with a warning, and left as it is
	plain := t.TempDir()
	mustRunCLI(t, primary, "config", "set", readStoresKey, plain+","+other)
	out, err = runCLIChild(t, t.TempDir(), "--store", primary, "search", "setkw", "--format", "json")
	if err != nil || !strings.Contains(out, "warning: "+plain+" ("+readStoresKey+") is not a claudemem store") || !strings.Contains(out, `"store"`) {
		t.Errorf("a directory that is not a store: %v\n%s", err, out)
	}
	if entries, readErr := os.ReadDir(plain); readErr != nil || len(entries) != 0 {
		t.Errorf("reading wrote into a directory that is not a store: %v %v", entries, readErr)
	}

	// ~/ is the home directory, and names show the home as ~
	home := t.TempDir()
	if out, err := runCLIChild(t, home, "--store", filepath.Join(home, "shared"), "note", "add", "c", "--title", "Home note", "--content", "setkw"); err != nil {
		t.Fatalf("note add: %v\n%s", err, out)
	}
	mustRunCLI(t, primary, "config", "set", readStoresKey, "~/shared")
	out, err = runCLIChild(t, home, "--store", primary, "search", "setkw", "--format", "json")
	if err != nil || !strings.Contains(out, `"store": "~/shared"`) {
		t.Errorf("a ~/ store: %v\n%s", err, out)
	}
}
