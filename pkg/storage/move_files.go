package storage

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// The files of a move: where an entry's file goes in the target, how it gets there, and the copies
// of it that either store holds outside its index.

// placeInTarget decides where the entry's file goes in the target: rel, a note for the report, and
// whether the file must be written (false when the target already holds this very file). Every copy
// of this id that the target holds, indexed or not, must be this file; a different one is refused,
// since a later reindex of the target could pick it instead. The place is checked as movePath checks
// it, so a dry run refuses what the move would, and it must not resolve to srcPath, the file this
// store gives up.
func (m *mover) placeInTarget(kind, id, srcRel, srcPath string, dst *indexRow, data []byte) (string, string, bool, error) {
	rel, note, install, err := m.choosePlace(kind, id, srcRel, dst, data)
	if err != nil {
		return "", "", false, err
	}
	targetPath, err := movePath(m.to, m.from, rel)
	if err != nil {
		return "", "", false, fmt.Errorf("the target's file: %v", err)
	}
	if sameResolvedFile(srcPath, targetPath) {
		return "", "", false, fmt.Errorf("the target's file %s is this store's file (through a link); move it by hand", rel)
	}
	return rel, note, install, nil
}

func (m *mover) choosePlace(kind, id, srcRel string, dst *indexRow, data []byte) (rel, note string, install bool, err error) {
	candidates := m.toFiles[id]
	if dst != nil {
		candidates = append([]string{dst.filepath}, candidates...)
	}
	if other := m.differentCopy(m.to, kind, id, data, candidates); other != "" {
		return "", "", false, fmt.Errorf("the target holds a different file for this id at %s; compare them by hand", other)
	}
	if dst != nil {
		path, err := movePath(m.to, m.from, dst.filepath)
		if err != nil {
			return "", "", false, fmt.Errorf("the target's file: %v", err)
		}
		existing, readErr := os.ReadFile(path)
		switch {
		case readErr == nil && bytes.Equal(existing, data):
			return dst.filepath, "finished an earlier move", false, nil
		case readErr == nil:
			// a copy of this id would have been refused above: it holds another entry, or none
			return "", "", false, fmt.Errorf("the target's index row for it names %s, which holds another entry's file; reindex the target, then run the move again", dst.filepath)
		case !os.IsNotExist(readErr):
			return "", "", false, fmt.Errorf("the target's file %s cannot be read: %v", dst.filepath, readErr)
		}
		free, err := m.to.freeEntryPath(kind, dst.filepath, id)
		return free, "finished an earlier move", true, err
	}
	for _, c := range m.to.copiesOf(kind, id, m.toFiles[id]) {
		// the file is in the target without its rows (an earlier run stopped, or lost them); it is
		// used unless another entry's row names that file
		var owner string
		switch err := m.to.db.QueryRow(`SELECT id FROM entries WHERE filepath = ? LIMIT 1`, c.rel).Scan(&owner); {
		case isNoRows(err):
			return c.rel, "finished an earlier move", false, nil
		case err != nil:
			return "", "", false, fmt.Errorf("check who owns %s in the target: %w", c.rel, err)
		}
	}
	free, err := m.to.freeEntryPath(kind, srcRel, id)
	if err != nil {
		return "", "", false, err
	}
	if free != srcRel {
		note = fmt.Sprintf("written as %s: %s is taken in the target", free, srcRel)
	}
	return free, note, true, nil
}

// differentCopy returns the first of rels in store that holds a file of this id other than data.
func (m *mover) differentCopy(store *FileStore, kind, id string, data []byte, rels []string) string {
	for _, c := range store.copiesOf(kind, id, rels) {
		if !bytes.Equal(c.data, data) {
			return c.rel
		}
	}
	return ""
}

