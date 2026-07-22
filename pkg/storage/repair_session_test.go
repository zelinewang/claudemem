package storage

import (
	"strings"
	"testing"
)

// ─── session Problems & Solutions corruption repair (pure engine) ───────────
//
// RepairSessionMarkdown repairs the RECOVERABLE corruption left by the
// historical Solution-loss defect (fixed in ae4dff5):
//
//	R1 fused split:        "- **Problem**: P **Solution**: S" → canonical pair
//	R2 orphan consumption: stray empty "  **Solution**:" after a fused line
//	R3 glued-empty strip:  "- **Problem**: P **Solution**:" → keep P only
//	R4 bold-colon strip:   "**Problem:**"/"**Solution:**" residue in bodies
//	R5 untouchables:       mode-A (destroyed solutions) and healthy files are
//	                       byte-preserved; other sections are never modified
//	R6 idempotence:        a second repair pass changes nothing
//
// Fixture strings below mirror the oracle corpus byte-for-byte.

const repairFixtureA = `---
branch: fix
created: 2026-06-01T10:00:00Z
date: "2026-06-01"
id: fix-a
project: moa2-fixture
session_id: sid-fix-a
tags: [moa2]
title: fused with text
type: session
---

## Summary

Fixture fix-a for the repair-tool oracle.

## Problems & Solutions
- **Problem**: RPR-A1-PROB alpha fused **Solution**: RPR-A1-SOL beta recovered
  **Solution**:
- **Problem**: RPR-A2-PROB gamma fused no orphan **Solution**: RPR-A2-SOL delta
- **Problem**: RPR-A3-PROB clean pair
  **Solution**: RPR-A3-SOL already fine

## Next Steps

- [ ] RPR-A-NEXT untouched
`

const repairFixtureAExpected = `---
branch: fix
created: 2026-06-01T10:00:00Z
date: "2026-06-01"
id: fix-a
project: moa2-fixture
session_id: sid-fix-a
tags: [moa2]
title: fused with text
type: session
---

## Summary

Fixture fix-a for the repair-tool oracle.

## Problems & Solutions
- **Problem**: RPR-A1-PROB alpha fused
  **Solution**: RPR-A1-SOL beta recovered
- **Problem**: RPR-A2-PROB gamma fused no orphan
  **Solution**: RPR-A2-SOL delta
- **Problem**: RPR-A3-PROB clean pair
  **Solution**: RPR-A3-SOL already fine

## Next Steps

- [ ] RPR-A-NEXT untouched
`

const repairFixtureB = `---
branch: fix
created: 2026-06-01T10:00:00Z
date: "2026-06-01"
id: fix-b
project: moa2-fixture
session_id: sid-fix-b
tags: [moa2]
title: empty residue
type: session
---

## Summary

Fixture fix-b for the repair-tool oracle.

## Problems & Solutions
- **Problem**: RPR-B1-PROB kept text with glued empty marker **Solution**:
  **Solution**:
`

const repairFixtureBExpected = `---
branch: fix
created: 2026-06-01T10:00:00Z
date: "2026-06-01"
id: fix-b
project: moa2-fixture
session_id: sid-fix-b
tags: [moa2]
title: empty residue
type: session
---

## Summary

Fixture fix-b for the repair-tool oracle.

## Problems & Solutions
- **Problem**: RPR-B1-PROB kept text with glued empty marker
  **Solution**:
`

const repairFixtureC = `---
branch: fix
created: 2026-06-01T10:00:00Z
date: "2026-06-01"
id: fix-c
project: moa2-fixture
session_id: sid-fix-c
tags: [moa2]
title: bold colon residue
type: session
---

## Summary

Fixture fix-c for the repair-tool oracle.

## Problems & Solutions
- **Problem**: **Problem:** RPR-C1-PROB residue body
  **Solution**: **Solution:** RPR-C1-SOL residue body
`

const repairFixtureCExpected = `---
branch: fix
created: 2026-06-01T10:00:00Z
date: "2026-06-01"
id: fix-c
project: moa2-fixture
session_id: sid-fix-c
tags: [moa2]
title: bold colon residue
type: session
---

## Summary

Fixture fix-c for the repair-tool oracle.

## Problems & Solutions
- **Problem**: RPR-C1-PROB residue body
  **Solution**: RPR-C1-SOL residue body
`

const repairFixtureD = `---
branch: fix
created: 2026-06-01T10:00:00Z
date: "2026-06-01"
id: fix-d
project: moa2-fixture
session_id: sid-fix-d
tags: [moa2]
title: mode A untouchable
type: session
---

## Summary

Fixture fix-d for the repair-tool oracle.

## Problems & Solutions
- **Problem**: RPR-D1-PROB solution destroyed at save time
  **Solution**:
- **Problem**: RPR-D2-PROB also destroyed
  **Solution**:
`

