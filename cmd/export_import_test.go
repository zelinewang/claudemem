package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// archiveReader returns a tar reader over an export-like archive holding the given files, in order.
func archiveReader(t *testing.T, files [][2]string) *tar.Reader {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: f[0], Mode: 0600, Size: int64(len(f[1])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return tar.NewReader(r)
}

// An import writes only inside the store: not into a sibling directory whose name starts with the
// store's, not above it, and not through a symlink in the store that leads out of it.
func TestExtractArchive_NeverWritesOutsideTheStore(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	store := filepath.Join(root, "memory")
	if err := os.MkdirAll(filepath.Join(store, "notes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(store, "notes", "linked")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	n, err := extractArchive(archiveReader(t, [][2]string{
		{"claudemem/notes/ops/kept.md", "kept"},
		{"claudemem/../memory-x/escaped.md", "escaped into a sibling"},
		{"claudemem/../escaped.md", "escaped above"},
		{"claudemem/notes/linked/through-link.md", "escaped through a link"},
	}), store)
	if err != nil || n != 1 {
		t.Errorf("extractArchive = %d, %v; want the 1 file inside the store", n, err)
	}
	if _, err := os.Stat(filepath.Join(store, "notes", "ops", "kept.md")); err != nil {
		t.Errorf("the file inside the store is missing: %v", err)
	}
	for _, path := range []string{
		filepath.Join(root, "memory-x", "escaped.md"),
		filepath.Join(root, "escaped.md"),
		filepath.Join(outside, "through-link.md"),
	} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("the import wrote %s, outside the store", path)
		}
	}
}

// A file of the archive that cannot be written fails the import, instead of being left out while the
// import reports success.
func TestExtractArchive_FailsOnAFileItCannotWrite(t *testing.T) {
	store := t.TempDir()
	if err := os.MkdirAll(filepath.Join(store, "notes", "ops", "taken.md"), 0700); err != nil { // a directory where the file goes
		t.Fatal(err)
	}
	n, err := extractArchive(archiveReader(t, [][2]string{
		{"claudemem/notes/ops/first.md", "first"},
		{"claudemem/notes/ops/taken.md", "cannot be written"},
	}), store)
	if err == nil || n != 1 {
		t.Errorf("extractArchive = %d, %v; want an error after the 1 file it wrote", n, err)
	}
}
