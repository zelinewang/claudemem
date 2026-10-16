package storage

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The move journal records, in the target, each file a move is putting there and has not committed
// the target's rows for yet: one small file per entry under .index/move-pending, written durably
// before the move creates its temp file. A move that stops before its rows commit leaves the record
// behind, and the next move into that store removes what the record names while it still holds the
// bytes the move wrote: the temp file, and the entry's file when the target's index does not list it
// there. So a rerun does not trip over its own leftover (refused as "a different file", after the
// entry changed here), and a stopped move leaves no copy of an entry, a private note say, in the
// target. The rows of this store are only removed after the target's rows commit, so a file the
// journal removes always still has its entry here. What a stopped move put into the target while the
// store the entry came from no longer holds it stays, and its record is marked kept: a reindex of the
// target lists that file, so the entry stays there, and settles the record (finishKeptMoves).

// pendingInstall is one record of the journal.
type pendingInstall struct {
	ID     string `json:"id"`
	File   string `json:"file"`   // where the entry's file goes, relative to the store
	Temp   string `json:"temp"`   // the temp file it is written to first, relative to the store
	SHA256 string `json:"sha256"` // of the file's bytes
	// From is the store the entry moves from (its directory, symlinks resolved), and FromFile the
	// entry's file there: the journal removes what the move put into the target only while that store
	// still holds the entry. A record without them is checked against the store a run moves from.
	From     string `json:"from,omitempty"`
	FromFile string `json:"from_file,omitempty"`
	// Kept is set by a move that found the store the entry came from no longer holding it, and kept
	// what the stopped move put into the target, since it may be the entry's only copy.
	Kept bool `json:"kept,omitempty"`
}

// namesMoveFiles reports whether the record names an entry's file and a temp file next to it, as the
// records a move writes do.
func (p pendingInstall) namesMoveFiles() bool {
	tempDir, tempName := filepath.Dir(p.Temp), filepath.Base(p.Temp)
	return kindOfPath(p.File) != "" && tempDir == filepath.Dir(p.File) &&
		strings.HasPrefix(tempName, ".") && strings.HasSuffix(tempName, ".tmp")
}

func (fs *FileStore) pendingDir() string { return filepath.Join(fs.indexDir, "move-pending") }

