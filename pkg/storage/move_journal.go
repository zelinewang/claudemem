package storage

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
// journal removes always still has its entry here.

// pendingInstall is one record of the journal.
type pendingInstall struct {
	ID     string `json:"id"`
	File   string `json:"file"`   // where the entry's file goes, relative to the store
	Temp   string `json:"temp"`   // the temp file it is written to first, relative to the store
	SHA256 string `json:"sha256"` // of the file's bytes
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
	if err := mkdirAllSynced(fs.pendingDir()); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	path := fs.pendingPath(p.ID)
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
// its rows did commit; then the record goes. In a dry run it only says what it would remove.
func (m *mover) recoverPending() error {
	records, paths, bad := m.to.pendingInstalls()
	for _, name := range bad {
		m.warn("the target's move journal holds a record that cannot be read (%s); left as it is", filepath.Join(".index", "move-pending", name))
	}
	if len(records) == 0 {
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
	tempDir, tempName := filepath.Dir(p.Temp), filepath.Base(p.Temp)
	if kind == "" || tempDir != filepath.Dir(p.File) || !strings.HasPrefix(tempName, ".") || !strings.HasSuffix(tempName, ".tmp") {
		m.warn("the target's move journal holds a record for %s that names %s and %s, not a move's files; left as it is", p.ID, p.File, p.Temp)
		return
	}
	verb := "removed"
	if m.dryRun {
		verb = "would remove"
	}
	if path, err := movePath(m.to, m.from, p.Temp); err == nil && holdsBytes(path, p.SHA256) {
		if m.dryRun || os.Remove(path) == nil {
			m.warn("%s %s: %s the temp file %s that a stopped move left in the target", kind, p.ID, verb, p.Temp)
		}
	}
	committed := false
	if row, err := m.to.indexRow(p.ID); err == nil && row != nil && row.filepath == p.File {
		committed = true
	}
	if path, err := movePath(m.to, m.from, p.File); err == nil && !committed {
		switch {
		case holdsBytes(path, p.SHA256):
			if m.dryRun || os.Remove(path) == nil {
				m.warn("%s %s: %s %s, which a stopped move put into the target without its rows", kind, p.ID, verb, p.File)
			}
		case frontmatterID(path) == p.ID: // this entry's file, but not the bytes the move wrote
			m.warn("%s %s: a stopped move may have put %s into the target without its rows, and it changed since; check it by hand", kind, p.ID, p.File)
		}
	}
	if !m.dryRun {
		os.Remove(record)
	}
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
