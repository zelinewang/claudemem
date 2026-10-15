//go:build windows

package storage

import "fmt"

// Windows is not a supported claudemem target (CGO_ENABLED=0 darwin/linux binaries); keep the
// package building there with a no-op lock rather than a flock that does not exist. A store opened
// only for reading still refuses it.
func (fs *FileStore) lockStore() (func(), error) {
	if fs.readOnly {
		return nil, fmt.Errorf("this store is open only for reading; writes go to --store")
	}
	return func() {}, nil
}
