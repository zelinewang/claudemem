package vectors

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// openFileDB opens an index file the way a store does: one connection, WAL, a busy timeout.
func openFileDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL"} {
		if _, err := db.Exec(pragma); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func dataVersion(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRow(`PRAGMA data_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// Opening the vectors of an index that is already current writes nothing: every command opens them,
// also a search, and a write there would commit under a concurrent writer's feet.
func TestNewVectorStore_OpeningACurrentIndexWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search.db")
	db, watcher := openFileDB(t, path), openFileDB(t, path)
	emb := &fakeEmbedder{name: "fake", model: "v1", dim: 2}
	if _, err := NewVectorStore(db, emb); err != nil { // creates and stamps the vectors table
		t.Fatal(err)
	}
	before := dataVersion(t, watcher)
	if _, err := NewVectorStore(db, emb); err != nil {
		t.Fatal(err)
	}
	if after := dataVersion(t, watcher); after != before {
		t.Errorf("opening a current index committed a write (data_version %d -> %d)", before, after)
	}
}
