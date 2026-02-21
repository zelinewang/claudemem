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
	sessionTitle     string
	sessionBranch    string
	sessionProject   string
	sessionSessionID string
	sessionTags      string
	sessionContent   string
	sessionSummary   string
	sessionDecisions []string
	sessionChanges   []string
	sessionProblems  []string
	sessionQuestions []string
	sessionNextSteps []string
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
	sessionSaveCmd.Flags().StringSliceVar(&sessionQuestions, "questions", []string{}, "Questions raised")
	sessionSaveCmd.Flags().StringSliceVar(&sessionNextSteps, "next-steps", []string{}, "Next steps")

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
	} else if sessionSummary != "" || len(sessionDecisions) > 0 || len(sessionChanges) > 0 ||
		len(sessionProblems) > 0 || len(sessionQuestions) > 0 || len(sessionNextSteps) > 0 {
		// Structured input provided
		session.Summary = sessionSummary
		session.Decisions = sessionDecisions
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
				}
			}
		} else {
			// No section headers — treat entire content as summary
			session.Summary = strings.TrimSpace(bodyContent)
		}
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