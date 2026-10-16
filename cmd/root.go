package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	Version      = "dev"
	storeDir     string
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
	// Register flags. --store defaults to "" and getStoreDir resolves that to ~/.claudemem, so help and
	// the usage block printed after an error never show the absolute home path.
	rootCmd.PersistentFlags().StringVar(&storeDir, "store", "", "memory store directory (default ~/.claudemem)")
	rootCmd.PersistentFlags().StringVar(&outputFormat, "format", "text", "output format (text|json)")
	rootCmd.Flags().BoolP("version", "v", false, "show version information")
}
