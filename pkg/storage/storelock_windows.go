//go:build windows

package storage

// Windows is not a supported claudemem target (CGO_ENABLED=0 darwin/linux binaries); keep the
// package building there with a no-op lock rather than a flock that does not exist.
func (fs *FileStore) lockStore() (func(), error) { return func() {}, nil }
