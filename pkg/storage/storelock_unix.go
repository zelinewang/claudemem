//go:build !windows

package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockTimeout bounds how long a writer waits for the store lock. A bare LOCK_EX would block forever
// with no output while another process holds the lock (review of PR #22, round 1, P2-3); it also
// turned an accidental re-entry — the lock is a flock and is NOT reentrant — into a silent hang
// (P2-2). With a bound, both become a loud error naming the lock file. Overridable for tests.
var lockTimeout = 60 * time.Second

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
	path := filepath.Join(fs.baseDir, ".write.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open store lock %s: %w", path, err)
	}
	deadline := time.Now().Add(lockTimeout)
	// A flat 1 ms poll. The hold time of a write is sub-millisecond, so under contention the cost that
	// matters is the handoff after a release: a blocking flock hands off at once, a 10 ms poll cost
	// 4–8 ms per handoff (a 40-goroutine merge went 0.03 s → 0.43 s), and exponential backoff was
	// worse (the longest waiter — the likely next holder — is also the sleepiest: up to 13.8 ms).
	// 1 ms restores the blocking-lock numbers (0.05 s; handoff ≤ 0.5 ms) at ~2% of one core while
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
