package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/models"
)

var (
	sessionTitle        string
	sessionBranch       string
	sessionProject      string
	sessionSessionID    string
	sessionTags         string
	sessionContent      string
	sessionSummary      string
	sessionWhatHappened string
	sessionDecisions    []string
	sessionChanges      []string
	sessionProblems     []string
	sessionInsights     []string
	sessionQuestions    []string
	sessionNextSteps   []string
	sessionRelatedNotes []string
)

var sessionSaveCmd = &cobra.Command{
	Use:   "save",
	Short: "Save a new session summary",
	Long: `Save a Claude conversation session summary.

The session content can be provided in multiple ways:
1. Via --content flag with full markdown body
2. Via structured flags (--summary, --decisions, etc.)
3. Via stdin (pipe full markdown)

Example with full markdown:
  claudemem session save --title "Fix auth bug" --branch "main" \
    --project "/home/user/myapp" --session-id "abc123" \
    --content "## Summary\nFixed authentication issue..."

Example with structured input:
  claudemem session save --title "Fix auth bug" --branch "main" \
    --project "/home/user/myapp" --session-id "abc123" \
    --summary "Fixed the JWT token refresh logic" \
    --decisions "Use Redis for token storage" \
    --decisions "Add 5-minute grace period"

Example with stdin:
  echo "## Summary\nFixed auth..." | claudemem session save \
    --title "Fix auth bug" --branch "main" \
    --project "/home/user/myapp" --session-id "abc123"`,
	RunE: runSessionSave,
}

func init() {
	sessionCmd.AddCommand(sessionSaveCmd)

	sessionSaveCmd.Flags().StringVar(&sessionTitle, "title", "", "Session title (required)")
	sessionSaveCmd.Flags().StringVar(&sessionBranch, "branch", "", "Git branch (required)")
	sessionSaveCmd.Flags().StringVar(&sessionProject, "project", "", "Project path (required)")
	sessionSaveCmd.Flags().StringVar(&sessionSessionID, "session-id", "", "Claude session ID (required)")
	sessionSaveCmd.Flags().StringVar(&sessionTags, "tags", "", "Comma-separated tags")
	sessionSaveCmd.Flags().StringVar(&sessionContent, "content", "", "Full markdown body")
	sessionSaveCmd.Flags().StringVar(&sessionSummary, "summary", "", "Session summary")
	sessionSaveCmd.Flags().StringSliceVar(&sessionDecisions, "decisions", []string{}, "Key decisions (can be used multiple times)")
	sessionSaveCmd.Flags().StringSliceVar(&sessionChanges, "changes", []string{}, "File changes in format 'path:description'")
	sessionSaveCmd.Flags().StringSliceVar(&sessionProblems, "problems", []string{}, "Problems in format 'problem:solution'")
	sessionSaveCmd.Flags().StringSliceVar(&sessionInsights, "insights", []string{}, "Learning insights")
	sessionSaveCmd.Flags().StringSliceVar(&sessionQuestions, "questions", []string{}, "Questions raised")
	sessionSaveCmd.Flags().StringSliceVar(&sessionNextSteps, "next-steps", []string{}, "Next steps")
	sessionSaveCmd.Flags().StringVar(&sessionWhatHappened, "what-happened", "", "What happened narrative")
	sessionSaveCmd.Flags().StringSliceVar(&sessionRelatedNotes, "related-notes", []string{}, "Related note refs in format 'id:title:category'")

	sessionSaveCmd.MarkFlagRequired("title")
	sessionSaveCmd.MarkFlagRequired("branch")
	sessionSaveCmd.MarkFlagRequired("project")
	sessionSaveCmd.MarkFlagRequired("session-id")
}

