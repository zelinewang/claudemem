package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/code"
)

var codeOutlineCmd = &cobra.Command{
	Use:   "outline <file>",
	Short: "Show code structure (functions, classes, types)",
	Long: `Extract and display the structural outline of a source file.
Shows function signatures, class definitions, type declarations,
and other top-level symbols without displaying full implementations.

Supported languages: Go, Python, TypeScript/JavaScript, Rust.

Examples:
  claudemem code outline main.go
  claudemem code outline src/server.ts --format json
  claudemem code outline lib/models.py`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath := args[0]

		content, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}

		result := code.Outline(filePath, string(content))

		if outputFormat == "json" {
			return OutputJSON(result)
		}

		if len(result.Symbols) == 0 {
			OutputText("No symbols found in %s (language: %s)", result.File, result.Lang)
			return nil
		}

		OutputText("%s (%s) — %d symbols\n", result.File, result.Lang, len(result.Symbols))

		for _, sym := range result.Symbols {
			OutputText("  L%-4d %-10s %s", sym.Line, sym.Type, sym.Name)
		}

		return nil
	},
}

func init() {
	codeCmd.AddCommand(codeOutlineCmd)
}
