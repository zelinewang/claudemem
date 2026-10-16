package cmd

import (
	"strings"
	"testing"
)

// Empty note lists and searches print null in JSON, as they did before stores could be read
// together, with one store or several.
func TestCLI_EmptyNoteResultsPrintNull(t *testing.T) {
	single := t.TempDir()
	addNoteCLI(t, single, "ops", "A note", "nullkw", "x")
	s := newTwoStores(t)
	addNoteCLI(t, s.other, "ops", "Their note", "nullkw", "x")
	for _, store := range []string{single, s.primary} {
		for _, args := range [][]string{
			{"note", "list", "nosuchcategory", "--format", "json"},
			{"note", "search", "nohitsatall", "--format", "json"},
		} {
			if out := strings.TrimSpace(mustRunCLI(t, store, args...)); out != "null" {
				t.Errorf("%v printed %q, want null", args, out)
			}
		}
	}
}