const repairFixtureE = `---
branch: fix
created: 2026-06-01T10:00:00Z
date: "2026-06-01"
id: fix-e
project: moa2-fixture
session_id: sid-fix-e
tags: [moa2]
title: healthy
type: session
---

## Summary

Fixture fix-e for the repair-tool oracle.

## Problems & Solutions
- **Problem**: RPR-E1-PROB healthy
  **Solution**: RPR-E1-SOL healthy
`

const repairFixtureF = `---
branch: fix
created: 2026-06-01T10:00:00Z
date: "2026-06-01"
id: fix-f
project: moa2-fixture
session_id: sid-fix-f
tags: [moa2]
title: first delimiter edge
type: session
---

## Summary

Fixture fix-f for the repair-tool oracle.

## Problems & Solutions
- **Problem**: RPR-F1-PROB split here only **Solution**: RPR-F1-SOL first part **Solution**: RPR-F1-TRAIL stays inside solution
`

const repairFixtureFExpected = `---
branch: fix
created: 2026-06-01T10:00:00Z
date: "2026-06-01"
id: fix-f
project: moa2-fixture
session_id: sid-fix-f
tags: [moa2]
title: first delimiter edge
type: session
---

## Summary

Fixture fix-f for the repair-tool oracle.

## Problems & Solutions
- **Problem**: RPR-F1-PROB split here only
  **Solution**: RPR-F1-SOL first part **Solution**: RPR-F1-TRAIL stays inside solution
`

func TestRepair_FixtureRoundTrips(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
		changed  bool
	}{
		{"a fused+orphan+clean", repairFixtureA, repairFixtureAExpected, true},
		{"b glued-empty residue", repairFixtureB, repairFixtureBExpected, true},
		{"c bold-colon residue", repairFixtureC, repairFixtureCExpected, true},
		{"d mode-A untouchable", repairFixtureD, repairFixtureD, false},
		{"e healthy", repairFixtureE, repairFixtureE, false},
		{"f first-delimiter edge", repairFixtureF, repairFixtureFExpected, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repaired, changes := RepairSessionMarkdown(tc.input)
			if repaired != tc.expected {
				t.Errorf("repaired content mismatch\n--- got ---\n%s\n--- want ---\n%s", repaired, tc.expected)
			}
			if tc.changed && len(changes) == 0 {
				t.Error("expected changes to be reported, got none")
			}
			if !tc.changed && len(changes) != 0 {
				t.Errorf("expected no changes, got %d: %+v", len(changes), changes)
			}
			// R6: a second pass must be a no-op.
			again, againChanges := RepairSessionMarkdown(repaired)
			if again != repaired {
				t.Errorf("not idempotent: second pass changed content\n%s\n→\n%s", repaired, again)
			}
			if len(againChanges) != 0 {
				t.Errorf("not idempotent: second pass reported %d changes", len(againChanges))
			}
		})
	}
}

// R2: the stray empty solution marker is consumed ONLY when a fused split
// recovered solution content. A glued-empty strip (no recovered content)
// must leave the standalone marker in place (it becomes honest mode-A shape).
func TestRepair_OrphanConsumedOnlyAfterFusedSplit(t *testing.T) {
	input := `## Problems & Solutions
- **Problem**: P has solution **Solution**: S recovered
  **Solution**:
- **Problem**: P2 glued empty **Solution**:
  **Solution**:
`
	repaired, _ := RepairSessionMarkdown(input)
	expected := `## Problems & Solutions
- **Problem**: P has solution
  **Solution**: S recovered
- **Problem**: P2 glued empty
  **Solution**:
`
	if repaired != expected {
		t.Errorf("orphan consumption mismatch\n--- got ---\n%s\n--- want ---\n%s", repaired, expected)
	}
}

// R5: corruption-shaped lines outside "## Problems & Solutions" are never
// modified, even inside the same file.
func TestRepair_OutOfSectionUntouched(t *testing.T) {
	input := `## Summary

- **Problem**: quoted fused line **Solution**: must stay verbatim

## Problems & Solutions
- **Problem**: real fused **Solution**: recovered

## What Changed

- **Problem**: another quoted fused line **Solution**: also stays
`
	repaired, changes := RepairSessionMarkdown(input)
	expected := `## Summary

- **Problem**: quoted fused line **Solution**: must stay verbatim

## Problems & Solutions
- **Problem**: real fused
  **Solution**: recovered

## What Changed

- **Problem**: another quoted fused line **Solution**: also stays
`
	if repaired != expected {
		t.Errorf("out-of-section mismatch\n--- got ---\n%s\n--- want ---\n%s", repaired, expected)
	}
	if len(changes) != 1 {
		t.Errorf("expected exactly 1 change (in-section only), got %d", len(changes))
	}
}

