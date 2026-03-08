package cmd

import (
	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Context management for session integration",
	Long:  `Commands for managing context injection and retrieval across sessions.`,
}

func init() {
	rootCmd.AddCommand(contextCmd)
}
