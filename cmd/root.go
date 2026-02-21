package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	Version     = "dev"
	storeDir    string
	outputFormat string
)

var rootCmd = &cobra.Command{
	Use:   "claudemem",
	Short: "AI-optimized memory storage system",
	Long: `claudemem is a specialized knowledge base designed for AI assistants,
particularly Claude, to maintain and retrieve context across conversations.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if v, _ := cmd.Flags().GetBool("version"); v {
			fmt.Printf("claudemem version %s\n", Version)
			return nil
		}
		return cmd.Help()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	// Set default store directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	defaultStore := filepath.Join(homeDir, ".claudemem")

	// Register flags
	rootCmd.PersistentFlags().StringVar(&storeDir, "store", defaultStore, "memory store directory")
	rootCmd.PersistentFlags().StringVar(&outputFormat, "format", "text", "output format (text|json)")
	rootCmd.Flags().BoolP("version", "v", false, "show version information")
}