// pendingPath is where the record of an entry's install lives; the name is a hash of the id, so any
// id makes a plain file name.
func (fs *FileStore) pendingPath(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(fs.pendingDir(), hex.EncodeToString(sum[:16])+".json")
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// recordPending writes the record of an install durably, replacing the entry's earlier one.
func (fs *FileStore) recordPending(p pendingInstall) error {
	return fs.writePendingRecord(fs.pendingPath(p.ID), p)
}

// writePendingRecord writes a record of the journal durably at path, replacing the one there.
func (fs *FileStore) writePendingRecord(path string, p pendingInstall) error {
	if err := mkdirAllSynced(fs.pendingDir()); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tmp, err := writeTemp(path, data)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(fs.pendingDir())
}

// clearPending removes the record of an entry's install: its rows committed, or it was rolled back.
// A record that outlives a crash here is resolved by the next move, which finds the rows.
func (fs *FileStore) clearPending(id string) {
	os.Remove(fs.pendingPath(id))
}

// pendingInstalls reads the journal. A record that cannot be read or parsed is returned in bad.
func (fs *FileStore) pendingInstalls() (records []pendingInstall, paths []string, bad []string) {
	entries, err := os.ReadDir(fs.pendingDir())
	if err != nil {
		return nil, nil, nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(fs.pendingDir(), e.Name())
		data, err := os.ReadFile(path)
		var p pendingInstall
		if err == nil {
			err = json.Unmarshal(data, &p)
		}
		if err != nil || p.ID == "" {
			bad = append(bad, e.Name())
			continue
		}
		records = append(records, p)
		paths = append(paths, path)
	}
	return records, paths, bad
}

// PendingMoveRecords counts the records in the store's move journal that the next move into the store
// resolves: each is a file that a move into this store put here before it stopped, without its rows
// (or a record that cannot be read), and a reindex lists such files. The records a move marked kept
// are not counted: a reindex settles them (finishKeptMoves).
func (fs *FileStore) PendingMoveRecords() int {
	records, _, bad := fs.pendingInstalls()
	n := len(bad)
	for _, p := range records {
		if !p.Kept {
			n++
		}
	}
	return n
}

// newTempPath is a fresh temp name next to path: hidden, not ending in .md, so a reindex never reads
// it, and in the same form as the temps of the store's other writers.
func newTempPath(path string) string {
	var b [6]byte
	rand.Read(b[:])
	return filepath.Join(filepath.Dir(path), "."+strings.TrimSuffix(filepath.Base(path), ".md")+"."+hex.EncodeToString(b[:])+".tmp")
}

// writeNewFile writes data to a file that must not exist yet, and syncs it.
func writeNewFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

// recoverPending resolves the journal of the target before a run, under the target's lock: a record
// there is from a move that stopped before its rows committed (a running move holds the lock from its
// record to its commit). It removes the temp and the entry's file the record names while they still
// hold the bytes the move wrote, unless the target's index lists the entry at that file, which means
// its rows did commit; then the record goes. While the store the entry came from no longer holds it,
// what the move put into the target may be its only copy: it stays, and the record is marked kept,
// for a reindex of the target to settle. In a dry run it only says what it would remove, and only
// while no writer holds the target's lock: a record found then may be that of a move still running.
func (m *mover) recoverPending() error {
	records, paths, bad := m.to.pendingInstalls()
	for _, name := range bad {
		m.warn("the target's move journal holds a record that cannot be read (%s); left as it is", filepath.Join(".index", "move-pending", name))
	}
	if len(records) == 0 {
		return nil
	}
	if m.dryRun && storeLockHeld(m.to.baseDir) {
		m.warn("the target's move journal holds %d records while a writer holds the target's lock: another move into it is probably running, so this dry run leaves its journal alone; run the dry run again when that move is done", len(records))
		return nil
	}
	if !m.dryRun {
		unlock, err := m.to.lockStore()
		if err != nil {
			return err
		}
		defer unlock()
	}
	for i, p := range records {
		m.resolvePending(p, paths[i])
	}
	return nil
}

func (m *mover) resolvePending(p pendingInstall, record string) {
	kind := kindOfPath(p.File)
	if !p.namesMoveFiles() {
		m.warn("the target's move journal holds a record for %s that names %s and %s, not a move's files; left as it is", p.ID, p.File, p.Temp)
		return
	}
	verb := "removed"
	if m.dryRun {
		verb = "would remove"
	}
	committed := false
	if row, err := m.to.indexRow(p.ID); err == nil && row != nil && row.filepath == p.File {
		committed = true
	}
	if !committed {
		if held, from := m.sourceHolds(p); !held {
			var left []string
			for _, rel := range []string{p.Temp, p.File} {
				if path, err := movePath(m.to, m.from, rel); err == nil && (holdsBytes(path, p.SHA256) || frontmatterID(path) == p.ID) {
					left = append(left, rel)
				}
			}
			if len(left) > 0 {
				m.keep(p, record, kind, from, left)
				return
			}
		}
	}
	keep := false // the record stays while a file it names could not be checked or removed
	remove := func(rel, what string) {
		path, err := movePath(m.to, m.from, rel)
		switch {
		case err != nil:
			m.warn("%s %s: %s %s cannot be checked: %v; left as it is, and the journal keeps its record", kind, p.ID, what, rel, err)
			keep = true
			return
		case !holdsBytes(path, p.SHA256):
			if what == "the file" && frontmatterID(path) == p.ID { // this entry's file, but not the bytes the move wrote
				m.warn("%s %s: a stopped move may have put %s into the target without its rows, and it changed since; check it by hand", kind, p.ID, rel)
			}
			return
		case m.dryRun:
		default:
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				m.warn("%s %s: could not remove %s, which a stopped move left in the target: %v; the journal keeps its record, so the next move tries again", kind, p.ID, rel, err)
				keep = true
				return
			}
		}
		if what == "the temp file" {
			m.warn("%s %s: %s the temp file %s that a stopped move left in the target", kind, p.ID, verb, rel)
		} else {
			m.warn("%s %s: %s %s, which a stopped move put into the target without its rows", kind, p.ID, verb, rel)
		}
	}
	remove(p.Temp, "the temp file")
	if !committed {
		remove(p.File, "the file")
	}
	if !m.dryRun && !keep {
		os.Remove(record)
	}
}

