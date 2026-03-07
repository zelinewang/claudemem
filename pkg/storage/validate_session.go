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

	// Parse problems and solutions
	lines := strings.Split(content, "\n")
	problemCount := 0
	emptyCount := 0
	var emptyProblems []string

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])

		// Detect problem lines
		if strings.HasPrefix(line, "- **Problem**:") || strings.HasPrefix(line, "- Problem:") {
			problemCount++
			problemText := strings.TrimPrefix(line, "- **Problem**: ")
			problemText = strings.TrimPrefix(problemText, "- Problem: ")

			// Look for the solution on next line(s)
			solutionFound := false
			for j := i + 1; j < len(lines) && j <= i+3; j++ {
				sLine := strings.TrimSpace(lines[j])
				if strings.Contains(sLine, "Solution") && strings.Contains(sLine, ":") {
					// Extract solution text after ":"
					parts := strings.SplitN(sLine, ":", 2)
					if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
						solutionFound = true
					}
					break
				}
				// If we hit another Problem or section header, stop looking
				if strings.HasPrefix(sLine, "- **Problem**") || strings.HasPrefix(sLine, "## ") {
					break
				}
			}

			if !solutionFound {
				emptyCount++
				if len(problemText) > 50 {
					problemText = problemText[:50] + "..."
				}
				emptyProblems = append(emptyProblems, problemText)
			}
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
