package storage

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/zelinewang/claudemem/pkg/models"
	"gopkg.in/yaml.v3"
)

// FormatNoteMarkdown converts a note to markdown with YAML frontmatter
func FormatNoteMarkdown(note *models.Note) string {
	var buf bytes.Buffer

	// Write YAML frontmatter
	buf.WriteString("---\n")
	frontmatter := map[string]interface{}{
		"id":       note.ID,
		"type":     note.Type,
		"category": note.Category,
		"title":    note.Title,
		"tags":     note.Tags,
		"created":  note.Created.Format("2006-01-02T15:04:05Z"),
		"updated":  note.Updated.Format("2006-01-02T15:04:05Z"),
	}
	if len(note.Metadata) > 0 {
		frontmatter["metadata"] = note.Metadata
	}

	yamlData, _ := yaml.Marshal(frontmatter)
	buf.Write(yamlData)
	buf.WriteString("---\n\n")

	// Write content
	buf.WriteString(note.Content)

	return buf.String()
}

// ParseNoteMarkdown parses markdown with YAML frontmatter into a Note
func ParseNoteMarkdown(data []byte) (*models.Note, error) {
	content := string(data)

	// Ensure trailing newline for consistent parsing
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	// Check for frontmatter
	if !strings.HasPrefix(content, "---\n") {
		return nil, fmt.Errorf("missing YAML frontmatter")
	}

	// Find end of frontmatter
	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx == -1 {
		return nil, fmt.Errorf("invalid YAML frontmatter")
	}
	endIdx += 4

	// Parse YAML frontmatter
	frontmatterStr := content[4:endIdx]
	var frontmatter map[string]interface{}
	if err := yaml.Unmarshal([]byte(frontmatterStr), &frontmatter); err != nil {
		return nil, fmt.Errorf("failed to parse YAML frontmatter: %w", err)
	}

	// Create note from frontmatter
	note := &models.Note{
		Type:     "note",
		Tags:     []string{},
		Metadata: make(map[string]string),
	}

	// Parse fields
	if id, ok := frontmatter["id"].(string); ok {
		note.ID = id
	}
	if category, ok := frontmatter["category"].(string); ok {
		note.Category = category
	}
	if title, ok := frontmatter["title"].(string); ok {
		note.Title = title
	}
	if tags, ok := frontmatter["tags"].([]interface{}); ok {
		for _, tag := range tags {
			if t, ok := tag.(string); ok {
				note.Tags = append(note.Tags, t)
			}
		}
	}

	// Parse timestamps
	if created, ok := frontmatter["created"].(string); ok {
		note.Created, _ = parseTime(created)
	}
	if updated, ok := frontmatter["updated"].(string); ok {
		note.Updated, _ = parseTime(updated)
	}

	// Parse metadata
	if metadata, ok := frontmatter["metadata"].(map[string]interface{}); ok {
		for k, v := range metadata {
			if s, ok := v.(string); ok {
				note.Metadata[k] = s
			}
		}
	}

	// Extract content (after frontmatter)
	contentStart := endIdx + 5 // "\n---\n"
	if contentStart < len(content) {
		note.Content = strings.TrimSpace(content[contentStart:])
	}

	return note, nil
}

