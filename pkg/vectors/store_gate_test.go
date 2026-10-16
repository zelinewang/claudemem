package vectors

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

// presentGate is a Gate whose store holds the given documents; it counts its calls.
type presentGate struct {
	holds map[string]bool
	calls int
}

func (g *presentGate) gate(write func(present func(string) bool) error) error {
	g.calls++
	return write(func(id string) bool { return g.holds[id] })
}

func docRows(t *testing.T, db *sql.DB, id string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vectors WHERE doc_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func insertRow(t *testing.T, db *sql.DB, id, backend, model string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO vectors (doc_id, chunk, backend, model, dim, vector, created_at) VALUES (?, 0, ?, ?, 2, ?, '')`,
		id, backend, model, vectorToBlob([]float32{1, 0})); err != nil {
		t.Fatal(err)
	}
}

// A rebuild through a gate leaves out the documents the store no longer holds, removes the rows of
// documents it no longer holds, and keeps the rows of documents that came in meanwhile; under
// TF-IDF it clears every row of the backend; without a gate it clears them all, as it always has.
func TestRebuildIndexGated(t *testing.T) {
	for _, c := range []struct {
		name        string
		embedder    func() Embedder
		gated       bool
		keepArrived bool
	}{
		{"dense, gated", func() Embedder { return holeyEmbedder{} }, true, true},
		{"TF-IDF, gated", func() Embedder { return NewTFIDFEmbedder() }, true, false},
		{"dense, no gate", func() Embedder { return holeyEmbedder{} }, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := setupTestDB(t)
			defer db.Close()
			emb := c.embedder()
			vs, err := NewVectorStore(db, emb)
			if err != nil {
				t.Fatal(err)
			}
			insertRow(t, db, "arrived", emb.Name(), emb.Model())    // came in while the others were embedded
			insertRow(t, db, "deleted", emb.Name(), emb.Model())    // the store no longer holds it
			insertRow(t, db, "other-backend", "elsewhere", "other") // another backend's row always stays
			docs := []Document{{ID: "kept", Text: "a kept runbook"}, {ID: "left", Text: "a checklist that left"}}

			var left int
			if c.gated {
				g := &presentGate{holds: map[string]bool{"kept": true, "arrived": true}}
				left, err = vs.RebuildIndexGated(docs, g.gate)
				if g.calls != 1 {
					t.Errorf("gate called %d times, want 1", g.calls)
				}
			} else {
				err = vs.RebuildIndex(docs)
			}
			if err != nil {
				t.Fatal(err)
			}

			want := map[string]int{"kept": 1, "left": 0, "arrived": 0, "deleted": 0, "other-backend": 1}
			if c.keepArrived {
				want["arrived"] = 1
			}
			if !c.gated {
				want["left"] = 1
			}
			for id, n := range want {
				if got := docRows(t, db, id); got != n {
					t.Errorf("%s has %d rows, want %d", id, got, n)
				}
			}
			if wantLeft := map[bool]int{true: 1, false: 0}[c.gated]; left != wantLeft {
				t.Errorf("left = %d, want %d", left, wantLeft)
			}
		})
	}
}

func TestUpsertDocumentsGated_WritesOnlyWhatTheStoreHolds(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	vs, err := NewVectorStore(db, holeyEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	g := &presentGate{holds: map[string]bool{}}
	var docs []Document
	for i := 0; i < 120; i++ { // three batches of up to 50
		id := fmt.Sprintf("doc-%03d", i)
		docs = append(docs, Document{ID: id, Text: "text of " + id})
		if i%3 != 0 {
			g.holds[id] = true
		}
	}
	indexed, left, err := vs.UpsertDocumentsGated(docs, g.gate)
	if err != nil {
		t.Fatal(err)
	}
	if indexed != 80 || left != 40 || g.calls != 3 {
		t.Errorf("indexed %d, left %d, gate calls %d; want 80, 40, 3 (one per batch)", indexed, left, g.calls)
	}
	for _, d := range docs {
		if want := map[bool]int{true: 1, false: 0}[g.holds[d.ID]]; docRows(t, db, d.ID) != want {
			t.Errorf("%s: %d rows, want %d", d.ID, docRows(t, db, d.ID), want)
		}
	}
}

func TestGatedWrites_AGateErrorWritesNothing(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	vs, err := NewVectorStore(db, holeyEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	insertRow(t, db, "existing", "holey", "v1")
	refuse := func(func(func(string) bool) error) error { return errors.New("store is locked") }
	docs := []Document{{ID: "new", Text: "a new runbook"}}
	if _, err := vs.RebuildIndexGated(docs, refuse); err == nil {
		t.Error("RebuildIndexGated: no error from a refusing gate")
	}
	if _, _, err := vs.UpsertDocumentsGated(docs, refuse); err == nil {
		t.Error("UpsertDocumentsGated: no error from a refusing gate")
	}
	if docRows(t, db, "existing") != 1 || docRows(t, db, "new") != 0 {
		t.Errorf("rows changed: existing %d, new %d", docRows(t, db, "existing"), docRows(t, db, "new"))
	}
}