// leftovers removes the copies of the entry's file that this store still holds outside its index
// (other than except, which the move itself handled), when they are the moved file itself, and
// returns what it could not remove: a different copy, or one it failed to remove. targetRel is the
// entry's file in the target, which a copy here must not resolve to.
func (m *mover) leftovers(id, kind string, data []byte, except, targetRel string) []string {
	var problems []string
	synced := false // the target's file, before the first copy here goes
	for _, rel := range m.fromFiles[id] {
		if rel == except {
			continue
		}
		path, err := movePath(m.from, m.to, rel)
		if err != nil {
			problems = append(problems, fmt.Sprintf("a copy of its file at %s is left here: %v", rel, err))
			continue
		}
		if sameResolvedFile(path, joinStore(m.to, targetRel)) {
			problems = append(problems, fmt.Sprintf("the copy of its file at %s is the target's file (through a link); left as it is", rel))
			continue
		}
		held, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue // gone since the walk
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("a copy of its file at %s cannot be read: %v; check it by hand", rel, err))
			continue
		}
		f, perr := parseEntryFile(kind, held)
		if perr == nil && f.id != id {
			continue // another entry's file now
		}
		if perr != nil {
			problems = append(problems, fmt.Sprintf("a copy of its file at %s no longer parses: %v; check it by hand", rel, perr))
			continue
		}
		if !bytes.Equal(held, data) {
			problems = append(problems, fmt.Sprintf("a different copy of its file sits in this store at %s, outside the index; compare it with the target's and remove it by hand", rel))
			continue
		}
		if m.dryRun {
			m.warn("%s %s: a leftover copy of its file sits in this store at %s; it would be removed", kind, id, rel)
			continue
		}
		if !synced {
			if err := syncFile(joinStore(m.to, targetRel)); err != nil {
				problems = append(problems, fmt.Sprintf("a leftover copy of its file at %s is left here: the target's file %s could not be synced: %v", rel, targetRel, err))
				continue
			}
			synced = true
		}
		moveStep("before-leftover-remove")
		changed, aside, err := removeUnchanged(path, data)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("a leftover copy of its file at %s could not be removed: %v", rel, err))
			continue
		case changed:
			problems = append(problems, fmt.Sprintf("a leftover copy of its file at %s changed during the move (%s); compare it with the target's file and remove it by hand", rel, m.whereChanged(rel, aside)))
			continue
		}
		m.warn("%s %s: removed a leftover copy of its file from this store (%s)", kind, id, rel)
	}
	return problems
}

// fileCopy is a file of a store that holds a given entry.
type fileCopy struct {
	rel  string
	data []byte
}

// copiesOf returns the files among rels that hold this entry: regular files inside this store, read
// and parsed now, with this id.
func (fs *FileStore) copiesOf(kind, id string, rels []string) []fileCopy {
	var copies []fileCopy
	seen := map[string]bool{}
	for _, rel := range rels {
		if seen[rel] || !validEntryPath(kind, rel) {
			continue
		}
		seen[rel] = true
		path := filepath.Join(fs.baseDir, rel)
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if f, err := parseEntryFile(kind, data); err == nil && f.id == id {
			copies = append(copies, fileCopy{rel: rel, data: data})
		}
	}
	return copies
}

// kindOfPath is the kind of entry a store keeps at rel, or "".
func kindOfPath(rel string) string {
	for _, kind := range []string{"note", "session"} {
		if validEntryPath(kind, rel) {
			return kind
		}
	}
	return ""
}

// entryFilesByID walks a store's notes and sessions. It maps each wanted id to the files whose
// frontmatter names it, also a file a stopped move set aside, and returns the ids of every note file,
// sorted: a session's related note may sit in a file the index does not list.
func entryFilesByID(baseDir string, want map[string]bool) (map[string][]string, []string) {
	found := map[string][]string{}
	var notes []string
	for _, sub := range []string{"notes", "sessions"} {
		filepath.Walk(filepath.Join(baseDir, sub), func(path string, info os.FileInfo, err error) error {
			if err != nil || !info.Mode().IsRegular() || strings.HasPrefix(info.Name(), "._") {
				return nil
			}
			setAside := isSetAside(info.Name())
			if !setAside && !strings.HasSuffix(info.Name(), ".md") {
				return nil
			}
			id := frontmatterID(path)
			if want[id] {
				rel, _ := filepath.Rel(baseDir, path)
				found[id] = append(found[id], rel)
			}
			if sub == "notes" && id != "" && !setAside {
				notes = append(notes, id)
			}
			return nil
		})
	}
	sort.Strings(notes)
	return found, notes
}

