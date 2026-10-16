package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A tag filter is a list of required tags: an entry passes when it carries every one of them as a
// whole tag, compared case-insensitively (the rule noteHasAllTags always used). The index keeps an
// entry's tags as one space-joined string (entries.tags), so the SQL side matches whole
// space-delimited tokens. The substring LIKE used before let "team" match "team-old".

var (
	// ErrEntryNotFound reports that no note or session has the given id.
	ErrEntryNotFound = errors.New("entry not found")
	// ErrWrongKind reports that the id exists but belongs to the other kind (a note id given as a session).
	ErrWrongKind = errors.New("entry is of the other kind")
)

// MissingTagsError reports that the entry a get names exists but does not carry every tag asked
// for. Tags filter what a command returns; they do not hide that an entry exists.
type MissingTagsError struct {
	Kind string   // "note" or "session"
	ID   string   // the id or id prefix asked for
	Tags []string // the tags asked for
}

func (e *MissingTagsError) Error() string {
	word := "tags"
	if len(e.Tags) == 1 {
		word = "tag"
	}
	return fmt.Sprintf("%s %s does not carry the %s %s", e.Kind, e.ID, word, strings.Join(e.Tags, ", "))
}

// entryExists reports whether an entry of this kind has the id, or an id starting with it.
func (fs *FileStore) entryExists(kind, id string) (bool, error) {
	var one int
	err := fs.db.QueryRow(`SELECT 1 FROM entries WHERE (id = ? OR id LIKE ? || '%') AND type = ? LIMIT 1`, id, id, kind).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// HasAllTags reports whether have carries every tag in required, compared case-insensitively.
// An empty required list passes everything.
func HasAllTags(have, required []string) bool {
	for _, req := range required {
		found := false
		for _, tag := range have {
			if strings.EqualFold(tag, req) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// tagsWhere returns a WHERE fragment, starting with " AND", that keeps the rows whose space-joined
// tags column carries every required tag as a whole token, and its arguments. SQLite's LIKE is
// case-insensitive for ASCII, the same rule as HasAllTags for ASCII tags; % and _ in a tag are
// escaped so they match only themselves. No required tags gives an empty fragment.
func tagsWhere(column string, required []string) (string, []interface{}) {
	var sb strings.Builder
	args := make([]interface{}, 0, len(required))
	for _, tag := range required {
		sb.WriteString(" AND (' ' || " + column + " || ' ') LIKE ? ESCAPE '\\'")
		args = append(args, "% "+likeEscaper.Replace(tag)+" %")
	}
	return sb.String(), args
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// EditTagList returns tags with every tag in remove dropped and every tag in add appended unless
// already present, both compared case-insensitively; the tags that stay keep their order.
func EditTagList(tags, add, remove []string) []string {
	result := make([]string, 0, len(tags)+len(add))
	for _, tag := range tags {
		if !containsFold(remove, tag) {
			result = append(result, tag)
		}
	}
	for _, tag := range add {
		if !containsFold(result, tag) {
			result = append(result, tag)
		}
	}
	return result
}

func containsFold(list []string, s string) bool {
	for _, item := range list {
		if strings.EqualFold(item, s) {
			return true
		}
	}
	return false
}

// ValidateTagEdit checks the tags an edit adds and removes. An added tag must be one word, with no
// whitespace or comma: the index joins tags with spaces and the CLI splits lists on commas, so
// anything else would not survive a filter. Every tag must be non-empty and fit MaxTagLen, and no
// tag may be both added and removed.
func ValidateTagEdit(add, remove []string) error {
	for _, tag := range add {
		if tag == "" {
			return fmt.Errorf("empty tag in the tags to add")
		}
		if len(tag) > MaxTagLen {
			return fmt.Errorf("tag too long (max %d chars): %q", MaxTagLen, tag)
		}
		if strings.ContainsRune(tag, 0) {
			return fmt.Errorf("tag contains null byte")
		}
		if err := validateNewTags([]string{tag}, nil); err != nil {
			return err
		}
	}
	for _, tag := range remove {
		if strings.TrimSpace(tag) == "" {
			return fmt.Errorf("empty tag in the tags to remove")
		}
		if len(tag) > MaxTagLen {
			return fmt.Errorf("tag too long (max %d chars): %q", MaxTagLen, tag)
		}
	}
	for _, tag := range add {
		if containsFold(remove, tag) {
			return fmt.Errorf("tag %q is both added and removed", tag)
		}
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// keepResultsWithTags drops the search results whose file does not carry every required tag. The SQL
// tag filter reads the index, which can disagree with the file: a stale row (a file edited by hand or
// brought by a sync, not yet reindexed) or an old tag with a space in it, which the space-joined index
// reads as two tags. The file is the source of truth.
func (fs *FileStore) keepResultsWithTags(results []SearchResult, required []string) []SearchResult {
	if len(required) == 0 {
		return results
	}
	kept := make([]SearchResult, 0, len(results))
	for _, r := range results {
		if tags, ok := fs.fileTags(r.ID); ok && HasAllTags(tags, required) {
			kept = append(kept, r)
		}
	}
	return kept
}

// fileTags returns the tags in the frontmatter of the entry's file; ok is false when the entry or its
// file cannot be read.
func (fs *FileStore) fileTags(id string) (tags []string, ok bool) {
	var kind, rel string
	if err := fs.db.QueryRow(`SELECT type, filepath FROM entries WHERE id = ?`, id).Scan(&kind, &rel); err != nil {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(fs.baseDir, rel))
	if err != nil {
		return nil, false
	}
	f, err := parseEntryFile(kind, data)
	if err != nil || f.id != id {
		return nil, false
	}
	return f.tags, true
}
