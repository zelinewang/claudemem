package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
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

// A symlink already in the store at a file's place is never written through, whether it dangles to a
// path outside the store or points at another file of it; and a file there is replaced, not written
// into, so a hard link to a file outside the store keeps that file as it was.
func TestExtractArchive_NeverWritesThroughALinkAtTheFile(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	store := filepath.Join(root, "memory")
	ops := filepath.Join(store, "notes", "ops")
	if err := os.MkdirAll(ops, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "not-there-yet.md"), filepath.Join(ops, "dangling.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ops, "other.md"), []byte("the other note"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.md", filepath.Join(ops, "inside.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "shared.md"), []byte("outside, as it was"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(outside, "shared.md"), filepath.Join(ops, "hardlinked.md")); err != nil {
		t.Fatal(err)
	}
	n, err := extractArchive(archiveReader(t, [][2]string{
		{"claudemem/notes/ops/dangling.md", "written through a dangling link"},
		{"claudemem/notes/ops/inside.md", "written through a link inside"},
		{"claudemem/notes/ops/hardlinked.md", "the imported version"},
	}), store)
	if err != nil || n != 1 {
		t.Errorf("extractArchive = %d, %v; want the 1 file that is no symlink", n, err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "not-there-yet.md")); err == nil {
		t.Error("the import created a file outside the store through a dangling symlink")
	}
	if data, _ := os.ReadFile(filepath.Join(ops, "other.md")); string(data) != "the other note" {
		t.Errorf("the import wrote through a symlink into another file of the store: %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(outside, "shared.md")); string(data) != "outside, as it was" {
		t.Errorf("the import wrote into a file outside the store through a hard link: %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(ops, "hardlinked.md")); string(data) != "the imported version" {
		t.Errorf("the file in the store was not replaced: %q", data)
	}
}

// A file in the store that cannot be written is not replaced, and the import fails on it.
func TestExtractArchive_LeavesAReadOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a file its permissions keep from being written")
	}
	store := t.TempDir()
	path := filepath.Join(store, "notes", "ops", "ro.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old"), 0400); err != nil {
		t.Fatal(err)
	}
	if _, err := extractArchive(archiveReader(t, [][2]string{{"claudemem/notes/ops/ro.md", "new"}}), store); err == nil {
		t.Error("the import of a read-only file succeeded")
	}
	if data, _ := os.ReadFile(path); string(data) != "old" {
		t.Errorf("the read-only file was replaced: %q", data)
	}
}