// idsWithPrefix returns the distinct ids of sorted that start with prefix.
func idsWithPrefix(sorted []string, prefix string) []string {
	var ids []string
	for i := sort.SearchStrings(sorted, prefix); i < len(sorted) && strings.HasPrefix(sorted[i], prefix); i++ {
		if len(ids) == 0 || ids[len(ids)-1] != sorted[i] {
			ids = append(ids, sorted[i])
		}
	}
	return ids
}

// frontmatterID is the top-level id in a file's YAML frontmatter, or "".
func frontmatterID(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), 1024*1024)
	for n := 0; sc.Scan() && n < 200; n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if n == 0 {
			if line != "---" {
				return ""
			}
			continue
		}
		if line == "---" {
			return ""
		}
		if value, ok := strings.CutPrefix(line, "id:"); ok {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
}

// movePath is rel inside store as an absolute path, checked for a move: it stays inside the store,
// it is not a symlink, and its directory, symlinks resolved, lies inside this store and not in the
// other one. A file reached through a link between the two stores is never written or removed.
func movePath(store, other *FileStore, rel string) (string, error) {
	path := filepath.Join(store.baseDir, rel)
	if err := validateFilepathWithinBase(store.baseDir, path); err != nil {
		return "", err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a symlink; move it by hand", rel)
	}
	dir := resolvedDir(nearestExisting(filepath.Dir(path)))
	if !isWithin(dir, resolvedDir(store.baseDir)) || isWithin(dir, resolvedDir(other.baseDir)) {
		return "", fmt.Errorf("%s is reached through a symlink that leads out of its store; move it by hand", rel)
	}
	return path, nil
}

// sameResolvedFile reports whether two paths may name one file, so that nothing is removed through
// either: they are one path once symlinks are resolved, or they are one file in one directory reached
// by paths that resolve apart (a bind mount, or another alias the symlink check cannot see; the two
// stores then share that directory). Hard links in two directories are two names, and removing one
// leaves the other.
func sameResolvedFile(a, b string) bool {
	if resolvedPath(a) == resolvedPath(b) {
		return true
	}
	return sameInode(a, b) && sameInode(filepath.Dir(a), filepath.Dir(b))
}

