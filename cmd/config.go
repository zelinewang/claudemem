package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zelinewang/claudemem/pkg/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration",
	Long:  `Manage ClaudeMem configuration settings.`,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		value := args[1]

		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		cfg.Set(key, value)

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		OutputText("Config set: %s = %s", key, value)
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a configuration value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]

		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		value := cfg.GetString(key)
		if value == "" {
			return fmt.Errorf("configuration key not found: %s", key)
		}

		if outputFormat == "json" {
			return OutputJSON(map[string]string{key: value})
		}

		OutputText("%s = %s", key, value)
		return nil
	},
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configuration values",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		allConfig := cfg.Data()

		if outputFormat == "json" {
			return OutputJSON(allConfig)
		}

		if len(allConfig) == 0 {
			OutputText("No configuration set")
			return nil
		}

		var keys []string
		for k := range allConfig {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		OutputText("Configuration:")
		OutputText("--------------")
		for _, key := range keys {
			value := fmt.Sprintf("%v", allConfig[key])
			// Mask sensitive values
			if strings.Contains(strings.ToLower(key), "token") ||
				strings.Contains(strings.ToLower(key), "secret") ||
				strings.Contains(strings.ToLower(key), "password") {
				if len(value) > 8 {
					value = value[:4] + "..." + value[len(value)-4:]
				} else if len(value) > 0 {
					value = "***"
				}
			}
			OutputText("  %s = %s", key, value)
		}

		return nil
	},
}

func loadConfig() (*config.Config, error) {
	return config.Load(getStoreDir())
}

// getStoreDir returns the configured store directory
func getStoreDir() string {
	if storeDir != "" {
		return storeDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claudemem")
}

func init() {
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configListCmd)
	rootCmd.AddCommand(configCmd)
}
