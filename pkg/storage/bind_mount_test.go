//go:build linux

package storage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bindMountEnv tells a test that it runs again in a child process inside a bubblewrap sandbox, where
// a bind mount makes one directory reachable by two paths that only the device and inode numbers tie.
const bindMountEnv = "CLAUDEMEM_TEST_UNDER_BIND_MOUNT"

// runUnderBindMount runs the calling top-level test again in a child process inside a bubblewrap
// sandbox that binds src at dst (the rest of the file system stays as it is), with env added. The
// child prints marker when its checks ran. It skips when bwrap is missing or cannot mount here.
func runUnderBindMount(t *testing.T, src, dst, marker string, env ...string) {
	t.Helper()
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not installed: no bind mount without root")
	}
	args := []string{"--dev-bind", "/", "/", "--bind", src, dst}
	if out, err := exec.Command(bwrap, append(args, "true")...).CombinedOutput(); err != nil {
		t.Skipf("bwrap cannot make a bind mount here: %v %s", err, out)
	}
	cmd := exec.Command(bwrap, append(args, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")...)
	cmd.Env = append(append(os.Environ(), bindMountEnv+"=1"), env...)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), marker) {
		t.Fatalf("the checks under a bind mount failed (%v):\n%s", err, out)
	}
}

// Two stores that a bind mount ties together overlap: the same directory under two paths, or a store
// inside a bind mount of the other. A file reached through the mount is the same file.
func TestStoresOverlap_ThroughABindMount(t *testing.T) {
	const marker = "bind-mount checks ran"
	if os.Getenv(bindMountEnv) == "1" {
		store, alias := os.Getenv("CM_TEST_STORE"), os.Getenv("CM_TEST_ALIAS")
		for _, c := range []struct{ a, b string }{
			{store, alias},
			{store, filepath.Join(alias, "inner")},
			{filepath.Join(alias, "inner"), store},
			{filepath.Join(store, "inner"), alias},
		} {
			if !StoresOverlap(c.a, c.b) {
				t.Errorf("StoresOverlap(%s, %s) = false under the bind mount", c.a, c.b)
			}
		}
		file := filepath.Join("notes", "ops", "runbook.md")
		if !sameResolvedFile(filepath.Join(store, file), filepath.Join(alias, file)) {
			t.Error("a file and the same file through the bind mount are not taken as one")
		}
		fmt.Println(marker)
		return
	}
	root := t.TempDir()
	store, alias := filepath.Join(root, "store"), filepath.Join(root, "alias")
	for _, dir := range []string{filepath.Join(store, "inner"), filepath.Join(store, "notes", "ops"), alias} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(store, "notes", "ops", "runbook.md"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if StoresOverlap(store, filepath.Join(alias)) {
		t.Fatal("two separate directories overlap before the mount")
	}
	runUnderBindMount(t, store, alias, marker, "CM_TEST_STORE="+store, "CM_TEST_ALIAS="+alias)
}

// A target store that is a bind mount of a directory inside this store sits inside it, though neither
// path shows it: the move is refused, in both directions, as for a store nested by its path.
func TestMoveEntries_RefusesAStoreBoundToADirectoryOfTheOther(t *testing.T) {
	const marker = "nested bind-mount checks ran"
	if os.Getenv(bindMountEnv) == "1" {
		from, err := NewFileStore(os.Getenv("CM_TEST_STORE"))
		if err != nil {
			t.Fatal(err)
		}
		defer from.Close()
		to, err := NewFileStore(os.Getenv("CM_TEST_ALIAS"))
		if err != nil {
			t.Fatal(err)
		}
		defer to.Close()
		for _, pair := range [][2]*FileStore{{from, to}, {to, from}} {
			if _, err := MoveEntries(pair[0], pair[1], moveIDs(os.Getenv("CM_TEST_NOTE")), false, nil); err == nil || !strings.Contains(err.Error(), "overlap") {
				t.Errorf("a move from %s to %s: %v, want the stores refused as overlapping", pair[0].baseDir, pair[1].baseDir, err)
			}
		}
		if _, err := from.GetNote(os.Getenv("CM_TEST_NOTE")); err != nil {
			t.Errorf("the note left this store: %v", err)
		}
		fmt.Println(marker)
		return
	}
	root := t.TempDir()
	store, alias := filepath.Join(root, "store"), filepath.Join(root, "team")
	from, err := NewFileStore(store)
	if err != nil {
		t.Fatal(err)
	}
	note := addTaggedNote(t, from, "ops", "Deploy runbook", "body", "team")
	from.Close()
	inner, err := NewFileStore(filepath.Join(store, "inner"))
	if err != nil {
		t.Fatal(err)
	}
	inner.Close()
	if err := os.MkdirAll(alias, 0700); err != nil {
		t.Fatal(err)
	}
	runUnderBindMount(t, filepath.Join(store, "inner"), alias, marker, "CM_TEST_STORE="+store, "CM_TEST_ALIAS="+alias, "CM_TEST_NOTE="+note.ID)
}