// sameInode reports whether two paths, followed, are one existing file or directory.
func sameInode(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

// resolvedPath is path, absolute, with symlinks resolved, also when it does not exist yet: its
// closest existing ancestor resolved, with the rest appended.
func resolvedPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	base := nearestExisting(filepath.Dir(path))
	rest, err := filepath.Rel(base, path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Join(resolvedDir(base), rest)
}

// nearestExisting is dir, or its closest ancestor that exists.
func nearestExisting(dir string) string {
	for {
		if _, err := os.Lstat(dir); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

func joinStore(fs *FileStore, rel string) string { return filepath.Join(fs.baseDir, rel) }

// installFile puts data into the target at rel without replacing any file there: through a temp file
// linked into place, with the file and its directory synced. A file that appears at rel meanwhile is
// kept: when it is this very file it is used, when it holds this id otherwise the move is refused,
// and otherwise the next free name is taken. It returns where the file is, and whether this call
// created it.
func (m *mover) installFile(kind, id, rel string, data []byte) (string, bool, error) {
	wanted := rel
	for attempt := 0; attempt < 10; attempt++ {
		path, err := movePath(m.to, m.from, rel)
		if err != nil {
			return "", false, err
		}
		if err := mkdirAllSynced(filepath.Dir(path)); err != nil {
			return "", false, err
		}
		tmp, err := writeTemp(path, data)
		if err != nil {
			return "", false, err
		}
		err = linkNoReplace(tmp, path)
		os.Remove(tmp)
		if err == nil {
			if err := syncDir(filepath.Dir(path)); err != nil {
				os.Remove(path)
				return "", false, fmt.Errorf("sync the target directory: %w", err)
			}
			return rel, true, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", false, err
		}
		if existing, readErr := os.ReadFile(path); readErr == nil {
			if f, perr := parseEntryFile(kind, existing); perr == nil && f.id == id {
				if bytes.Equal(existing, data) {
					return rel, false, nil
				}
				return "", false, fmt.Errorf("a different file for this id appeared at %s in the target; compare them by hand", rel)
			}
		}
		if rel, err = m.to.freeEntryPath(kind, wanted, id); err != nil {
			return "", false, err
		}
	}
	return "", false, fmt.Errorf("no free file name for %s in the target", wanted)
}

// setAsideSuffix ends the name under which a move sets a file of this store aside while it checks
// that the file did not change before removing it: the file's own name, hidden, with this suffix. It
// does not end in .md, so a reindex never reads it; one that a stopped run left is found by the next
// run's walk and handled as a copy of the entry.
const setAsideSuffix = ".moving"

func setAsidePath(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+setAsideSuffix)
}

func isSetAside(name string) bool {
	return strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".md"+setAsideSuffix)
}

// removeUnchanged removes the file at path, in this store, if it still holds data, the bytes the
// move put into the target. The file is first set aside under a hidden name in its directory, so that
// a writer outside claudemem (an editor, an add-only sync) that saves it meanwhile makes a new file at
// its name instead of writing into one about to go; only then is it compared. A file that changed is
// put back under its name and reported as changed; when a new file took the name meanwhile, it stays
// set aside, and aside names it. A file already gone counts as removed.
func removeUnchanged(path string, data []byte) (changed bool, aside string, err error) {
	aside = path
	if !isSetAside(filepath.Base(path)) {
		aside = setAsidePath(path)
		if held, readErr := os.ReadFile(aside); readErr == nil { // set aside by a stopped run
			if !bytes.Equal(held, data) {
				return false, "", fmt.Errorf("a different copy that a stopped move set aside sits next to it (%s)", filepath.Base(aside))
			}
			if err := os.Remove(aside); err != nil {
				return false, "", err
			}
		} else if !os.IsNotExist(readErr) {
			return false, "", readErr
		}
		if err := os.Rename(path, aside); err != nil {
			if os.IsNotExist(err) {
				return false, "", nil
			}
			return false, "", err
		}
	}
	held, err := os.ReadFile(aside)
	if err == nil && bytes.Equal(held, data) {
		if err := os.Remove(aside); err != nil {
			if aside != path {
				os.Rename(aside, path)
			}
			return false, "", err
		}
		return false, "", nil
	}
	if aside == path {
		return true, "", nil
	}
	if err := linkNoReplace(aside, path); err != nil {
		return true, aside, nil
	}
	os.Remove(aside)
	return true, "", nil
}

// whereChanged says where a file of this store that changed during the move is now.
func (m *mover) whereChanged(rel, aside string) string {
	if aside == "" {
		return "it stays at " + rel
	}
	asideRel, err := filepath.Rel(m.from.baseDir, aside)
	if err != nil {
		asideRel = aside
	}
	return "it is set aside at " + asideRel + ", since a new file took its name"
}

// linkNoReplace makes tmp also appear at path, failing with os.ErrExist when path exists. On a file
// system without hard links it renames tmp to path after checking that path is free.
func linkNoReplace(tmp, path string) error {
	err := os.Link(tmp, path)
	if err == nil || errors.Is(err, os.ErrExist) {
		return err
	}
	if _, statErr := os.Lstat(path); statErr == nil {
		return os.ErrExist
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	return os.Rename(tmp, path)
}

// mkdirAllSynced creates dir and its missing parents, syncing the directory that holds each new one.
func mkdirAllSynced(dir string) error {
	var missing []string
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil || filepath.Dir(d) == d {
			break
		}
		missing = append(missing, d)
	}
	if len(missing) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := syncDir(filepath.Dir(missing[i])); err != nil {
			return fmt.Errorf("sync %s: %w", filepath.Dir(missing[i]), err)
		}
	}
	return nil
}

