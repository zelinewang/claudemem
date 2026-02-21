package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zanelabz/claudemem/pkg/models"
	"github.com/zanelabz/claudemem/pkg/storage"
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

	// If body content was provided, parse it
	if bodyContent != "" {
		// Create a temporary markdown with frontmatter to parse
		tempMarkdown := fmt.Sprintf(`---
id: %s
type: session
title: %s
date: %s
branch: %s
project: %s
session_id: %s
tags: %s
created: %s
---

%s`, session.ID, session.Title, session.Date, session.Branch,
			session.Project, session.SessionID, sessionTags,
			session.Created.Format("2006-01-02T15:04:05Z"), bodyContent)

		// Parse the markdown to extract structured sections
		parsed, err := storage.ParseSessionMarkdown([]byte(tempMarkdown))
		if err == nil {
			// Copy parsed sections to our session
			session.Summary = parsed.Summary
			session.Decisions = parsed.Decisions
			session.Changes = parsed.Changes
			session.Problems = parsed.Problems
			session.Questions = parsed.Questions
			session.NextSteps = parsed.NextSteps
		} else {
			// If parsing fails, treat entire content as summary
			session.Summary = strings.TrimSpace(bodyContent)
		}
	}

	// Save session
	if err := store.SaveSession(session); err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}

	// Output result
	idPrefix := session.ID
	if len(idPrefix) > 8 {
		idPrefix = idPrefix[:8]
	}

	if outputFormat == "json" {
		return OutputJSON(map[string]string{
			"id":      session.ID,
			"title":   session.Title,
			"date":    session.Date,
			"branch":  session.Branch,
			"project": session.Project,
		})
	} else {
		OutputText(fmt.Sprintf("✓ Saved session: \"%s\" (%s, %s) [id: %s]",
			session.Title, session.Date, session.Branch, idPrefix))
	}

	return nil
}