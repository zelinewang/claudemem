package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config provides simple JSON configuration management
type Config struct {
	path string
	data map[string]interface{}
}

// Load reads config from ~/.claudemem/config.json
func Load(storeDir string) (*Config, error) {
	configPath := filepath.Join(storeDir, "config.json")
	c := &Config{
		path: configPath,
		data: make(map[string]interface{}),
	}

	// Read file if it exists
	if data, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(data, &c.data); err != nil {
			return nil, fmt.Errorf("failed to parse config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	return c, nil
}

// Save writes config to disk
func (c *Config) Save() error {
	// Ensure directory exists
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Marshal config
	data, err := json.MarshalIndent(c.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(c.path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// Get retrieves a config value
func (c *Config) Get(key string) interface{} {
	return c.data[key]
}

// GetString retrieves a string config value
func (c *Config) GetString(key string) string {
	if v, ok := c.data[key].(string); ok {
		return v
	}
	return ""
}

// GetBool retrieves a boolean config value
func (c *Config) GetBool(key string) bool {
	if v, ok := c.data[key].(bool); ok {
		return v
	}
	return false
}

// GetInt retrieves an integer config value
func (c *Config) GetInt(key string) int {
	if v, ok := c.data[key].(float64); ok {
		return int(v)
	}
	return 0
}

// Set updates a config value
func (c *Config) Set(key string, value interface{}) {
	c.data[key] = value
}

// Delete removes a config key
func (c *Config) Delete(key string) {
	delete(c.data, key)
}

// Keys returns all config keys
func (c *Config) Keys() []string {
	keys := make([]string, 0, len(c.data))
	for k := range c.data {
		keys = append(keys, k)
	}
	return keys
}

// Data returns the entire config map
func (c *Config) Data() map[string]interface{} {
	return c.data
}