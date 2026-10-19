package storage

import (
	"strings"
	"testing"
)

func validReportWith(whatChanged string) string {
	summary := strings.Repeat("word ", 120)
	return "## Summary\n" + summary + "\n\n" +
		"## What Happened\n1. **One** — first\n2. **Two** — second\n3. **Three** — third\n\n" +
		whatChanged +
		"## Learning Insights\n- insight\n\n## Next Steps\n- [ ] next\n"
}

func findCheck(result *SessionValidationResult, section string) *SessionValidationCheck {
	for i := range result.Checks {
		if result.Checks[i].Section == section {
			return &result.Checks[i]
		}
	}
	return nil
}

func TestValidateSessionMarkdown_ReportsWhatChangedItems(t *testing.T) {
	report := validReportWith("## What Changed\n" +
		"- `pkg/a.go` — canonical one\n" +
		"- `pkg/b.go`: backticked path with a colon\n" +
		"- `pkg/c.go` — canonical two\n" +
		"- free text without a path\n" +
		"- pkg/d.go: bare path with a colon\n\n")

	result := ValidateSessionMarkdown(report)
	check := findCheck(result, "What Changed")
	if check == nil {
		t.Fatalf("no What Changed check in %+v", result.Checks)
	}
	if !check.Passed || !result.Valid {
		t.Errorf("items without a path are kept, so the report stays valid: check %+v, valid %v", check, result.Valid)
	}
	if !strings.Contains(check.Message, "5 items") || !strings.Contains(check.Message, "2 stored as text") {
		t.Errorf("message = %q, want the item count and the 2 items stored without a path", check.Message)
	}
}

func TestValidateSessionMarkdown_WhatChangedAllInTemplateForm(t *testing.T) {
	report := validReportWith("## What Changed\n- `pkg/a.go` — one\n- `pkg/b.go` — two\n\n")

	check := findCheck(ValidateSessionMarkdown(report), "What Changed")
	if check == nil || !check.Passed || check.Message != "2 items" {
		t.Errorf("check = %+v, want passed with message '2 items'", check)
	}
}

func TestValidateSessionMarkdown_WhatChangedAbsent(t *testing.T) {
	check := findCheck(ValidateSessionMarkdown(validReportWith("")), "What Changed")
	if check == nil || !check.Passed {
		t.Errorf("check = %+v, want a passed check (the section is optional)", check)
	}
}