// keep leaves what a stopped move put into the target while the store the entry came from no longer
// holds it, since it may be its only copy, and marks its record kept: a reindex of the target then
// lists the file, so the entry stays there, and clears the record (finishKeptMoves). The warning says
// so, and with only the temp file left, to rename it to the entry's file first. A dry run marks
// nothing.
func (m *mover) keep(p pendingInstall, record, kind, from string, left []string) {
	kept, then := "kept, since it may be its only copy", "to keep the entry there"
	if m.dryRun {
		kept, then = "a run keeps it, since it may be its only copy", "after that run, to keep the entry there"
	}
	if len(left) == 1 && left[0] == p.Temp {
		m.warn("%s %s: a stopped move left its temp file %s in the target, not yet renamed to %s, and %s no longer holds the entry: %s; %s, rename it to %s and reindex the target, which also clears this record; otherwise remove it by hand",
			kind, p.ID, p.Temp, p.File, from, kept, then, p.File)
	} else {
		files := strings.Join(left, " and ")
		m.warn("%s %s: a stopped move put %s into the target without its rows, and %s no longer holds it: %s; %s, reindex the target, which also clears this record; otherwise remove %s by hand",
			kind, p.ID, files, from, kept, then, files)
	}
	if m.dryRun || p.Kept {
		return
	}
	p.Kept = true
	if err := m.to.writePendingRecord(record, p); err != nil {
		m.warn("%s %s: its record in the target's move journal could not be marked kept (%v), so a reindex leaves the record; the next move after that reindex clears it", kind, p.ID, err)
	}
}

// finishKeptMoves settles, once a reindex has listed this store's files and under its lock, the
// records of the move journal that a move marked kept. The entry of a file the index now lists there
// stays in this store: its record goes, and so does the temp file the move left. With only the temp
// file left, it says to rename that first; with nothing of what the move put here left (removed by
// hand), the record goes. A record it does not settle is left for the next move.
func (fs *FileStore) finishKeptMoves() {
	records, paths, _ := fs.pendingInstalls()
	for i, p := range records {
		if !p.Kept || !p.namesMoveFiles() {
			continue
		}
		kind := kindOfPath(p.File)
		temp, tempErr := movePath(fs, nil, p.Temp)
		file, fileErr := movePath(fs, nil, p.File)
		row, rowErr := fs.indexRow(p.ID)
		switch {
		case tempErr != nil || fileErr != nil || rowErr != nil:
		case row != nil && row.filepath == p.File:
			if holdsBytes(temp, p.SHA256) {
				if err := os.Remove(temp); err != nil {
					fmt.Fprintf(os.Stderr, "warning: %s %s: this reindex lists %s, which a stopped move kept here, but could not remove its temp file %s: %v; its record in .index/move-pending stays\n", kind, p.ID, p.File, p.Temp, err)
					continue
				}
			}
			os.Remove(paths[i])
			fmt.Fprintf(os.Stderr, "warning: %s %s: this reindex lists %s, which a stopped move kept here because the store the entry came from no longer holds it: the entry stays in this store, and its record in .index/move-pending is cleared\n", kind, p.ID, p.File)
		case holdsBytes(temp, p.SHA256):
			fmt.Fprintf(os.Stderr, "warning: %s %s: a stopped move kept its temp file %s here, not yet renamed to %s, because the store the entry came from no longer holds it: to keep the entry, rename it to %s and reindex again, which also clears this record; otherwise remove it by hand\n", kind, p.ID, p.Temp, p.File, p.File)
		case frontmatterID(file) != p.ID:
			os.Remove(paths[i]) // nothing of what the move put here is left
		}
	}
}

// sourceHolds reports whether the store that a recorded move took an entry from still holds a file
// of it (the file named in the record, or the one its index lists, or either set aside), and names
// that store. A file that is the target's own file (through a link) does not count.
func (m *mover) sourceHolds(p pendingInstall) (bool, string) {
	dir, name := p.From, p.From
	if dir == "" || sameStoreDir(dir, m.from.baseDir) {
		dir, name = m.from.baseDir, "this store"
	}
	var rels []string
	if p.FromFile != "" {
		rels = append(rels, p.FromFile)
	}
	if dir == m.from.baseDir {
		if row, err := m.from.indexRow(p.ID); err == nil && row != nil {
			rels = append(rels, row.filepath)
		}
	}
	target := joinStore(m.to, p.File)
	for _, rel := range rels {
		if kindOfPath(rel) == "" {
			continue
		}
		path := filepath.Join(dir, rel)
		for _, candidate := range []string{path, setAsidePath(path)} {
			if info, err := os.Lstat(candidate); err == nil && info.Mode().IsRegular() &&
				!sameResolvedFile(candidate, target) && frontmatterID(candidate) == p.ID {
				return true, name
			}
		}
	}
	return false, name
}

// holdsBytes reports whether the regular file at path holds the bytes with this SHA-256.
func holdsBytes(path, sum string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && sha256Hex(data) == sum
}
