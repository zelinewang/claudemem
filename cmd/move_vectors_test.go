package cmd

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ─── A move carries no vectors ─────────────────────────────────────────────────
//
// A store's vectors can be older than its file: a reindex, a sync pull or a tag edit rebuilds the
// search row and keeps the vectors. Carried into another store, they let a semantic search there find
// words the file no longer has.

const removedWords = "salary negotiation bonus"

// countIndexRows runs a COUNT query on a store's index, opened read-only.
func countIndexRows(t *testing.T, dir, query string, args ...interface{}) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, ".index", "search.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// moveOne moves one entry with the CLI and returns what it printed.
func moveOne(t *testing.T, from, to, id string) string {
	t.Helper()
	ids := filepath.Join(t.TempDir(), "ids.txt")
	if err := os.WriteFile(ids, []byte(id+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return mustRunCLI(t, from, "move", "--to", to, "--file", ids)
}

// checkMovedWithoutVectors checks that the target has no vectors for the moved entry, that the move
// printed the command that embeds it there, and that a semantic search there for the words the file
// no longer has does not find it.
func checkMovedWithoutVectors(t *testing.T, to, id, moveOutput string) {
	t.Helper()
	if n := countIndexRows(t, to, `SELECT COUNT(*) FROM vectors WHERE doc_id = ?`, id); n != 0 {
		t.Errorf("the target has %d vector rows for the moved entry, want 0", n)
	}
	if want := "embed them with: claudemem --store " + to + " reindex --vectors --missing"; !strings.Contains(moveOutput, want) {
		t.Errorf("the move did not print %q:\n%s", want, moveOutput)
	}
	out := mustRunCLI(t, to, "--read-stores", "", "search", removedWords, "--semantic", "--format", "json")
	var results []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("parse %s: %v", out, err)
	}
	for _, r := range results {
		if r.ID == id {
			t.Errorf("a semantic search in the target for words the file no longer has found it: %s", out)
		}
	}
}

// A hand edit, then a reindex: the search row follows the file, the vectors keep the old text.
func TestCLI_MoveCarriesNoVectors_AfterAnEditAndAReindex(t *testing.T) {
	embed := newFakeEmbeddings(t, removedWords, "kubernetes canary")
	t.Setenv("CM_TEST_EMBED_KEY", "k")
	from, to := t.TempDir(), t.TempDir()
	for _, dir := range []string{from, to} {
		writeStoreConfig(t, dir, openAIConfig(embed.URL, "CM_TEST_EMBED_KEY"))
	}
	id := addNoteCLI(t, from, "work", "Steps", "Public steps for the deploy. PRIVATEDETAIL "+removedWords+" 123", "team")
	addNoteCLI(t, to, "ops", "Unrelated", "kubernetes canary rollout", "team")

	files, _ := filepath.Glob(filepath.Join(from, "notes", "work", "*.md"))
	if len(files) != 1 {
		t.Fatalf("note files: %v", files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files[0], []byte(strings.Replace(string(data), " PRIVATEDETAIL "+removedWords+" 123", "", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	mustRunCLI(t, from, "reindex")
	if n := countIndexRows(t, from, `SELECT COUNT(*) FROM vectors WHERE doc_id = ?`, id); n != 1 {
		t.Fatalf("this store has %d vector rows for the note, want its 1 old one", n)
	}

	checkMovedWithoutVectors(t, to, id, moveOne(t, from, to, id))
}

// A note edited on one machine and pulled on the other keeps the other machine's old vectors (a pull
// embeds only the entries that have none); a move there would take them along.
func TestCLI_MoveCarriesNoVectors_AfterASyncPull(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "test", "GIT_AUTHOR_EMAIL": "test@example.com",
		"GIT_COMMITTER_NAME": "test", "GIT_COMMITTER_EMAIL": "test@example.com",
		"CM_TEST_EMBED_KEY": "k",
	} {
		t.Setenv(k, v)
	}
	embed := newFakeEmbeddings(t, removedWords, "kubernetes canary")
	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	first, second, team := t.TempDir(), t.TempDir(), t.TempDir()
	for _, dir := range []string{second, team} {
		writeStoreConfig(t, dir, openAIConfig(embed.URL, "CM_TEST_EMBED_KEY"))
	}
	addNoteCLI(t, team, "ops", "Unrelated", "kubernetes canary rollout", "team")

	id := addNoteCLI(t, first, "work", "Deploy notes", "Deploy steps for the api. PRIVATEDETAIL "+removedWords+" 123", "team")
	mustRunCLI(t, first, "sync", "init", remote)
	mustRunCLI(t, first, "sync", "push")
	mustRunCLI(t, second, "sync", "init", remote)
	mustRunCLI(t, second, "sync", "pull")
	if n := countIndexRows(t, second, `SELECT COUNT(*) FROM vectors WHERE doc_id = ?`, id); n != 1 {
		t.Fatalf("the second machine has %d vector rows for the note after the first pull, want 1", n)
	}

	mustRunCLI(t, first, "note", "update", id, "--content", "Deploy steps for the api.")
	mustRunCLI(t, first, "sync", "push")
	mustRunCLI(t, second, "sync", "pull")
	if n := countIndexRows(t, second, `SELECT COUNT(*) FROM memory_fts WHERE id = ? AND content LIKE '%salary%'`, id); n != 0 {
		t.Fatalf("the pull did not bring the edit (%d search rows with the old words)", n)
	}

	checkMovedWithoutVectors(t, team, id, moveOne(t, second, team, id))
}
