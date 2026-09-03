package cmd

import (
	"strings"
	"testing"

	"github.com/zelinewang/claudemem/pkg/storage"
)

// Review of PR #21 (N3 / R3-4): a shared-files-only result must not send the reader to `repair`,
// which by design ignores shared files; index issues still do.
func TestVerifyAdvice(t *testing.T) {
	shared := &storage.VerifyResult{EntryCount: 2, FTSCount: 2,
		SharedFiles: []storage.SharedFile{{Path: "notes/x/a.md", Entries: 2}}}
	adv := verifyAdvice(shared)
	if len(adv) != 1 || strings.Contains(adv[0], "repair") || !strings.Contains(adv[0], "Shared files need a human") {
		t.Fatalf("shared-files-only advice wrong: %q", adv)
	}

	orphan := &storage.VerifyResult{EntryCount: 3, FTSCount: 3,
		OrphanedEntries: []storage.OrphanEntry{{ID: "abcdef12", Type: "note", Path: "notes/x/gone.md"}}}
	adv = verifyAdvice(orphan)
	if len(adv) != 1 || !strings.Contains(adv[0], "claudemem repair") {
		t.Fatalf("orphan advice wrong: %q", adv)
	}

	drift := &storage.VerifyResult{EntryCount: 3, FTSCount: 2}
	if adv = verifyAdvice(drift); len(adv) != 1 || !strings.Contains(adv[0], "claudemem repair") {
		t.Fatalf("FTS-drift advice wrong: %q", adv)
	}

	both := &storage.VerifyResult{EntryCount: 3, FTSCount: 2,
		SharedFiles: []storage.SharedFile{{Path: "notes/x/a.md", Entries: 2}}}
	if adv = verifyAdvice(both); len(adv) != 2 {
		t.Fatalf("both kinds of issue should give two lines: %q", adv)
	}

	clean := &storage.VerifyResult{EntryCount: 3, FTSCount: 3, InSync: true}
	if adv = verifyAdvice(clean); len(adv) != 0 {
		t.Fatalf("a clean result should give no advice: %q", adv)
	}
}