// FormatSessionMarkdown converts a session to markdown with YAML frontmatter
func FormatSessionMarkdown(session *models.Session) string {
	var buf bytes.Buffer

	// Write YAML frontmatter
	buf.WriteString("---\n")
	frontmatter := map[string]interface{}{
		"id":         session.ID,
		"type":       session.Type,
		"title":      session.Title,
		"date":       session.Date,
		"branch":     session.Branch,
		"project":    session.Project,
		"session_id": session.SessionID,
		"tags":       session.Tags,
		"created":    session.Created.Format("2006-01-02T15:04:05Z"),
	}

	yamlData, _ := yaml.Marshal(frontmatter)
	buf.Write(yamlData)
	buf.WriteString("---\n\n")

	// Write structured sections
	if session.Summary != "" {
		buf.WriteString("## Summary\n")
		buf.WriteString(session.Summary)
		buf.WriteString("\n\n")
	}

	if session.WhatHappened != "" {
		buf.WriteString("## What Happened\n")
		buf.WriteString(session.WhatHappened)
		buf.WriteString("\n\n")
	}

	if len(session.Decisions) > 0 {
		buf.WriteString("## Key Decisions\n")
		for _, decision := range session.Decisions {
			buf.WriteString(fmt.Sprintf("- %s\n", decision))
		}
		buf.WriteString("\n")
	}

	if len(session.Changes) > 0 {
		buf.WriteString("## What Changed\n")
		for _, change := range session.Changes {
			buf.WriteString(fmt.Sprintf("- `%s` — %s\n", change.Path, change.Description))
		}
		buf.WriteString("\n")
	}

	if len(session.Problems) > 0 {
		buf.WriteString("## Problems & Solutions\n")
		for _, ps := range session.Problems {
			writeMarkerBody(&buf, "- **Problem**: ", ps.Problem)
			writeMarkerBody(&buf, "  **Solution**: ", ps.Solution)
		}
		buf.WriteString("\n")
	}

	if len(session.Insights) > 0 {
		buf.WriteString("## Learning Insights\n")
		for _, insight := range session.Insights {
			buf.WriteString(fmt.Sprintf("- %s\n", insight))
		}
		buf.WriteString("\n")
	}

	if len(session.Questions) > 0 {
		buf.WriteString("## Questions Raised\n")
		for _, question := range session.Questions {
			buf.WriteString(fmt.Sprintf("- %s\n", question))
		}
		buf.WriteString("\n")
	}

	if len(session.NextSteps) > 0 {
		buf.WriteString("## Next Steps\n")
		for _, step := range session.NextSteps {
			buf.WriteString(fmt.Sprintf("- [ ] %s\n", step))
		}
		buf.WriteString("\n")
	}

	if len(session.RelatedNotes) > 0 {
		buf.WriteString("## Related Notes\n")
		for _, rn := range session.RelatedNotes {
			// Store full ID in markdown (not truncated) to preserve data integrity.
			// CLI display truncates to 8 chars for UX, but storage should be lossless.
			buf.WriteString(fmt.Sprintf("- `%s` — \"%s\" (%s)\n", rn.ID, rn.Title, rn.Category))
		}
		buf.WriteString("\n")
	}

	// Write any custom/extra sections (preserves user content that doesn't match known headers)
	for _, es := range session.ExtraSections {
		buf.WriteString(fmt.Sprintf("## %s\n", es.Name))
		buf.WriteString(es.Content)
		buf.WriteString("\n\n")
	}

	return strings.TrimRight(buf.String(), "\n")
}

// ParseSessionMarkdown parses markdown with YAML frontmatter into a Session
func ParseSessionMarkdown(data []byte) (*models.Session, error) {
	content := string(data)

	// Ensure trailing newline for consistent parsing
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	// Check for frontmatter
	if !strings.HasPrefix(content, "---\n") {
		return nil, fmt.Errorf("missing YAML frontmatter")
	}

	// Find end of frontmatter
	endIdx := strings.Index(content[4:], "\n---\n")
	if endIdx == -1 {
		return nil, fmt.Errorf("invalid YAML frontmatter")
	}
	endIdx += 4

	// Parse YAML frontmatter
	frontmatterStr := content[4:endIdx]
	var frontmatter map[string]interface{}
	if err := yaml.Unmarshal([]byte(frontmatterStr), &frontmatter); err != nil {
		return nil, fmt.Errorf("failed to parse YAML frontmatter: %w", err)
	}

	// Create session from frontmatter
	session := &models.Session{
		Type:          "session",
		Tags:          []string{},
		Decisions:     []string{},
		Changes:       []models.FileChange{},
		Problems:      []models.ProblemSolution{},
		Insights:      []string{},
		Questions:     []string{},
		NextSteps:     []string{},
		RelatedNotes:  []models.RelatedNote{},
		ExtraSections: []models.ExtraSection{},
	}

	// Parse fields
	if id, ok := frontmatter["id"].(string); ok {
		session.ID = id
	}
	if title, ok := frontmatter["title"].(string); ok {
		session.Title = title
	}
	if date, ok := frontmatter["date"].(string); ok {
		session.Date = date
	}
	if branch, ok := frontmatter["branch"].(string); ok {
		session.Branch = branch
	}
	if project, ok := frontmatter["project"].(string); ok {
		session.Project = project
	}
	if sessionID, ok := frontmatter["session_id"].(string); ok {
		session.SessionID = sessionID
	}
	if tags, ok := frontmatter["tags"].([]interface{}); ok {
		for _, tag := range tags {
			if t, ok := tag.(string); ok {
				session.Tags = append(session.Tags, t)
			}
		}
	}

	// Parse timestamp
	if created, ok := frontmatter["created"].(string); ok {
		session.Created, _ = parseTime(created)
	}

	// Parse body sections
	if contentStart := endIdx + 5; contentStart < len(content) {
		body := content[contentStart:]
		parseSessionBody(session, body)
	}

	return session, nil
}

