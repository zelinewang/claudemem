//go:build windows

package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// lockStore takes the store-wide exclusive write lock and returns the function that releases it. It
// is the same lock as on other systems (storelock_unix.go explains why every writer takes it), taken
// with LockFileEx instead of flock. A store opened only for reading refuses it.
func (fs *FileStore) lockStore() (func(), error) {
	if fs.readOnly {
		return nil, fmt.Errorf("this store is open only for reading; writes go to --store")
	}
	return LockStoreDir(fs.baseDir)
}

// LockStoreDir takes the write lock of the store in dir: an exclusive LockFileEx lock on the first
// byte of its .write.lock file. The lock belongs to the open handle, so two handles exclude each
// other, also within one process, and Windows drops it when the holder exits. The wait polls every
// millisecond up to lockTimeout, as on other systems, and the lock is not reentrant either.
func LockStoreDir(dir string) (func(), error) {
	path := filepath.Join(dir, ".write.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open store lock %s: %w", path, err)
	}
	handle := windows.Handle(f.Fd())
	deadline := time.Now().Add(lockTimeout)
	for {
		err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			f.Close()
			return nil, fmt.Errorf("lock store (%s): %w", path, err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("store is locked by another claudemem writer for more than %s (%s) — a write is stuck or the lock was re-entered; retry", lockTimeout, path)
		}
		time.Sleep(time.Millisecond)
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, new(windows.Overlapped))
		_ = f.Close()
	}, nil
}
