package storage

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// TagEdit is one line of a `claudemem tag apply` file.
type TagEdit struct {
	Line   int      `json:"-"`
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	Add    []string `json:"add"`
	Remove []string `json:"remove"`
}

// maxReportedProblems caps how many bad lines ParseTagEdits names; the count is always exact.
const maxReportedProblems = 20

// ParseTagEdits reads a tag-edit file: one JSON object per line,
// {"id": "...", "kind": "note"|"session", "add": [...], "remove": [...]}; blank lines are skipped.
// The whole file is checked before anything is applied: an unknown field, a missing id, a bad kind,
// an invalid tag, a tag both added and removed, or an entry listed twice fails the file and names
// the bad lines, so a broken generator writes nothing. Ids are exact, never prefixes.
func ParseTagEdits(r io.Reader) ([]TagEdit, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var (
		edits    []TagEdit
		problems []string
		seen     = map[string]int{}
		line     int
	)
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		e, err := parseTagEditLine(text)
		if err != nil {
			problems = append(problems, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		e.Line = line
		key := e.Kind + " " + e.ID
		if first, dup := seen[key]; dup {
			problems = append(problems, fmt.Sprintf("line %d: %s %s is already on line %d; put all its changes on one line", line, e.Kind, e.ID, first))
			continue
		}
		seen[key] = line
		edits = append(edits, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read tag edits: %w", err)
	}
	if len(problems) > 0 {
		shown := problems
		if len(shown) > maxReportedProblems {
			shown = append(shown[:maxReportedProblems:maxReportedProblems], fmt.Sprintf("... and %d more", len(problems)-maxReportedProblems))
		}
		return nil, fmt.Errorf("%d invalid lines, nothing applied:\n  %s", len(problems), strings.Join(shown, "\n  "))
	}
	return edits, nil
}

func parseTagEditLine(text string) (TagEdit, error) {
	var e TagEdit
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return e, err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return e, fmt.Errorf("one JSON object per line")
	}
	if e.ID == "" || strings.IndexFunc(e.ID, unicode.IsSpace) >= 0 {
		return e, fmt.Errorf("id must be a full id without spaces")
	}
	if e.Kind != "note" && e.Kind != "session" {
		return e, fmt.Errorf("kind must be note or session, got %q", e.Kind)
	}
	if err := ValidateTagEdit(e.Add, e.Remove); err != nil {
		return e, err
	}
	return e, nil
}

// TagApplyIssue names an entry that ApplyTagEdits could not apply.
type TagApplyIssue struct {
	Line   int    `json:"line"`
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// TagApplyReport counts what ApplyTagEdits did (or, in a dry run, would do).
type TagApplyReport struct {
	DryRun          bool            `json:"dry_run"`
	Entries         int             `json:"entries"`
	Changed         int             `json:"changed"`
	NotesChanged    int             `json:"notes_changed"`
	SessionsChanged int             `json:"sessions_changed"`
	Unchanged       int             `json:"unchanged"`
	Unknown         []TagApplyIssue `json:"unknown"`
	Failed          []TagApplyIssue `json:"failed"`
}

// ApplyTagEdits applies each edit with EditTags: one entry at a time, each atomic on its own and
// under the store lock, so a writer that runs meanwhile is never lost and an interrupted run leaves
// every entry either done or untouched. An unknown id or a failed entry is reported and the run goes
// on; running the same edits again changes nothing. progress, when set, is called after each entry.
func (fs *FileStore) ApplyTagEdits(edits []TagEdit, dryRun bool, progress func(done, total int)) *TagApplyReport {
	report := &TagApplyReport{DryRun: dryRun, Entries: len(edits), Unknown: []TagApplyIssue{}, Failed: []TagApplyIssue{}}
	var rowIDs map[string]int64
	if !dryRun {
		rowIDs = fs.ftsRowIDs() // one FTS scan for the run instead of one per entry
	}
	for i, e := range edits {
		res, err := fs.editTagsLocking(e.Kind, e.ID, e.Add, e.Remove, dryRun, rowIDs[e.ID])
		switch {
		case errors.Is(err, ErrEntryNotFound), errors.Is(err, ErrWrongKind):
			report.Unknown = append(report.Unknown, TagApplyIssue{Line: e.Line, ID: e.ID, Kind: e.Kind, Reason: err.Error()})
		case err != nil:
			report.Failed = append(report.Failed, TagApplyIssue{Line: e.Line, ID: e.ID, Kind: e.Kind, Reason: err.Error()})
		case res.Changed:
			report.Changed++
			if e.Kind == "note" {
				report.NotesChanged++
			} else {
				report.SessionsChanged++
			}
		default:
			report.Unchanged++
		}
		if progress != nil {
			progress(i+1, len(edits))
		}
	}
	return report
}
