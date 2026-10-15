package vectors

import (
	"path/filepath"
	"testing"
)

// With a document table, every vector write inserts a document's vectors only while the table lists
// it, checked in the transaction that writes them: a document whose entry left the index gets none.
func TestVectorStore_RequireDocumentRows(t *testing.T) {
	for _, write := range []string{"index", "upsert", "rebuild"} {
		t.Run(write, func(t *testing.T) {
			db := openFileDB(t, filepath.Join(t.TempDir(), "search.db"))
			if _, err := db.Exec(`CREATE TABLE entries (id TEXT PRIMARY KEY)`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO entries (id) VALUES ('listed')`); err != nil {
				t.Fatal(err)
			}
			vs, err := NewVectorStore(db, &fakeEmbedder{name: "fake-cloud", model: "m1", dim: 4})
			if err != nil {
				t.Fatal(err)
			}
			if err := vs.RequireDocumentRows("entries"); err != nil {
				t.Fatal(err)
			}
			docs := []Document{{ID: "listed", Text: "alpha text"}, {ID: "gone", Text: "beta text"}}
			left := 0
			switch write {
			case "index":
				for _, d := range docs {
					if err := vs.IndexDocument(d.ID, d.Text); err != nil {
						t.Fatal(err)
					}
				}
			case "upsert":
				var indexed int
				indexed, left, err = vs.UpsertDocumentsGated(docs, nil)
				if err != nil || indexed != 1 {
					t.Fatalf("upsert: %d indexed, %v", indexed, err)
				}
			case "rebuild":
				if left, err = vs.RebuildIndexGated(docs, nil); err != nil {
					t.Fatal(err)
				}
			}
			for id, want := range map[string]int{"listed": 1, "gone": 0} {
				var n int
				if err := db.QueryRow(`SELECT COUNT(*) FROM vectors WHERE doc_id = ?`, id).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n != want {
					t.Errorf("%s has %d vector rows, want %d", id, n, want)
				}
			}
			if write != "index" && left != 1 {
				t.Errorf("%d documents left out, want 1", left)
			}
		})
	}
	vs, err := NewVectorStore(openFileDB(t, filepath.Join(t.TempDir(), "search.db")), &fakeEmbedder{name: "fake-cloud", model: "m1", dim: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := vs.RequireDocumentRows("entries; DROP TABLE vectors"); err == nil {
		t.Error("a table name that is no table name was taken")
	}
}
