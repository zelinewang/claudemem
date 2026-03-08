package cmd

import (
	"github.com/spf13/cobra"
)

var codeCmd = &cobra.Command{
	Use:   "code",
	Short: "Code intelligence tools",
	Long:  `Tools for analyzing code structure and navigating codebases.`,
}

func init() {
	rootCmd.AddCommand(codeCmd)
}
