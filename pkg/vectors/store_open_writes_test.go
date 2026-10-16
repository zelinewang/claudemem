package vectors

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
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

// A rebuild's write waits for another process's write instead of failing busy: here a second
// connection commits while the rebuild reads the rows it replaces.
func TestRebuildIndexGated_WaitsForAnotherWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search.db")
	db, other := openFileDB(t, path), openFileDB(t, path)
	emb := &fakeEmbedder{name: "fake", model: "v1", dim: 2}
	vs, err := NewVectorStore(db, emb)
	if err != nil {
		t.Fatal(err)
	}
	insertRow(t, db, "elsewhere", emb.Name(), emb.Model()) // not rebuilt: the rebuild asks the gate about it as it reads

	var otherErr error
	done := make(chan struct{})
	asked := false
	gate := func(write func(present func(string) bool) error) error {
		return write(func(id string) bool {
			if id == "elsewhere" && !asked {
				asked = true
				go func() { // another process logs a search
					defer close(done)
					_, otherErr = other.Exec(`INSERT OR REPLACE INTO vector_meta (key, value) VALUES ('probe', 'x')`)
				}()
				select { // let it commit, if the rebuild lets it
				case <-done:
				case <-time.After(300 * time.Millisecond):
				}
			}
			return true
		})
	}
	if _, err := vs.RebuildIndexGated([]Document{{ID: "doc", Text: "some text"}}, gate); err != nil {
		t.Fatalf("the rebuild failed while another connection wrote: %v", err)
	}
	<-done
	if !asked {
		t.Fatal("the gate was not asked about the row the rebuild reads")
	}
	if otherErr != nil {
		t.Errorf("the other writer failed: %v", otherErr)
	}
	if n := docRows(t, db, "doc"); n != 1 {
		t.Errorf("the rebuilt document has %d rows, want 1", n)
	}
}

// A prune of the inactive backends waits for another process's write instead of failing busy: here
// a second connection commits between the prune's count and its delete.
func TestPruneInactiveBackends_WaitsForAnotherWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search.db")
	db, other := openFileDB(t, path), openFileDB(t, path)
	emb := &fakeEmbedder{name: "fake", model: "v1", dim: 2}
	vs, err := NewVectorStore(db, emb)
	if err != nil {
		t.Fatal(err)
	}
	insertRow(t, db, "stale", "old-backend", "old-model")
	insertRow(t, db, "kept", emb.Name(), emb.Model())

	var otherErr error
	done := make(chan struct{})
	pruneTestHook = func() {
		pruneTestHook = nil
		go func() { // another process logs a search
			defer close(done)
			_, otherErr = other.Exec(`INSERT OR REPLACE INTO vector_meta (key, value) VALUES ('probe', 'x')`)
		}()
		select { // let it commit, if the prune lets it
		case <-done:
		case <-time.After(300 * time.Millisecond):
		}
	}
	t.Cleanup(func() { pruneTestHook = nil })

	deleted, err := vs.PruneInactiveBackends()
	if err != nil {
		t.Fatalf("the prune failed while another connection wrote: %v", err)
	}
	<-done
	if otherErr != nil {
		t.Errorf("the other writer failed: %v", otherErr)
	}
	if deleted["old-backend:old-model"] != 1 || docRows(t, db, "stale") != 0 || docRows(t, db, "kept") != 1 {
		t.Errorf("deleted %v; rows left: stale %d, kept %d", deleted, docRows(t, db, "stale"), docRows(t, db, "kept"))
	}
}