func runSessionSave(cmd *cobra.Command, args []string) error {
	store, err := getSessionStore()
	if err != nil {
		return err
	}
	defer store.Close()

	// Create new session
	session := models.NewSession(sessionTitle, sessionBranch, sessionProject, sessionSessionID)

	// Parse tags
	if sessionTags != "" {
		session.Tags = strings.Split(sessionTags, ",")
		for i := range session.Tags {
			session.Tags[i] = strings.TrimSpace(session.Tags[i])
		}
	}

	// Determine content source
	var bodyContent string

	if sessionContent != "" {
		// Content provided via flag
		bodyContent = sessionContent
	} else if sessionSummary != "" || sessionWhatHappened != "" || len(sessionDecisions) > 0 || len(sessionChanges) > 0 ||
		len(sessionProblems) > 0 || len(sessionInsights) > 0 || len(sessionQuestions) > 0 || len(sessionNextSteps) > 0 {
		// Structured input provided
		session.Summary = sessionSummary
		session.WhatHappened = sessionWhatHappened
		session.Decisions = sessionDecisions
		session.Insights = sessionInsights
		session.Questions = sessionQuestions
		session.NextSteps = sessionNextSteps

		// Parse changes (format: path:description)
		for _, change := range sessionChanges {
			parts := strings.SplitN(change, ":", 2)
			if len(parts) == 2 {
				session.Changes = append(session.Changes, models.FileChange{
					Path:        parts[0],
					Description: parts[1],
				})
			}
		}

		// Parse problems (format: problem:solution)
		for _, problem := range sessionProblems {
			parts := strings.SplitN(problem, ":", 2)
			ps := models.ProblemSolution{Problem: parts[0]}
			if len(parts) == 2 {
				ps.Solution = parts[1]
			}
			session.Problems = append(session.Problems, ps)
		}
	} else {
		// Try to read from stdin
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			// Data is being piped
			reader := bufio.NewReader(os.Stdin)
			data, err := io.ReadAll(reader)
			if err != nil {
				return fmt.Errorf("failed to read from stdin: %w", err)
			}
			bodyContent = string(data)
		} else {
			return fmt.Errorf("no content provided (use --content, structured flags, or pipe to stdin)")
		}
	}

	// If body content was provided, parse sections from it directly
	if bodyContent != "" {
		// Check if content has ## section headers — parse as structured markdown
		if strings.Contains(bodyContent, "## ") {
			// Parse section headers directly without wrapping in frontmatter
			sections := make(map[string]string)
			lines := strings.Split(bodyContent, "\n")
			currentSection := ""
			var currentContent []string

			for _, line := range lines {
				if strings.HasPrefix(line, "## ") {
					if currentSection != "" {
						sections[currentSection] = strings.Join(currentContent, "\n")
					}
					currentSection = strings.TrimPrefix(line, "## ")
					currentContent = nil
				} else {
					currentContent = append(currentContent, line)
				}
			}
			if currentSection != "" {
				sections[currentSection] = strings.Join(currentContent, "\n")
			}

			// Map sections to session fields
			for name, content := range sections {
				trimmed := strings.TrimSpace(content)
				switch strings.ToLower(name) {
				case "summary":
					session.Summary = trimmed
				case "what happened":
					session.WhatHappened = trimmed
				case "key decisions", "decisions":
					for _, line := range strings.Split(trimmed, "\n") {
						line = strings.TrimSpace(line)
						if strings.HasPrefix(line, "- ") {
							session.Decisions = append(session.Decisions, strings.TrimPrefix(line, "- "))
						}
					}
				case "what changed", "changes":
					for _, line := range strings.Split(trimmed, "\n") {
						line = strings.TrimSpace(line)
						if strings.HasPrefix(line, "- ") {
							item := strings.TrimPrefix(line, "- ")
							parts := strings.SplitN(item, " — ", 2)
							if len(parts) == 2 {
								session.Changes = append(session.Changes, models.FileChange{
									Path:        strings.Trim(parts[0], "`"),
									Description: parts[1],
								})
							}
						}
					}
				case "problems & solutions", "problems and solutions":
					lines := strings.Split(trimmed, "\n")
					for i := 0; i < len(lines); i++ {
						line := strings.TrimSpace(lines[i])
						if !strings.HasPrefix(line, "- ") {
							continue
						}
						item := strings.TrimPrefix(line, "- ")
						problem := strings.TrimPrefix(item, "**Problem**: ")
						problem = strings.TrimPrefix(problem, "Problem: ")
						solution := ""
						if i+1 < len(lines) {
							next := strings.TrimSpace(lines[i+1])
							if strings.Contains(next, "Solution:") {
								solution = strings.TrimPrefix(next, "  **Solution**: ")
								solution = strings.TrimPrefix(solution, "  Solution: ")
								solution = strings.TrimPrefix(solution, "**Solution**: ")
								solution = strings.TrimPrefix(solution, "Solution: ")
								i++
							}
						}
						if problem != "" {
							session.Problems = append(session.Problems, models.ProblemSolution{
								Problem:  problem,
								Solution: solution,
							})
						}
					}
				case "learning insights", "insights":
					for _, line := range strings.Split(trimmed, "\n") {
						line = strings.TrimSpace(line)
						if strings.HasPrefix(line, "- ") {
							session.Insights = append(session.Insights, strings.TrimPrefix(line, "- "))
						}
					}
				case "next steps":
					for _, line := range strings.Split(trimmed, "\n") {
						line = strings.TrimSpace(line)
						if strings.HasPrefix(line, "- ") {
							step := strings.TrimPrefix(line, "- ")
							step = strings.TrimPrefix(step, "[ ] ")
							step = strings.TrimPrefix(step, "[x] ")
							session.NextSteps = append(session.NextSteps, step)
						}
					}
				case "questions raised", "questions":
					for _, line := range strings.Split(trimmed, "\n") {
						line = strings.TrimSpace(line)
						if strings.HasPrefix(line, "- ") {
							session.Questions = append(session.Questions, strings.TrimPrefix(line, "- "))
						}
					}
				case "related notes":
					for _, line := range strings.Split(trimmed, "\n") {
						line = strings.TrimSpace(line)
						if strings.HasPrefix(line, "- ") {
							item := strings.TrimPrefix(line, "- ")
							// Parse format: `id` — "title" (category)
							rn := models.RelatedNote{}
							if idx := strings.Index(item, "`"); idx >= 0 {
								endIdx := strings.Index(item[idx+1:], "`")
								if endIdx >= 0 {
									rn.ID = item[idx+1 : idx+1+endIdx]
								}
							}
							if idx := strings.Index(item, "\""); idx >= 0 {
								endIdx := strings.Index(item[idx+1:], "\"")
								if endIdx >= 0 {
									rn.Title = item[idx+1 : idx+1+endIdx]
								}
							}
							if idx := strings.LastIndex(item, "("); idx >= 0 {
								endIdx := strings.Index(item[idx:], ")")
								if endIdx > 0 {
									rn.Category = item[idx+1 : idx+endIdx]
								}
							}
							if rn.ID != "" {
								session.RelatedNotes = append(session.RelatedNotes, rn)
							}
						}
					}
				default:
					// Preserve custom sections not in the predefined set
					if trimmed != "" {
						session.ExtraSections = append(session.ExtraSections, models.ExtraSection{
							Name:    name, // preserve original casing
							Content: trimmed,
						})
					}
				}
			}
		} else {
			// No section headers — treat entire content as summary
			session.Summary = strings.TrimSpace(bodyContent)
		}
	}

	// Parse related notes from flag (format: "id:title:category")
	for _, rn := range sessionRelatedNotes {
		parts := strings.SplitN(rn, ":", 3)
		if len(parts) < 3 || parts[0] == "" {
			fmt.Fprintf(os.Stderr, "Warning: --related-notes entry %q should be in format 'id:title:category', skipping\n", rn)
			continue
		}
		note := models.RelatedNote{
			ID:       parts[0],
			Title:    parts[1],
			Category: parts[2],
		}
		session.RelatedNotes = append(session.RelatedNotes, note)
	}

	// Auto-discover notes linked to this session via session_id in DB
	if sessionSessionID != "" {
		discoveredNotes, discoverErr := store.FindNotesBySessionRef(sessionSessionID)
		if discoverErr == nil && len(discoveredNotes) > 0 {
			session.RelatedNotes = append(session.RelatedNotes, discoveredNotes...)
			fmt.Fprintf(os.Stderr, "Auto-discovered %d related notes\n", len(discoveredNotes))
		}
	}

	// Deduplicate RelatedNotes by ID (content parsing, flag, and auto-discovery may all provide them)
	if len(session.RelatedNotes) > 0 {
		seen := make(map[string]bool)
		var deduped []models.RelatedNote
		for _, rn := range session.RelatedNotes {
			if !seen[rn.ID] {
				seen[rn.ID] = true
				deduped = append(deduped, rn)
			}
		}
		session.RelatedNotes = deduped
	}

	// Save session (auto-dedup: same date+project+branch → update)
	result, err := store.SaveSession(session)
	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	// Output result
	idPrefix := result.SessionID
	if len(idPrefix) > 8 {
		idPrefix = idPrefix[:8]
	}

	if outputFormat == "json" {
		return OutputJSON(result)
	}

	if result.Action == "updated" {
		OutputText("📎 Updated existing session: \"%s\" (%s, %s) [id: %s]",
			result.Title, result.Date, session.Branch, idPrefix)
	} else {
		OutputText("✓ Saved session: \"%s\" (%s, %s) [id: %s]",
			result.Title, result.Date, session.Branch, idPrefix)
	}

	return nil
}