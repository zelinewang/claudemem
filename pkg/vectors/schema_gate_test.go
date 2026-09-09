package vectors

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// The 2026-09-03 incident: a master build (vectors schema v22) was installed
// over a store that a v23 pre-release build had already migrated. The old
// binary kept running against the newer table, its INSERTs failed on the
// column it did not know, and no vectors were written for five days. From
// now on every store carries vector_meta.vectors_schema, and a binary that
// knows an older schema refuses to touch a newer index.

func v23TableOnly(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		CREATE TABLE vector_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE vectors (
			doc_id     TEXT    NOT NULL,
			chunk      INTEGER NOT NULL,
			backend    TEXT    NOT NULL,
			model      TEXT    NOT NULL,
			dim        INTEGER NOT NULL,
			vector     BLOB    NOT NULL,
			created_at TEXT    NOT NULL,
			PRIMARY KEY (doc_id, chunk, backend, model)
		);`); err != nil {
		t.Fatalf("setup v23: %v", err)
	}
}

func TestNewVectorStore_RefusesIndexFromNewerSchema(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	v23TableOnly(t, db)
	if _, err := db.Exec(`INSERT INTO vector_meta (key, value) VALUES ('vectors_schema', '24')`); err != nil {
		t.Fatal(err)
	}

	_, err := NewVectorStore(db, NewTFIDFEmbedder())
	if err == nil {
		t.Fatal("opening an index stamped with a newer schema must fail")
	}
	var newer *ErrIndexNewerThanBinary
	if !errors.As(err, &newer) {
		t.Fatalf("want *ErrIndexNewerThanBinary, got %T: %v", err, err)
	}
	if newer.IndexSchema != 24 || newer.BinarySchema != currentVectorsSchema {
		t.Fatalf("want index=24 binary=%d, got %+v", currentVectorsSchema, newer)
	}
	if !strings.Contains(err.Error(), "24") || !strings.Contains(err.Error(), "upgrade") {
		t.Fatalf("message should name the newer schema and tell the user to upgrade: %q", err.Error())
	}
}

func TestNewVectorStore_StampsSchemaOnFreshMigratedAndUnstampedStores(t *testing.T) {
	// fresh store
	fresh := setupTestDB(t)
	defer fresh.Close()
	if _, err := NewVectorStore(fresh, NewTFIDFEmbedder()); err != nil {
		t.Fatalf("fresh: %v", err)
	}
	if got := readMetaOrEmpty(fresh, "vectors_schema"); got != "23" {
		t.Fatalf("fresh store should be stamped 23, got %q", got)
	}

	// v22 store migrated on open
	old := setupTestDB(t)
	defer old.Close()
	if _, err := old.Exec(`CREATE TABLE vector_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(v22Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := NewVectorStore(old, NewTFIDFEmbedder()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := readMetaOrEmpty(old, "vectors_schema"); got != "23" {
		t.Fatalf("migrated store should be stamped 23, got %q", got)
	}

	// v23 store written by a build that predates the stamp (the hub today)
	unstamped := setupTestDB(t)
	defer unstamped.Close()
	v23TableOnly(t, unstamped)
	if _, err := NewVectorStore(unstamped, NewTFIDFEmbedder()); err != nil {
		t.Fatalf("unstamped v23: %v", err)
	}
	if got := readMetaOrEmpty(unstamped, "vectors_schema"); got != "23" {
		t.Fatalf("unstamped v23 store should be stamped 23 on open, got %q", got)
	}
}
