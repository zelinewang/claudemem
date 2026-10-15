package storage

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Tags are the labels a filtered view selects on. One store can serve a second profile whose wrapper
// shows only the entries tagged vis-shared, so a tag filter is a visibility boundary, not just a
// search facet.
//
// A filter is a list of required tags: an entry passes when it carries every one of them as a
// whole tag, compared case-insensitively (the rule noteHasAllTags always used). The index keeps an
// entry's tags as one space-joined string (entries.tags), so the SQL side matches whole
// space-delimited tokens. The substring LIKE used before let "vis-shared" match "vis-shared-old".

var (
	// ErrEntryNotFound reports that no note or session has the given id.
	ErrEntryNotFound = errors.New("entry not found")
	// ErrWrongKind reports that the id exists but belongs to the other kind (a note id given as a session).
	ErrWrongKind = errors.New("entry is of the other kind")
)

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

// SharedTag is the visibility label of the shared part of a store: a filtered view built on it sees
// only the entries carrying it. Every other entry, labelled vis-personal or not labelled at all, counts
// as not shared. A write never merges an entry into one on the other side of this label, or a merge
// would copy personal text into a shared entry, or label a personal entry shared.
const SharedTag = "vis-shared"

// isShared reports whether tags carry SharedTag.
func isShared(tags []string) bool {
	return HasAllTags(tags, []string{SharedTag})
}

// labelWhere returns a WHERE fragment, starting with " AND", that keeps only the rows on the same
// side of SharedTag as tags: the rows carrying it when tags carry it, the others when they do not.
func labelWhere(column string, tags []string) (string, []interface{}) {
	cond := "(' ' || " + column + " || ' ') LIKE ? ESCAPE '\\'"
	if !isShared(tags) {
		cond = "NOT " + cond
	}
	return " AND " + cond, []interface{}{"% " + likeEscaper.Replace(SharedTag) + " %"}
}

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
		if strings.IndexFunc(tag, func(r rune) bool { return unicode.IsSpace(r) || r == ',' || r == 0 }) >= 0 {
			return fmt.Errorf("tag %q must be a single word: no spaces or commas", tag)
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