// Section header variants ("## Problems", "## Problems / Solutions") are not
// the canonical section and must stay untouched.
func TestRepair_SectionHeaderVariantsUntouched(t *testing.T) {
	input := `## Problems
- **Problem**: fused **Solution**: stays
`
	repaired, changes := RepairSessionMarkdown(input)
	if repaired != input {
		t.Errorf("variant section was modified:\n%s", repaired)
	}
	if len(changes) != 0 {
		t.Errorf("expected no changes for variant section, got %d", len(changes))
	}
}

// Real-corpus shape (2026-06-27 reelcraft session): a canonical problem field
// whose body is a whole bold-colon pair — "**Problem:** P **Solution:** S".
func TestRepair_DoubleBoldColonFused(t *testing.T) {
	input := `## Problems & Solutions
- **Problem**: **Problem:** Seedance 2.0 returned E005 on photoreal clips. **Solution:** the agent fell back to open-weights Wan.
`
	repaired, _ := RepairSessionMarkdown(input)
	expected := `## Problems & Solutions
- **Problem**: Seedance 2.0 returned E005 on photoreal clips.
  **Solution**: the agent fell back to open-weights Wan.
`
	if repaired != expected {
		t.Errorf("double bold-colon fused mismatch\n--- got ---\n%s\n--- want ---\n%s", repaired, expected)
	}
}

// Real-corpus shape (2026-04-24 config-ssot session): a canonical solution
// field whose body wraps a canonical problem field and ends with a glued
// empty marker. The trailing empty marker is residue and is stripped; the
// wrapped problem text is content and is preserved.
func TestRepair_SolutionLineWrappedGluedEmpty(t *testing.T) {
	input := `## Problems & Solutions
- **Problem**: USE_BEDROCK=1 caused 400 error. **Solution**:
  **Solution**: **Problem**: DISABLE_ADAPTIVE is contradictory. Solution: delete it. **Solution**:
`
	repaired, _ := RepairSessionMarkdown(input)
	expected := `## Problems & Solutions
- **Problem**: USE_BEDROCK=1 caused 400 error.
  **Solution**: **Problem**: DISABLE_ADAPTIVE is contradictory. Solution: delete it.
`
	if repaired != expected {
		t.Errorf("wrapped glued-empty mismatch\n--- got ---\n%s\n--- want ---\n%s", repaired, expected)
	}
}

// False-positive guard: a solution body that merely ENDS with the literal
// text "**Solution**:" (e.g. prose documenting the marker) without the
// wrapped-problem signature is NOT stripped.
func TestRepair_SolutionLineTrailingTokenProse_Kept(t *testing.T) {
	input := `## Problems & Solutions
- **Problem**: validator missed bold lines
  **Solution**: grep for the literal **Solution**: token
- **Problem**: second
  **Solution**: prose ending with **Solution**:
`
	repaired, changes := RepairSessionMarkdown(input)
	if repaired != input {
		t.Errorf("prose solution bodies must be preserved:\n%s", repaired)
	}
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %d", len(changes))
	}
}

// Change records carry original 1-based line numbers and before/after text
// so the CLI can render an auditable dry-run report.
func TestRepair_ChangeRecords(t *testing.T) {
	repaired, changes := RepairSessionMarkdown(repairFixtureA)
	_ = repaired
	if len(changes) != 3 {
		t.Fatalf("expected 3 changes (2 fused splits + 1 orphan), got %d: %+v", len(changes), changes)
	}
	var split, orphan *RepairChange
	for i := range changes {
		switch changes[i].Action {
		case RepairActionFusedSplit:
			if split == nil {
				split = &changes[i]
			}
		case RepairActionOrphanConsumed:
			orphan = &changes[i]
		}
	}
	if split == nil || orphan == nil {
		t.Fatalf("missing expected actions in %+v", changes)
	}
	if split.Line != 18 {
		t.Errorf("fused split should be reported at original line 18, got %d", split.Line)
	}
	if !strings.Contains(split.Before, "RPR-A1-PROB alpha fused **Solution**: RPR-A1-SOL") {
		t.Errorf("split.Before should contain the original fused line, got %q", split.Before)
	}
	if len(split.After) != 2 || !strings.Contains(split.After[1], "**Solution**: RPR-A1-SOL beta recovered") {
		t.Errorf("split.After should be the canonical two-line pair, got %+v", split.After)
	}
	if orphan.Line != 19 {
		t.Errorf("consumed orphan should be reported at original line 19, got %d", orphan.Line)
	}
	if len(orphan.After) != 0 {
		t.Errorf("consumed orphan should have empty After, got %+v", orphan.After)
	}
}
