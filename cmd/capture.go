package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/models"
)

var captureCmd = &cobra.Command{
	Use:   "capture",
	Short: "Auto-capture observations from tool output",
	Long: `Processes tool output from Claude Code PostToolUse hooks and
automatically extracts noteworthy observations as notes.

Designed to be called from a Claude Code PostToolUse hook.
Reads JSON from stdin with tool name and output, extracts
key observations, and saves them as notes.

Feature-flagged: requires features.auto_capture = true in config.`,
	RunE: runCapture,
}

type hookInput struct {
	ToolName string `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	Output   string `json:"output"`
}

func init() {
	rootCmd.AddCommand(captureCmd)
}

func runCapture(cmd *cobra.Command, args []string) error {
	// Read hook payload from stdin
	data, err := io.ReadAll(os.Stdin)
	if err != nil || len(data) == 0 {
		// No input or error — exit silently (hook should not block)
		return nil
	}

	var input hookInput
	if err := json.Unmarshal(data, &input); err != nil {
		// Invalid JSON — exit silently
		return nil
	}

	// Only capture from tools that produce meaningful observations
	if !isCapturableTool(input.ToolName) {
		return nil
	}

	// Extract observation from output
	observation := extractObservation(input.ToolName, input.Output)
	if observation == "" {
		return nil
	}

	// Save as note
	store, err := getStore()
	if err != nil {
		return nil // Fail silently — never block the user
	}

	title := fmt.Sprintf("Auto: %s observation (%s)", input.ToolName, time.Now().Format("2006-01-02 15:04"))
	note := models.NewNote("auto-capture", title, observation)
	note.Tags = []string{"auto-capture", strings.ToLower(input.ToolName)}

	// Check for recent duplicate
	existing, _ := store.SearchNotes(input.ToolName, "auto-capture", nil)
	for _, n := range existing {
		if time.Since(n.Updated) < 5*time.Minute && strings.Contains(n.Content, observation[:min(50, len(observation))]) {
			return nil // Skip duplicate within 5 min window
		}
	}

	store.AddNote(note)
	return nil
}

func isCapturableTool(name string) bool {
	// Tools that tend to produce noteworthy observations
	switch name {
	case "Bash", "bash":
		return true // Command outputs, errors, test results
	default:
		return false
	}
}

func extractObservation(toolName, output string) string {
	if len(output) < 20 {
		return "" // Too short to be meaningful
	}
	if len(output) > 2000 {
		return "" // Too long — likely a full file dump, not an observation
	}

	// Look for error patterns worth capturing
	lines := strings.Split(output, "\n")
	var meaningful []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Capture errors, warnings, and test results
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") ||
			strings.Contains(lower, "fail") ||
			strings.Contains(lower, "warning") ||
			strings.Contains(lower, "passed") ||
			strings.Contains(lower, "permission denied") {
			meaningful = append(meaningful, line)
		}
	}

	if len(meaningful) == 0 {
		return ""
	}

	return strings.Join(meaningful, "\n")
}
