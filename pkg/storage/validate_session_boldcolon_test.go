package storage

import (
	"strings"
	"testing"
)

// ─── Validator bold-colon marker visibility ─────────────────────────────────
//
// The session template's bold-colon variant writes solution fields as
// "**Solution:**" (colon inside the bold). The validator must treat those
// lines exactly like the canonical "**Solution**:" form: an empty body fails
// the Problems & Solutions check, a non-empty body satisfies it.

func TestValidate_BoldColonEmptySolution_Fails(t *testing.T) {
	content := `## Summary
` + longSummary() + `

## What Happened
1. **Phase one** — Did X.
2. **Phase two** — Did Y.
3. **Phase three** — Did Z.

## Problems & Solutions
- **Problem:** Something broke
  **Solution:**

## Learning Insights
- Insight here

## Next Steps
- [ ] Do next
`
	result := ValidateSessionMarkdown(content)
	for _, c := range result.Checks {
		if c.Section == "Problems & Solutions" && c.Passed {
			t.Errorf("bold-colon empty **Solution:** must fail the check, got pass: %s", c.Message)
		}
	}
}

func TestValidate_BoldColonSolutionWithText_Passes(t *testing.T) {
	content := `## Summary
` + longSummary() + `

## What Happened
1. **Phase one** — Did X.
2. **Phase two** — Did Y.
3. **Phase three** — Did Z.

## Problems & Solutions
- **Problem:** Bug A
  **Solution:** Fixed by doing X

## Learning Insights
- Useful insight

## Next Steps
- [ ] Follow up
`
	result := ValidateSessionMarkdown(content)
	for _, c := range result.Checks {
		if c.Section == "Problems & Solutions" && !c.Passed {
			t.Errorf("bold-colon **Solution:** with a body should pass, got fail: %s", c.Message)
		}
	}
}

// The bold-colon empty marker must also be detected when glued onto the end
// of a fused problem line (historical corruption residue shape).
func TestValidate_CanonicalEmptySolution_StillFails(t *testing.T) {
	content := `## Summary
` + longSummary() + `

## What Happened
1. **Phase one** — Did X.
2. **Phase two** — Did Y.
3. **Phase three** — Did Z.

## Problems & Solutions
- **Problem**: Something broke
  **Solution**:

## Learning Insights
- Insight here

## Next Steps
- [ ] Do next
`
	result := ValidateSessionMarkdown(content)
	if result.Valid {
		t.Error("expected overall invalid for canonical empty solution")
	}
	found := false
	for _, c := range result.Checks {
		if c.Section == "Problems & Solutions" && !c.Passed && strings.Contains(c.Message, "empty") {
			found = true
		}
	}
	if !found {
		t.Error("canonical empty **Solution**: must keep failing after the bold-colon fix")
	}
}