// parseSessionBody extracts structured sections from the markdown body
func parseSessionBody(session *models.Session, body string) {
	sections := splitIntoSections(body)

	for sectionName, sectionContent := range sections {
		switch strings.ToLower(sectionName) {
		case "summary":
			session.Summary = strings.TrimSpace(sectionContent)

		case "key decisions", "decisions":
			for _, line := range parseListItems(sectionContent) {
				session.Decisions = append(session.Decisions, line)
			}

		case "what changed", "changes":
			for _, line := range parseListItems(sectionContent) {
				// Parse format: `path` — description
				parts := strings.SplitN(line, " — ", 2)
				if len(parts) == 2 {
					path := strings.Trim(parts[0], "`")
					session.Changes = append(session.Changes, models.FileChange{
						Path:        path,
						Description: parts[1],
					})
				}
			}

		case "problems & solutions", "problems and solutions":
			session.Problems = append(session.Problems, ParseProblemsSolutions(sectionContent)...)

		case "what happened":
			session.WhatHappened = strings.TrimSpace(sectionContent)

		case "learning insights", "insights":
			for _, line := range parseListItems(sectionContent) {
				session.Insights = append(session.Insights, line)
			}

		case "questions raised", "questions":
			for _, line := range parseListItems(sectionContent) {
				session.Questions = append(session.Questions, line)
			}

		case "next steps":
			for _, line := range parseListItems(sectionContent) {
				// Remove checkbox prefix if present
				line = strings.TrimPrefix(line, "[ ] ")
				line = strings.TrimPrefix(line, "[x] ")
				line = strings.TrimPrefix(line, "[X] ")
				session.NextSteps = append(session.NextSteps, line)
			}

		case "related notes":
			for _, line := range parseListItems(sectionContent) {
				rn := parseRelatedNoteLine(line)
				if rn.ID != "" {
					session.RelatedNotes = append(session.RelatedNotes, rn)
				}
			}

		default:
			// Preserve any section that doesn't match a known header.
			// This ensures custom content like "Architecture Diagram",
			// "Performance Map", "Files in Scope", etc. is never lost.
			trimmed := strings.TrimSpace(sectionContent)
			if trimmed != "" {
				session.ExtraSections = append(session.ExtraSections, models.ExtraSection{
					Name:    sectionName, // preserve original casing
					Content: trimmed,
				})
			}
		}
	}
}

// writeMarkerBody writes one "marker + body" line pair for the Problems &
// Solutions section. Continuation lines of a multi-line body are indented by
// two spaces so ParseProblemsSolutions re-attaches them to the same field,
// keeping the round-trip Parse(Format(s)) exact.
func writeMarkerBody(buf *bytes.Buffer, marker, body string) {
	lines := strings.Split(body, "\n")
	buf.WriteString(marker)
	buf.WriteString(lines[0])
	buf.WriteString("\n")
	for _, cont := range lines[1:] {
		buf.WriteString("  ")
		buf.WriteString(cont)
		buf.WriteString("\n")
	}
}

// problemBodyMarkers are the accepted Problem field openers, longest first.
// Both the canonical template (colon outside the bold) and the common
// bold-colon variant are recognized, plus the plain "Problem:" form.
var problemBodyMarkers = []string{"**Problem**: ", "**Problem:** ", "**Problem**:", "**Problem:**", "Problem: ", "Problem:"}

// solutionBodyMarkers mirrors problemBodyMarkers for the Solution field.
var solutionBodyMarkers = []string{"**Solution**: ", "**Solution:** ", "**Solution**:", "**Solution:**", "Solution: ", "Solution:"}

// stripMarker returns the body after the first matching marker prefix.
func stripMarker(text string, markers []string) (string, bool) {
	for _, m := range markers {
		if strings.HasPrefix(text, m) {
			return strings.TrimSpace(text[len(m):]), true
		}
	}
	return "", false
}

// joinBody appends a continuation line to a multi-line body.
func joinBody(existing, add string) string {
	if existing == "" {
		return add
	}
	if add == "" {
		return existing
	}
	return existing + "\n" + add
}

