package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	hookpkg "github.com/zelinewang/claudemem/pkg/hooks"
)

var (
	hookEventDryRun  bool
	hookEventPayload string
	hookEvalFixtures string
)

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Evaluate normalized agent hook events",
	Long: `Evaluate normalized agent hook events without coupling claudemem to a
specific coding agent's native hook schema.`,
}

var hookEventCmd = &cobra.Command{
	Use:   "event",
	Short: "Dry-run a normalized hook event",
	Long: `Dry-run a normalized hook event and return a machine-readable decision.

The first version is intentionally non-mutating. It classifies an event as a
candidate, redacts sensitive text, and reports whether the event is eligible
for later saving. It does not write notes, sessions, or candidate queue rows.`,
	RunE: runHookEvent,
}

var hookEvalCmd = &cobra.Command{
	Use:   "eval",
	Short: "Run hook classifier evaluation fixtures",
	Long: `Run hook classifier evaluation fixtures and report precision, recall,
and per-case pass/fail status. This is a development harness for improving the
deterministic classifier before enabling any autosave path.`,
	RunE: runHookEval,
}

func init() {
	rootCmd.AddCommand(hookCmd)

	hookCmd.AddCommand(hookEventCmd)
	hookEventCmd.Flags().BoolVar(&hookEventDryRun, "dry-run", true, "evaluate only; do not mutate memory")
	hookEventCmd.Flags().StringVar(&hookEventPayload, "payload", "-", "normalized hook payload path, or - for stdin")

	hookCmd.AddCommand(hookEvalCmd)
	hookEvalCmd.Flags().StringVar(&hookEvalFixtures, "fixtures", "pkg/hooks/testdata/eval", "directory of hook eval JSON fixtures")
}

func runHookEvent(cmd *cobra.Command, args []string) error {
	if !hookEventDryRun {
		return fmt.Errorf("hook event currently supports dry-run only")
	}

	data, err := readHookPayload(hookEventPayload, os.Stdin)
	if err != nil {
		return err
	}

	decision, err := runHookEventPayload(data)
	if err != nil {
		return err
	}

	return OutputJSON(decision)
}

func runHookEventPayload(data []byte) (hookpkg.Decision, error) {
	return hookpkg.EvaluateEventJSON(data)
}

func readHookPayload(path string, stdin io.Reader) ([]byte, error) {
	if path == "" || path == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("read hook payload from stdin: %w", err)
		}
		return data, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read hook payload %s: %w", path, err)
	}
	return data, nil
}

func runHookEval(cmd *cobra.Command, args []string) error {
	report, err := hookpkg.RunEvalDir(hookEvalFixtures)
	if err != nil {
		return err
	}

	if outputFormat == "json" {
		return OutputJSON(report)
	}

	OutputText("hook eval: %d cases, %d passed, %d failed", report.Total, report.Passed, report.Failed)
	OutputText("precision: %.2f, recall: %.2f, f1: %.2f", report.Precision, report.Recall, report.F1)
	for _, result := range report.Cases {
		if result.Pass {
			continue
		}
		OutputText("failed: %s: %v", result.Name, result.Errors)
	}
	if report.Failed > 0 {
		return fmt.Errorf("hook eval failed %d/%d cases", report.Failed, report.Total)
	}

	return nil
}
