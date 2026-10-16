package vectors

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// tableNames lists the tables of db.
func tableNames(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return strings.Join(names, ",")
}

// OpenForSearch serves a database that another store's embedder searches: it creates, migrates and
// stamps nothing, whatever it finds.
func TestOpenForSearch_ChangesNothing(t *testing.T) {
	emb := &fakeEmbedder{name: "fake-cloud", model: "m1", dim: 4}

	empty := setupTestDB(t)
	defer empty.Close()
	if _, err := OpenForSearch(empty, emb); err == nil {
		t.Error("an index without vectors was accepted")
	}
	if got := tableNames(t, empty); got != "" {
		t.Errorf("opening an index without vectors created %q", got)
	}

	old := setupTestDB(t)
	defer old.Close()
	if _, err := old.Exec(`CREATE TABLE vectors (id TEXT PRIMARY KEY, vector BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenForSearch(old, emb); err == nil {
		t.Error("a v21 vectors table was accepted")
	}
	if got := tableNames(t, old); got != "vectors" {
		t.Errorf("opening a v21 index changed its tables to %q", got)
	}

	newer := setupTestDB(t)
	defer newer.Close()
	v23TableOnly(t, newer)
	if _, err := newer.Exec(`INSERT INTO vector_meta (key, value) VALUES ('vectors_schema', '24')`); err != nil {
		t.Fatal(err)
	}
	var tooNew *ErrIndexNewerThanBinary
	if _, err := OpenForSearch(newer, emb); !errors.As(err, &tooNew) {
		t.Errorf("an index stamped by a newer binary: %v", err)
	}

	if _, err := OpenForSearch(setupTestDB(t), NewTFIDFEmbedder()); err == nil || !strings.Contains(err.Error(), "TF-IDF") {
		t.Errorf("a TF-IDF embedder: %v", err)
	}
}

// A database opened read-only is searched with the embedder of the store that reads it, matching
// only that embedder's backend and model.
func TestOpenForSearch_SearchesAReadOnlyIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search.db")
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	emb := &fakeEmbedder{name: "fake-cloud", model: "m1", dim: 4}
	vs, err := NewVectorStore(writer, emb)
	if err != nil {
		t.Fatal(err)
	}
	if err := vs.RebuildIndex([]Document{{ID: "a", Text: "alpha text"}, {ID: "b", Text: "beta text"}}); err != nil {
		t.Fatal(err)
	}
	writer.Close()

	reader, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	before := tableNames(t, reader)
	ro, err := OpenForSearch(reader, emb)
	if err != nil {
		t.Fatal(err)
	}
	results, err := ro.Search("alpha text", 5)
	if err != nil || len(results) == 0 || results[0].ID != "a" {
		t.Errorf("search = %+v, %v; want a first", results, err)
	}
	if got := tableNames(t, reader); got != before {
		t.Errorf("tables changed from %q to %q", before, got)
	}
	other, err := OpenForSearch(reader, &fakeEmbedder{name: "fake-cloud", model: "m2", dim: 4})
	if err != nil {
		t.Fatal(err)
	}
	if results, err := other.Search("alpha text", 5); err != nil || len(results) != 0 {
		t.Errorf("another model's search = %+v, %v; want nothing", results, err)
	}
}

type countingEmbedder struct {
	fakeEmbedder
	calls int
}

func (c *countingEmbedder) Embed(text string, t InputType) ([]float32, error) {
	c.calls++
	return c.fakeEmbedder.Embed(text, t)
}

func TestCacheQueryEmbeddings_EmbedsEachQueryOnce(t *testing.T) {
	inner := &countingEmbedder{fakeEmbedder: fakeEmbedder{name: "fake-cloud", model: "m1", dim: 4}}
	cached := CacheQueryEmbeddings(inner)
	first, _ := cached.Embed("query", InputTypeQuery)
	again, _ := CacheQueryEmbeddings(cached).Embed("query", InputTypeQuery)
	if inner.calls != 1 || len(first) != 4 || len(again) != 4 || first[0] != again[0] {
		t.Errorf("the same query was embedded %d times", inner.calls)
	}
	cached.Embed("another query", InputTypeQuery)
	cached.Embed("query", InputTypeDocument)
	if inner.calls != 3 {
		t.Errorf("another text or input type: %d embeddings, want 3", inner.calls)
	}
	if cached.Name() != "fake-cloud" || cached.Model() != "m1" || cached.Dimensions() != 4 {
		t.Errorf("the cache changed the embedder's identity: %s %s %d", cached.Name(), cached.Model(), cached.Dimensions())
	}
}