// ParseProblemsSolutions parses the body of a "Problems & Solutions" section
// into structured entries. It is the single parser used by both the CLI save
// path (cmd/session_save.go) and the stored-file path (ParseSessionMarkdown),
// replacing two earlier ad-hoc parsers that dropped canonical "**Solution**:"
// bodies and fused them into problem text.
//
// Grammar (state machine over lines):
//   - a bullet ("- " or "* ") opens a new entry; a Problem marker is stripped,
//     any other bullet text is kept verbatim as the problem body (backward
//     compatible with free-form entries);
//   - a Solution marker line (bulleted or indented continuation) switches the
//     current entry into its solution body;
//   - any other non-empty line continues the current body (problem or
//     solution), preserving multi-line bodies with "\n" joins.
//
// Fused historical lines ("- **Problem**: P **Solution**: S" on one physical
// line, produced by earlier corrupted versions) are deliberately NOT split:
// their full text is preserved in the Problem field so no information is
// destroyed on load.
func ParseProblemsSolutions(content string) []models.ProblemSolution {
	var result []models.ProblemSolution
	var cur *models.ProblemSolution
	inSolution := false

	flush := func() {
		if cur == nil {
			return
		}
		cur.Problem = strings.TrimSpace(cur.Problem)
		cur.Solution = strings.TrimSpace(cur.Solution)
		if cur.Problem != "" {
			result = append(result, *cur)
		}
		cur = nil
	}

	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		// Bullet line: new entry, bulleted solution, or free-form item.
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			text := strings.TrimSpace(line[2:])
			if body, ok := stripMarker(text, problemBodyMarkers); ok {
				flush()
				cur = &models.ProblemSolution{Problem: body}
				inSolution = false
				continue
			}
			if body, ok := stripMarker(text, solutionBodyMarkers); ok && cur != nil {
				cur.Solution = joinBody(cur.Solution, body)
				inSolution = true
				continue
			}
			flush()
			cur = &models.ProblemSolution{Problem: text}
			inSolution = false
			continue
		}

		// Continuation line: solution marker switch or body continuation.
		if body, ok := stripMarker(line, solutionBodyMarkers); ok && cur != nil {
			cur.Solution = joinBody(cur.Solution, body)
			inSolution = true
			continue
		}
		if cur == nil {
			continue // content before the first bullet is not a problem entry
		}
		if inSolution {
			cur.Solution = joinBody(cur.Solution, line)
		} else {
			cur.Problem = joinBody(cur.Problem, line)
		}
	}
	flush()
	return result
}

// parseRelatedNoteLine parses a line like: `id-prefix` — "Title" (category)
func parseRelatedNoteLine(line string) models.RelatedNote {
	rn := models.RelatedNote{}

	// Try to extract ID from backticks: `id-prefix`
	if idx := strings.Index(line, "`"); idx >= 0 {
		endIdx := strings.Index(line[idx+1:], "`")
		if endIdx >= 0 {
			rn.ID = line[idx+1 : idx+1+endIdx]
			line = line[idx+1+endIdx+1:] // rest after closing backtick
		}
	}

	// Try to extract title from quotes: "Title"
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "—")
	line = strings.TrimPrefix(line, "-")
	line = strings.TrimSpace(line)

	if idx := strings.Index(line, "\""); idx >= 0 {
		endIdx := strings.Index(line[idx+1:], "\"")
		if endIdx >= 0 {
			rn.Title = line[idx+1 : idx+1+endIdx]
			line = line[idx+1+endIdx+1:]
		}
	}

	// Try to extract category from parentheses: (category)
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "(") {
		endIdx := strings.Index(line, ")")
		if endIdx > 0 {
			rn.Category = line[1:endIdx]
		}
	}

	return rn
}

// splitIntoSections splits markdown body into sections by headers
func splitIntoSections(body string) map[string]string {
	sections := make(map[string]string)
	lines := strings.Split(body, "\n")

	currentSection := ""
	currentContent := []string{}

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			// Save previous section
			if currentSection != "" {
				sections[currentSection] = strings.Join(currentContent, "\n")
			}
			// Start new section
			currentSection = strings.TrimPrefix(line, "## ")
			currentContent = []string{}
		} else {
			currentContent = append(currentContent, line)
		}
	}

	// Save last section
	if currentSection != "" {
		sections[currentSection] = strings.Join(currentContent, "\n")
	}

	return sections
}

// parseListItems extracts list items from markdown content
func parseListItems(content string) []string {
	var items []string
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") {
			items = append(items, strings.TrimPrefix(trimmed, "- "))
		} else if strings.HasPrefix(trimmed, "* ") {
			items = append(items, strings.TrimPrefix(trimmed, "* "))
		} else if strings.HasPrefix(line, "  ") && len(items) > 0 {
			items[len(items)-1] += " " + trimmed
		}
	}

	return items
}

// parseTime attempts to parse various time formats
func parseTime(s string) (time.Time, error) {
	formats := []string{
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}

	for _, format := range formats {
		if parsed, err := time.Parse(format, s); err == nil {
			return parsed, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse time: %s", s)
}
