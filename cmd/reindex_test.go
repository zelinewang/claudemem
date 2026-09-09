package cmd

import (
	"strings"
	"testing"
)

// TestValidateReindexFlags (Codex P2 on PR #20): --missing is a modifier of
// vector indexing; alone it used to rebuild only FTS and exit 0, leaving
// every missing vector untouched behind a success message.
func TestValidateReindexFlags(t *testing.T) {
	cases := []struct {
		vectors, all, missing bool
		wantErr               bool
	}{
		{false, false, false, false},
		{true, false, false, false},
		{true, false, true, false},
		{false, true, true, false},
		{false, false, true, true},
	}
	for _, c := range cases {
		err := validateReindexFlags(c.vectors, c.all, c.missing)
		if (err != nil) != c.wantErr {
			t.Errorf("validateReindexFlags(vectors=%v all=%v missing=%v): err=%v, wantErr=%v", c.vectors, c.all, c.missing, err, c.wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), "--vectors") {
			t.Errorf("error should tell the user to add --vectors, got %q", err.Error())
		}
	}
}

// TestPartialBackfillError (Codex P1 on PR #20): a backfill that embedded
// fewer documents than were missing is a failure for automation, not a
// success with a smaller number.
func TestPartialBackfillError(t *testing.T) {
	if err := partialBackfillError(2, 2); err != nil {
		t.Fatalf("complete backfill must not error, got %v", err)
	}
	if err := partialBackfillError(0, 0); err != nil {
		t.Fatalf("nothing missing must not error, got %v", err)
	}
	err := partialBackfillError(1, 3)
	if err == nil || !strings.Contains(err.Error(), "2 of 3") {
		t.Fatalf("want an error naming '2 of 3' documents still missing, got %v", err)
	}
}
