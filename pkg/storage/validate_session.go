package storage

import (
	"fmt"
	"strings"
)

// SessionValidationResult holds the result of validating a session markdown file
type SessionValidationResult struct {
	Valid  bool                    `json:"valid"`
	Checks []SessionValidationCheck `json:"checks"`
}

// SessionValidationCheck is a single validation check result
type SessionValidationCheck struct {
	Section string `json:"section"`
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
}

// ValidateSessionMarkdown validates a session markdown file against quality standards.
// All checks are mechanical — no AI judgment involved.
func ValidateSessionMarkdown(content string) *SessionValidationResult {
	result := &SessionValidationResult{Valid: true}

	// Parse sections from markdown
	sections := parseMarkdownSections(content)

	// 1. Summary: must exist and have >= 100 words
	result.addCheck(validateSectionMinWords(sections, "Summary", 100))

	// 2. What Happened: must exist and have >= 3 numbered phases
	result.addCheck(validateWhatHappenedPhases(sections, 3))

	// 3. Problems & Solutions: every problem must have a non-empty solution
	result.addCheck(validateProblemsHaveSolutions(sections))

	// 4. Required sections must exist (content can be minimal but not absent)
	for _, sec := range []string{"Learning Insights", "Next Steps"} {
		result.addCheck(validateSectionExists(sections, sec))
	}

	return result
}

func (r *SessionValidationResult) addCheck(c SessionValidationCheck) {
	r.Checks = append(r.Checks, c)
	if !c.Passed {
		r.Valid = false
	}
}

// parseMarkdownSections extracts ## sections from markdown content
func parseMarkdownSections(content string) map[string]string {
	sections := make(map[string]string)
	lines := strings.Split(content, "\n")
	currentSection := ""
	var currentContent []string

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			if currentSection != "" {
				sections[currentSection] = strings.TrimSpace(strings.Join(currentContent, "\n"))
			}
			currentSection = strings.TrimPrefix(line, "## ")
			currentContent = nil
		} else {
			currentContent = append(currentContent, line)
		}
	}
	if currentSection != "" {
		sections[currentSection] = strings.TrimSpace(strings.Join(currentContent, "\n"))
	}
	return sections
}

// validateSectionMinWords checks that a section exists and has at least minWords words
func validateSectionMinWords(sections map[string]string, name string, minWords int) SessionValidationCheck {
	content, exists := findSection(sections, name)
	if !exists || strings.TrimSpace(content) == "" {
		return SessionValidationCheck{
			Section: name,
			Passed:  false,
			Message: fmt.Sprintf("missing or empty (min: %d words)", minWords),
		}
	}
	wordCount := len(strings.Fields(content))
	if wordCount < minWords {
		return SessionValidationCheck{
			Section: name,
			Passed:  false,
			Message: fmt.Sprintf("%d words (min: %d)", wordCount, minWords),
		}
	}
	return SessionValidationCheck{
		Section: name,
		Passed:  true,
		Message: fmt.Sprintf("%d words", wordCount),
	}
}

// validateWhatHappenedPhases checks for numbered phases (1. 2. 3. etc.)
func validateWhatHappenedPhases(sections map[string]string, minPhases int) SessionValidationCheck {
	content, exists := findSection(sections, "What Happened")
	if !exists || strings.TrimSpace(content) == "" {
		return SessionValidationCheck{
			Section: "What Happened",
			Passed:  false,
			Message: fmt.Sprintf("missing or empty (min: %d phases)", minPhases),
		}
	}

	// Count numbered phases: lines starting with "N." or "N. **"
	phaseCount := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) >= 2 && trimmed[0] >= '1' && trimmed[0] <= '9' && trimmed[1] == '.' {
			phaseCount++
		}
	}

	if phaseCount < minPhases {
		return SessionValidationCheck{
			Section: "What Happened",
			Passed:  false,
			Message: fmt.Sprintf("%d phases (min: %d)", phaseCount, minPhases),
		}
	}
	return SessionValidationCheck{
		Section: "What Happened",
		Passed:  true,
		Message: fmt.Sprintf("%d phases", phaseCount),
	}
}

