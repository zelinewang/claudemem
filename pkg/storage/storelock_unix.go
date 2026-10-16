//go:build !windows

package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockStore takes the store-wide exclusive write lock and returns the function that releases it.
//
// Every mutating path (AddNote, UpdateNote, DeleteNote, SaveSession) is a read-modify-write on
// files that the SQLite transaction does not cover, and claudemem is one process per CLI call —
// parallel agents running `claudemem note add` are separate processes, so an in-process mutex
// would not serialise them. A flock on a file under the store does: it serialises across
// processes AND across goroutines of one process (each call opens its own descriptor, and flock
// locks belong to the open file description, so two descriptors of one process still exclude
// each other). The kernel drops it when the holder dies, so a crash never leaves a stale lock.
//
// Review of 2026-09-03 (evening): without this, concurrent dedup-merges into one note each read
// the same base and the last writer won (38 of 41 appended paragraphs lost in the regression
// test), and two notes with one slug moving into one category could both be told the same name
// was free and the second rename replaced the first note's file.
//
// The lock is coarse on purpose: a write takes milliseconds, correctness beats concurrency here,
// and there is exactly one lock so no ordering can deadlock. It is NOT reentrant — callers that
// already hold it use the *Locked variants of the mutating methods; a re-entry fails after
// lockTimeout with the error below instead of hanging.
func (fs *FileStore) lockStore() (func(), error) {
	if fs.readOnly {
		return nil, fmt.Errorf("this store is open only for reading; writes go to --store")
	}
	return LockStoreDir(fs.baseDir)
}

// LockStoreDir takes the write lock of the store in dir, the same lock the store's own writes and a
// move take. It is for a writer that changes the store's files without going through the store, such
// as git sync and import. While holding it, the caller must not call a store method that writes: the
// lock is not reentrant, and that call would wait out lockTimeout and fail.
func LockStoreDir(dir string) (func(), error) {
	path := filepath.Join(dir, ".write.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open store lock %s: %w", path, err)
	}
	deadline := time.Now().Add(lockTimeout)
	// A flat 1 ms poll. The hold time of a write is sub-millisecond, so under contention the cost that
	// matters is the handoff after a release: a blocking flock hands off at once, a 10 ms poll cost
	// 4–8 ms per handoff (a 40-goroutine merge went 0.03 s → 0.43 s), and exponential backoff was
	// worse (the longest waiter — the likely next holder — is also the sleepiest: up to 13.8 ms).
	// 1 ms restores the blocking-lock numbers (0.05 s; handoff ≤ 1 ms, the poll interval) at ~2% of one core while
	// contended, and keeps the bound and the loud error (review of PR #22, rounds 2–3, measured).
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, fmt.Errorf("lock store (%s): %w", path, err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("store is locked by another claudemem writer for more than %s (%s) — a write is stuck or the lock was re-entered; retry, or find the holder with `lsof %s`", lockTimeout, path, path)
		}
		time.Sleep(time.Millisecond)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// storeLockHeld reports whether a writer holds the write lock of the store in dir, without waiting
// and without creating anything: a shared lock on its lock file is tried once and let go. A store
// whose lock file does not exist has no holder.
func storeLockHeld(dir string) bool {
	f, err := os.Open(filepath.Join(dir, ".write.lock"))
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return err == syscall.EWOULDBLOCK || err == syscall.EAGAIN
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}