// syncFile makes a file and its directory durable. The move writes the target's file itself in sync;
// a file it finds there (left by an earlier run, a sync or another writer) is synced this way before
// a copy of it in this store goes. A variable, so that a test can make it fail.
var syncFile = func(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

// syncDir makes a directory's entries durable. Windows has no directory sync; its file systems keep
// the entries with the files.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// writeTemp writes data to a new temp file next to path and returns its name. The name starts with
// a dot and does not end in .md, so a reindex never picks it up.
func writeTemp(path string, data []byte) (string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "."+strings.TrimSuffix(filepath.Base(path), ".md")+".*.tmp")
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// removeStaleTemps removes the temp files that an earlier, stopped move left next to rel: those that
// hold exactly data, the moved file. Any other temp there is kept, also one of this entry: a note update
// or a tag edit that failed to put its file in place names its temp as the only copy of the new text.
func (fs *FileStore) removeStaleTemps(rel string, data []byte) {
	dir := filepath.Dir(filepath.Join(fs.baseDir, rel))
	temps, _ := filepath.Glob(filepath.Join(dir, ".*.tmp"))
	for _, tmp := range temps {
		if info, err := os.Lstat(tmp); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if held, err := os.ReadFile(tmp); err == nil && bytes.Equal(held, data) {
			os.Remove(tmp)
		}
	}
}

// freeEntryPath returns where an entry's file goes in this store: rel when no other entry's row
// names it and no other file sits there (a file holding this entry itself counts as free), else the
// first such "<name>-N.md" next to it.
func (fs *FileStore) freeEntryPath(kind, rel, id string) (string, error) {
	dir := filepath.Dir(rel)
	base := strings.TrimSuffix(filepath.Base(rel), ".md")
	for i := 1; i <= 1000; i++ {
		name := base + ".md"
		if i > 1 {
			name = fmt.Sprintf("%s-%d.md", base, i)
		}
		candidate := filepath.Join(dir, name)
		var owner string
		err := fs.db.QueryRow(`SELECT id FROM entries WHERE filepath = ?`, candidate).Scan(&owner)
		if err != nil && !isNoRows(err) {
			return "", fmt.Errorf("check who owns %s in the target: %w", candidate, err)
		}
		if err == nil && owner != id {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(fs.baseDir, candidate))
		if readErr == nil {
			if f, perr := parseEntryFile(kind, data); perr != nil || f.id != id {
				continue
			}
		} else if !os.IsNotExist(readErr) {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("no free file name for %s in the target", rel)
}

// validEntryPath reports whether rel is where a file of this kind lives: notes/<category>/<name>.md
// or sessions/<name>.md, inside the store.
func validEntryPath(kind, rel string) bool {
	if filepath.IsAbs(rel) || strings.Contains(filepath.ToSlash(rel), "..") || !strings.HasSuffix(rel, ".md") {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	switch kind {
	case "note":
		return len(parts) == 3 && parts[0] == "notes"
	case "session":
		return len(parts) == 2 && parts[0] == "sessions"
	}
	return false
}

// lockBoth takes the write locks of both stores, always in the same order (by resolved directory),
// so two moves running in opposite directions cannot deadlock.
func lockBoth(a, b *FileStore) (func(), error) {
	first, second := a, b
	if resolvedDir(b.baseDir) < resolvedDir(a.baseDir) {
		first, second = b, a
	}
	unlockFirst, err := first.lockStore()
	if err != nil {
		return nil, err
	}
	unlockSecond, err := second.lockStore()
	if err != nil {
		unlockFirst()
		return nil, err
	}
	return func() {
		unlockSecond()
		unlockFirst()
	}, nil
}

// sameStoreDir reports whether two store directories are one, also when reached by a symlink or a
// bind mount.
func sameStoreDir(a, b string) bool {
	if resolvedDir(a) == resolvedDir(b) {
		return true
	}
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

// StoresOverlap reports whether two store directories are one, or one sits inside the other, after
// symlinks are resolved and also through a bind mount or another alias that only the device and inode
// numbers show. Such stores share files, so neither move nor a store read across them is allowed.
func StoresOverlap(a, b string) bool {
	if sameStoreDir(a, b) {
		return true
	}
	ra, rb := resolvedDir(a), resolvedDir(b)
	return isWithin(ra, rb) || isWithin(rb, ra) || insideByInode(ra, b) || insideByInode(rb, a)
}

// insideByInode reports whether dir or one of its ancestors is the directory other (same device and
// inode): dir is reached through an alias of other, such as a bind mount.
func insideByInode(dir, other string) bool {
	oi, err := os.Stat(other)
	if err != nil {
		return false
	}
	for d := dir; ; d = filepath.Dir(d) {
		if di, err := os.Stat(d); err == nil && os.SameFile(di, oi) {
			return true
		}
		if filepath.Dir(d) == d {
			return false
		}
	}
}

// isWithin reports whether path is dir or lies under it (both resolved, absolute).
func isWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// resolvedDir is dir as an absolute path with symlinks resolved, as far as they resolve.
func resolvedDir(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return filepath.Clean(dir)
}