// validateProblemsHaveSolutions checks that every Problem has a non-empty Solution
func validateProblemsHaveSolutions(sections map[string]string) SessionValidationCheck {
	content, exists := findSection(sections, "Problems & Solutions")
	if !exists {
		// Section not present — OK if no problems encountered (skip check)
		return SessionValidationCheck{
			Section: "Problems & Solutions",
			Passed:  true,
			Message: "section not present (no problems reported)",
		}
	}

	if strings.TrimSpace(content) == "" {
		return SessionValidationCheck{
			Section: "Problems & Solutions",
			Passed:  true,
			Message: "empty (no problems reported)",
		}
	}

	// Only validate the structured template format: "- **Problem**: X\n  **Solution**: Y"
	// Free-form formats (e.g., "- **title**: description") are valid and not checked for structure.
	// We specifically catch the bug where **Solution**: exists but has empty content.
	lines := strings.Split(content, "\n")
	emptyCount := 0
	var emptyProblems []string

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])

		// Only check lines that explicitly use **Solution**: pattern
		if strings.Contains(line, "**Solution**:") || strings.Contains(line, "**Solution**:") {
			// Extract text after "**Solution**:"
			idx := strings.Index(line, "Solution**:")
			if idx >= 0 {
				after := strings.TrimSpace(line[idx+len("Solution**:"):])
				if after == "" {
					// Empty solution — find the problem it belongs to
					problemText := ""
					for j := i - 1; j >= 0 && j >= i-3; j-- {
						pLine := strings.TrimSpace(lines[j])
						if strings.Contains(pLine, "**Problem**:") || strings.Contains(pLine, "Problem:") {
							problemText = pLine
							break
						}
					}
					if len(problemText) > 60 {
						problemText = problemText[:60] + "..."
					}
					emptyCount++
					emptyProblems = append(emptyProblems, problemText)
				}
			}
		}
	}

	problemCount := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- **Problem**:") || strings.HasPrefix(trimmed, "- Problem:") {
			problemCount++
		}
	}

	if emptyCount > 0 {
		return SessionValidationCheck{
			Section: "Problems & Solutions",
			Passed:  false,
			Message: fmt.Sprintf("%d/%d problems have empty solutions: %s", emptyCount, problemCount, strings.Join(emptyProblems, "; ")),
		}
	}

	if problemCount > 0 {
		return SessionValidationCheck{
			Section: "Problems & Solutions",
			Passed:  true,
			Message: fmt.Sprintf("%d problems, all have solutions", problemCount),
		}
	}

	return SessionValidationCheck{
		Section: "Problems & Solutions",
		Passed:  true,
		Message: "no structured problems found",
	}
}

// validateSectionExists checks that a section exists (can be minimal content)
func validateSectionExists(sections map[string]string, name string) SessionValidationCheck {
	_, exists := findSection(sections, name)
	if !exists {
		return SessionValidationCheck{
			Section: name,
			Passed:  false,
			Message: "missing section",
		}
	}
	return SessionValidationCheck{
		Section: name,
		Passed:  true,
		Message: "present",
	}
}

// findSection does case-insensitive lookup with common aliases
func findSection(sections map[string]string, name string) (string, bool) {
	lower := strings.ToLower(name)
	for k, v := range sections {
		if strings.ToLower(k) == lower {
			return v, true
		}
	}
	// Check aliases
	aliases := map[string][]string{
		"learning insights": {"insights"},
		"next steps":        {"next steps", "follow-up", "follow up"},
		"what happened":     {"what happened"},
		"problems & solutions": {"problems and solutions", "problems"},
		"key decisions":     {"decisions"},
		"what changed":      {"changes", "files changed"},
	}
	for _, alias := range aliases[lower] {
		for k, v := range sections {
			if strings.ToLower(k) == alias {
				return v, true
			}
		}
	}
	return "", false
}
