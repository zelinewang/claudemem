package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/storage"
)

var sessionValidateCmd = &cobra.Command{
	Use:   "validate [file]",
	Short: "Validate a session report markdown file before saving",
	Long: `Validate a session markdown file against quality standards.

All checks are mechanical — word counts, section presence, structural completeness.
Returns exit code 0 if PASSED, 1 if FAILED.

The file can be provided as an argument or piped via stdin.

Examples:
  # Validate a file
  claudemem session validate /tmp/claudemem-session-report.md

  # Validate from stdin
  cat report.md | claudemem session validate

  # JSON output
  claudemem session validate report.md --format json`,
	RunE: runSessionValidate,
}

func init() {
	sessionCmd.AddCommand(sessionValidateCmd)
}

func runSessionValidate(cmd *cobra.Command, args []string) error {
	var content string

	if len(args) > 0 {
		// Read from file
		data, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}
		content = string(data)
	} else {
		// Try stdin
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("failed to read stdin: %w", err)
			}
			content = string(data)
		} else {
			return fmt.Errorf("no file provided and no stdin input")
		}
	}

	if content == "" {
		return fmt.Errorf("empty content")
	}

	result := storage.ValidateSessionMarkdown(content)

	if outputFormat == "json" {
		return OutputJSON(result)
	}

	// Text output
	if result.Valid {
		OutputText("Session validation: PASSED")
	} else {
		OutputText("Session validation: FAILED")
	}

	for _, check := range result.Checks {
		status := "✓"
		if !check.Passed {
			status = "✗"
		}
		OutputText("  %s: %s %s", check.Section, check.Message, status)
	}

	if !result.Valid {
		os.Exit(1)
	}

	return nil
}
