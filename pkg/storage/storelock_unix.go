//go:build !windows

package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockStore takes the store-wide exclusive write lock and returns the function that releases it.
//
// Every mutating path (AddNote, UpdateNote, DeleteNote) is a read-modify-write on files that the
// SQLite transaction does not cover, and claudemem is one process per CLI call — parallel agents
// running `claudemem note add` are separate processes, so an in-process mutex would not serialise
// them. A flock on a file under the store does: it serialises across processes AND across
// goroutines of one process (each call opens its own descriptor, and flock locks belong to the
// open file description, so two descriptors of one process still exclude each other).
//
// Review of 2026-09-03 (evening): without this, concurrent dedup-merges into one note each read
// the same base and the last writer won (38 of 41 appended paragraphs lost in the regression
// test), and two notes with one slug moving into one category could both be told the same name
// was free and the second rename replaced the first note's file.
//
// The lock is coarse on purpose: a write takes milliseconds, correctness beats concurrency here,
// and there is exactly one lock so no ordering can deadlock. It is NOT reentrant — callers that
// already hold it use the *Locked variants of the mutating methods.
func (fs *FileStore) lockStore() (func(), error) {
	path := filepath.Join(fs.baseDir, ".write.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open store lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock store (%s): %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
