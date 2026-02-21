package models

import "time"

// Entry is the common interface for all memory entries
type Entry interface {
	GetID() string
	GetType() string      // "note" or "session"
	GetTitle() string
	GetContent() string
	GetTags() []string
	GetCreated() time.Time
}