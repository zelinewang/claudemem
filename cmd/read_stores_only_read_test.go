package cmd

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// ─── A store read through another store is only read ──────────────────────────

// fakeEmbeddings is an OpenAI-style embeddings endpoint on loopback that records every request. A
// text containing a word of the i-th group gets direction i; any other text gets the last direction.
type fakeEmbeddings struct {
	*httptest.Server
	mu       sync.Mutex
	requests []fakeEmbeddingRequest
}

type fakeEmbeddingRequest struct {
	Method, Path, Auth string
	Inputs             []string
}

func newFakeEmbeddings(t *testing.T, groups ...string) *fakeEmbeddings {
	t.Helper()
	f := &fakeEmbeddings{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input      []string `json:"input"`
			Dimensions int      `json:"dimensions"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.requests = append(f.requests, fakeEmbeddingRequest{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Inputs: body.Input})
		f.mu.Unlock()
		if r.Method != http.MethodPost {
			w.Write([]byte(`{}`))
			return
		}
		dims := body.Dimensions
		if dims <= 0 {
			dims = 4
		}
		type item struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		}
		var resp struct {
			Data []item `json:"data"`
		}
		for i, text := range body.Input {
			vec := make([]float32, dims)
			direction := dims - 1
			for g, group := range groups {
				for _, word := range strings.Fields(group) {
					if strings.Contains(strings.ToLower(text), word) && direction == dims-1 {
						direction = g % dims
					}
				}
			}
			vec[direction] = 1
			resp.Data = append(resp.Data, item{Embedding: vec, Index: i})
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeEmbeddings) taken() []fakeEmbeddingRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.requests
	f.requests = nil
	return out
}

// writeStoreConfig replaces a store's config.json with these settings.
func writeStoreConfig(t *testing.T, dir string, settings map[string]string) {
	t.Helper()
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// openAIConfig is a store's semantic-search settings for the OpenAI backend at endpoint.
func openAIConfig(endpoint, keyEnv string) map[string]string {
	return map[string]string{
		"features.semantic_search": "true",
		"embedding.backend":        "openai",
		"embedding.endpoint":       endpoint,
		"embedding.api_key_env":    keyEnv,
		"embedding.dimensions":     "4",
	}
}

// runCLIWithStderr is runCLI that also returns what the command wrote on stderr.
func runCLIWithStderr(t *testing.T, storePath string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	orig := os.Stderr
	r, w, perr := os.Pipe()
	if perr != nil {
		t.Fatal(perr)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	stdout, err = runCLI(t, storePath, args...)
	w.Close()
	os.Stderr = orig
	stderr = <-done
	r.Close()
	return stdout, stderr, err
}

// storeFiles hashes every file of a store except SQLite's -wal and -shm side files, which any reader
// of a WAL database may create.
func storeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || strings.HasSuffix(path, "-wal") || strings.HasSuffix(path, "-shm") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		rel, _ := filepath.Rel(dir, path)
		files[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// indexTables lists the tables of a store's index, opened read-only.
func indexTables(t *testing.T, dir string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, ".index", "search.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
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
	return names
}

// A store read through another one never chooses where a query or a credential goes: its embedding
// settings are not read, so the reader's query, environment keys and tokens never reach the
// endpoint it names.
func TestCLI_ReadStores_NeverUseTheirEmbeddingSettings(t *testing.T) {
	s := newTwoStores(t)
	mine := addNoteCLI(t, s.primary, "ops", "Primary runbook", "leakkw my private query words", "team")
	theirs := addNoteCLI(t, s.other, "ops", "Team runbook", "leakkw team text", "team")
	sid := saveSessionCLI(t, s.other, "Team session", "sid-leak", "leakkw summary", "team")
	elsewhere := newFakeEmbeddings(t)
	t.Setenv("CM_TEST_READER_SECRET", "secret-of-the-reader")
	writeStoreConfig(t, s.other, openAIConfig(elsewhere.URL, "CM_TEST_READER_SECRET"))

	for _, args := range [][]string{
		{"search", "leakkw", "--format", "json"},
		{"search", "my private query words", "--semantic", "--format", "json"},
		{"note", "search", "leakkw", "--format", "json"},
		{"note", "get", theirs},
		{"session", "get", sid},
		{"session", "search", "leakkw", "--format", "json"},
		{"context", "inject", "--format", "json"},
		{"stats", "--format", "json"},
	} {
		mustRunCLI(t, s.primary, args...)
	}
	if reqs := elsewhere.taken(); len(reqs) != 0 {
		t.Fatalf("reading the other store sent %d requests to the endpoint its config names, the first %+v", len(reqs), reqs[0])
	}
	ids, _ := entryStores(t, mustRunCLI(t, s.primary, "search", "leakkw", "--format", "json"))
	sort.Strings(ids)
	want := []string{mine, theirs, sid}
	sort.Strings(want)
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("search found %v, want both stores' entries by keyword %v", ids, want)
	}
}

// With an embedder of its own, the primary also searches the vectors of the stores it reads, those
// of its backend, model and dimensions, with the query embedded once.
func TestCLI_ReadStores_SearchTheirVectorsWithThePrimarysEmbedder(t *testing.T) {
	embeddings := newFakeEmbeddings(t, "rocket zzspace", "banana")
	elsewhere := newFakeEmbeddings(t, "rocket zzspace", "banana")
	t.Setenv("CM_TEST_EMBED_KEY", "test-key")

	other := t.TempDir()
	writeStoreConfig(t, other, openAIConfig(embeddings.URL, "CM_TEST_EMBED_KEY"))
	theirs := addNoteCLI(t, other, "eng", "Engine tuning", "rocket engine tuning steps", "team")
	addNoteCLI(t, other, "eng", "Bread", "banana bread", "team")
	mustRunCLI(t, other, "reindex", "--vectors")

	primary := t.TempDir()
	settings := openAIConfig(embeddings.URL, "CM_TEST_EMBED_KEY")
	settings[readStoresKey] = other
	writeStoreConfig(t, primary, settings)
	addNoteCLI(t, primary, "misc", "Fruit", "banana smoothie", "misc")
	embeddings.taken()

	// the other store has the same settings: still one embedding request, the primary's
	ids, stores := entryStores(t, mustRunCLI(t, primary, "search", "zzspace", "--format", "json"))
	if len(ids) != 1 || ids[0] != theirs || stores[theirs] != displayStoreName(other) {
		t.Fatalf("search zzspace = %v %v, want the other store's rocket note found by its vector", ids, stores)
	}
	if reqs := embeddings.taken(); len(reqs) != 1 || strings.Join(reqs[0].Inputs, "|") != "zzspace" {
		t.Errorf("embedding requests = %+v, want one: the query, embedded once for both stores", reqs)
	}

	// the other store now names another endpoint: the reader keeps to its own
	writeStoreConfig(t, other, openAIConfig(elsewhere.URL, "CM_TEST_EMBED_KEY"))
	if ids, _ := entryStores(t, mustRunCLI(t, primary, "search", "zzspace", "--format", "json")); len(ids) != 1 || ids[0] != theirs {
		t.Errorf("search zzspace = %v, want the other store's rocket note", ids)
	}
	if reqs := elsewhere.taken(); len(reqs) != 0 {
		t.Errorf("the endpoint of the store read got %d requests, the first %+v", len(reqs), reqs[0])
	}
	if reqs := embeddings.taken(); len(reqs) != 1 {
		t.Errorf("the primary's endpoint got %d requests, want 1", len(reqs))
	}

	// vectors of another model are not compared with the query
	settings["embedding.model"] = "text-embedding-3-large"
	writeStoreConfig(t, primary, settings)
	if ids, _ := entryStores(t, mustRunCLI(t, primary, "search", "zzspace", "--format", "json")); len(ids) != 0 {
		t.Errorf("search zzspace with another model = %v, want nothing", ids)
	}
}

// A store read through another one is never written: no vector tables, no schema stamp, no access
// log, no file, even when its own settings turn semantic search on.
func TestCLI_ReadStores_AreOnlyRead(t *testing.T) {
	s := newTwoStores(t)
	theirs := addNoteCLI(t, s.other, "ops", "Team note", "rokw team text", "team")
	sid := saveSessionCLI(t, s.other, "Team session", "sid-ro", "rokw summary", "team")
	writeStoreConfig(t, s.other, map[string]string{"features.semantic_search": "true"})
	writeStoreConfig(t, s.primary, map[string]string{readStoresKey: s.other, "features.semantic_search": "true"})
	tables := indexTables(t, s.other)
	files := storeFiles(t, s.other)

	for _, args := range [][]string{
		{"search", "rokw", "--format", "json"},
		{"search", "rokw", "--semantic", "--format", "json"},
		{"note", "get", theirs},
		{"note", "list", "--format", "json"},
		{"note", "search", "rokw", "--format", "json"},
		{"session", "get", sid},
		{"session", "list", "--format", "json"},
		{"context", "inject", "--format", "json"},
		{"stats", "--format", "json"},
		{"stats", "--top-accessed", "--format", "json"},
	} {
		mustRunCLI(t, s.primary, args...)
	}

	if got := indexTables(t, s.other); strings.Join(got, ",") != strings.Join(tables, ",") {
		t.Errorf("the tables of the store read changed: %v, were %v", got, tables)
	}
	after := storeFiles(t, s.other)
	for name, sum := range files {
		if after[name] != sum {
			t.Errorf("reading through the primary changed %s", name)
		}
	}
	for name := range after {
		if _, ok := files[name]; !ok {
			t.Errorf("reading through the primary created %s", name)
		}
	}
}

// Problems with the stores read are warnings: the command goes on with the primary and the stores
// that open. --read-stores is typed for one call, so a bad value there stays an error.
func TestCLI_ReadStores_ProblemsAreWarnings(t *testing.T) {
	commands := [][]string{
		{"search", "warnkw", "--format", "json"},
		{"note", "list", "--format", "json"},
		{"context", "inject", "--format", "json"},
		{"stats", "--format", "json"},
	}
	runAll := func(t *testing.T, store, wantWarning string) {
		t.Helper()
		for _, args := range commands {
			out, stderr, err := runCLIWithStderr(t, store, args...)
			if err != nil {
				t.Errorf("%v: %v\n%s", args, err, out)
				continue
			}
			if !strings.Contains(stderr, wantWarning) {
				t.Errorf("%v: stderr %q, want a warning containing %q", args, stderr, wantWarning)
			}
		}
	}

	t.Run("a malformed config", func(t *testing.T) {
		primary := t.TempDir()
		mine := addNoteCLI(t, primary, "ops", "Mine", "warnkw", "x")
		if err := os.WriteFile(filepath.Join(primary, "config.json"), []byte(`{"features.semantic_search": tru`), 0o600); err != nil {
			t.Fatal(err)
		}
		runAll(t, primary, "cannot read "+readStoresKey)
		if ids, _ := entryStores(t, mustRunCLI(t, primary, "search", "warnkw", "--format", "json")); len(ids) != 1 || ids[0] != mine {
			t.Errorf("search = %v, want the primary's note", ids)
		}
	})

	t.Run("a relative path", func(t *testing.T) {
		s := newTwoStores(t)
		theirs := addNoteCLI(t, s.other, "ops", "Theirs", "warnkw", "x")
		mustRunCLI(t, s.primary, "config", "set", readStoresKey, "relative/dir,"+s.other)
		runAll(t, s.primary, "must be absolute")
		if ids, _ := entryStores(t, mustRunCLI(t, s.primary, "search", "warnkw", "--format", "json")); len(ids) != 1 || ids[0] != theirs {
			t.Errorf("search = %v, want the other store's note", ids)
		}
		if _, err := runCLI(t, s.primary, "--read-stores", "relative/dir", "search", "warnkw"); err == nil || !strings.Contains(err.Error(), "must be absolute") {
			t.Errorf("--read-stores with a relative path: %v", err)
		}
	})

	t.Run("a corrupt store", func(t *testing.T) {
		s := newTwoStores(t)
		mine := addNoteCLI(t, s.primary, "ops", "Mine", "warnkw", "x")
		addNoteCLI(t, s.other, "ops", "Theirs", "warnkw", "x")
		index := filepath.Join(s.other, ".index", "search.db")
		for _, side := range []string{index + "-wal", index + "-shm"} {
			if err := os.Remove(side); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(index, []byte(strings.Repeat("not a database ", 512)), 0o600); err != nil {
			t.Fatal(err)
		}
		runAll(t, s.primary, "cannot be opened")
		if ids, _ := entryStores(t, mustRunCLI(t, s.primary, "search", "warnkw", "--format", "json")); len(ids) != 1 || ids[0] != mine {
			t.Errorf("search = %v, want the primary's note", ids)
		}
	})

	t.Run("a store inside the primary or holding it", func(t *testing.T) {
		parent := t.TempDir()
		primary := filepath.Join(parent, "inner")
		mine := addNoteCLI(t, primary, "ops", "Mine", "warnkw", "x")
		nested := filepath.Join(primary, "notes", "team")
		addNoteCLI(t, nested, "ops", "Nested", "warnkw", "x")
		addNoteCLI(t, parent, "ops", "Parent", "warnkw", "x")
		for _, dir := range []string{nested, parent} {
			mustRunCLI(t, primary, "config", "set", readStoresKey, dir)
			runAll(t, primary, "is inside")
			if ids, _ := entryStores(t, mustRunCLI(t, primary, "search", "warnkw", "--format", "json")); len(ids) != 1 || ids[0] != mine {
				t.Errorf("stores.read %s: search = %v, want only the primary's note", dir, ids)
			}
			if _, err := runCLI(t, primary, "--read-stores", dir, "search", "warnkw"); err == nil || !strings.Contains(err.Error(), "is inside") {
				t.Errorf("--read-stores %s: %v", dir, err)
			}
		}
	})
}
