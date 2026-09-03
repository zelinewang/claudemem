package storage

import "github.com/zelinewang/claudemem/pkg/models"

// The public mutating methods take the store-wide write lock (see storelock_unix.go) and delegate
// to the *Locked implementations, which assume the lock is held. Internal callers that already hold
// it (AddNote's dedup-merge path calling updateNoteLocked) must never re-enter the public method:
// the lock is a flock and is not reentrant.

// AddNote creates a note, or merges it into an existing note of the same category and title.
func (fs *FileStore) AddNote(note *models.Note) (*AddNoteResult, error) {
	unlock, err := fs.lockStore()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return fs.addNoteLocked(note)
}

// UpdateNote rewrites a note in place (the filename is stable; only a category change moves it).
func (fs *FileStore) UpdateNote(note *models.Note) error {
	unlock, err := fs.lockStore()
	if err != nil {
		return err
	}
	defer unlock()
	return fs.updateNoteLocked(note)
}

// DeleteNote removes a note's file and index rows.
func (fs *FileStore) DeleteNote(id string) error {
	unlock, err := fs.lockStore()
	if err != nil {
		return err
	}
	defer unlock()
	return fs.deleteNoteLocked(id)
}
