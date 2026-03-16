// Package nostop provides configuration file support for Nostop.
package nostop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hegner123/nostop/internal/api"
)

// Config file locations (in order of precedence):
// 1. ./nostop.json (current directory)
// 2. ~/.config/nostop/config.json (XDG)
// 3. ~/.nostop.json (home directory)

// FileConfig represents the structure of the JSON configuration file.
type FileConfig struct {
	API      APIConfig      `json:"api"`
	Context  ContextConfig  `json:"context"`
	Models   ModelsConfig   `json:"models"`
	Database DatabaseConfig `json:"database"`
	UI       UIConfig       `json:"ui"`
	Tools    ToolsConfig    `json:"tools"`
}

// ToolsConfig contains configuration for agentic tool use.
type ToolsConfig struct {
	Enabled       bool     `json:"enabled"`
	Timeout       int      `json:"timeout"` // seconds per tool execution
	DisabledTools []string `json:"disabled_tools,omitempty"`
	WorkDir       string   `json:"work_dir,omitempty"`
}

// APIConfig contains API-related configuration.
type APIConfig struct {
	Key     string `json:"key,omitempty"`  // Can also use ANTHROPIC_API_KEY env
	BaseURL string `json:"base_url,omitempty"` // Optional override
	Timeout int    `json:"timeout"`  // Seconds (default: 120)
}

// ContextConfig contains context management configuration.
type ContextConfig struct {
	MaxTokens        int     `json:"max_tokens"`        // Default: 200000
	ArchiveThreshold float64 `json:"archive_threshold"` // Default: 0.95
	ArchiveTarget    float64 `json:"archive_target"`    // Default: 0.50
}

// ModelsConfig contains model selection configuration.
type ModelsConfig struct {
	Chat      string `json:"chat"`      // Default: claude-opus-4-5-20251101
	Detection string `json:"detection"` // Default: claude-haiku-4-5-20251001
}

// DatabaseConfig contains database configuration.
type DatabaseConfig struct {
	Path string `json:"path"` // Default: ~/.local/share/nostop/nostop.db
}

// UIConfig contains UI-related configuration.
type UIConfig struct {
	Theme       string `json:"theme"`        // "dark" or "light"
	AutoRefresh bool   `json:"auto_refresh"` // Debug view auto-refresh
}

// DefaultFileConfig returns a FileConfig with sensible defaults.
func DefaultFileConfig() FileConfig {
	return FileConfig{
		API: APIConfig{
			Timeout: 120,
		},
		Context: ContextConfig{
			MaxTokens:        200000,
			ArchiveThreshold: ThresholdArchive,
			ArchiveTarget:    ArchiveTarget,
		},
		Models: ModelsConfig{
			Chat:      api.ModelSonnet4,
			Detection: api.ModelHaiku45Latest,
		},
		Database: DatabaseConfig{
			Path: "~/.local/share/nostop/nostop.db",
		},
		UI: UIConfig{
			Theme:       "dark",
			AutoRefresh: true,
		},
	}
}

// LoadConfig loads configuration from the first found config file.
// It searches in order: ./nostop.json, ~/.config/nostop/config.json, ~/.nostop.json
// Returns the loaded config and the path it was loaded from (empty if no file found).
func LoadConfig() (*FileConfig, string, error) {
	return LoadConfigWithPath("")
}

// LoadConfigWithPath loads configuration from a specific path if provided,
// otherwise searches the default locations.
func LoadConfigWithPath(configPath string) (*FileConfig, string, error) {
	cfg := DefaultFileConfig()

	if configPath != "" {
		// Use specified path
		expanded := expandPath(configPath)
		data, err := os.ReadFile(expanded)
		if err != nil {
			return nil, "", fmt.Errorf("config file not found: %s", configPath)
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, "", fmt.Errorf("failed to parse config file %s: %w", configPath, err)
		}
		return &cfg, expanded, nil
	}

	// Search default locations
	paths := getConfigPaths()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, "", fmt.Errorf("failed to parse config file %s: %w", path, err)
		}
		return &cfg, path, nil
	}

	// No config file found, return defaults
	return &cfg, "", nil
}

// getConfigPaths returns the list of config file paths to search.
func getConfigPaths() []string {
	var paths []string

	// 1. Current directory
	paths = append(paths, "nostop.json")

	// 2. XDG config directory
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			configHome = filepath.Join(home, ".config")
		}
	}
	if configHome != "" {
		paths = append(paths, filepath.Join(configHome, "nostop", "config.json"))
	}

	// 3. Home directory
	home, err := os.UserHomeDir()
	if err == nil {
		paths = append(paths, filepath.Join(home, ".nostop.json"))
	}

	return paths
}

// expandPath expands ~ to the user's home directory.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// ToNostopConfig converts a FileConfig to the Nostop engine Config.
// It applies environment variable overrides.
func (fc *FileConfig) ToNostopConfig() Config {
	cfg := Config{
		MaxContextTokens: fc.Context.MaxTokens,
		ArchiveThreshold: fc.Context.ArchiveThreshold,
		ArchiveTarget:    fc.Context.ArchiveTarget,
		DetectionModel:   fc.Models.Detection,
		ChatModel:        fc.Models.Chat,
		APIKey:           fc.API.Key,
		DBPath:           expandPath(fc.Database.Path),
	}

	// Environment variable overrides
	if envKey := os.Getenv("ANTHROPIC_API_KEY"); envKey != "" {
		cfg.APIKey = envKey
	}

	if envDBPath := os.Getenv("NOSTOP_DB_PATH"); envDBPath != "" {
		cfg.DBPath = expandPath(envDBPath)
	}

	// Tools configuration
	cfg.ToolsEnabled = fc.Tools.Enabled
	cfg.DisabledTools = fc.Tools.DisabledTools
	cfg.ToolTimeout = time.Duration(fc.Tools.Timeout) * time.Second
	if cfg.ToolTimeout == 0 {
		cfg.ToolTimeout = 30 * time.Second
	}
	cfg.ToolWorkDir = fc.Tools.WorkDir
	if cfg.ToolWorkDir == "" {
		cfg.ToolWorkDir, _ = os.Getwd()
	}

	return cfg
}

// WriteDefaultConfig writes the default configuration to the specified path.
// It creates parent directories if needed.
func WriteDefaultConfig(path string) error {
	expanded := expandPath(path)

	// Create parent directories
	dir := filepath.Dir(expanded)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Check if file exists
	if _, err := os.Stat(expanded); err == nil {
		return fmt.Errorf("config file already exists: %s", expanded)
	}

	// Create file
	f, err := os.Create(expanded)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer f.Close()

	// Write default config
	content := defaultConfigTemplate()
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// defaultConfigTemplate returns the default config file content.
func defaultConfigTemplate() string {
	cfg := DefaultFileConfig()
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return string(data) + "\n"
}

// GetConfigPath returns the path where a new config would be written.
// Prefers XDG config directory.
func GetConfigPath() string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "nostop.json"
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "nostop", "config.json")
}

// ValidateConfig validates a FileConfig and returns any errors.
func ValidateConfig(cfg *FileConfig) []error {
	var errs []error

	// Validate context settings
	if cfg.Context.MaxTokens <= 0 {
		errs = append(errs, fmt.Errorf("context.max_tokens must be positive"))
	}
	if cfg.Context.ArchiveThreshold <= 0 || cfg.Context.ArchiveThreshold > 1 {
		errs = append(errs, fmt.Errorf("context.archive_threshold must be between 0 and 1"))
	}
	if cfg.Context.ArchiveTarget <= 0 || cfg.Context.ArchiveTarget > 1 {
		errs = append(errs, fmt.Errorf("context.archive_target must be between 0 and 1"))
	}
	if cfg.Context.ArchiveTarget >= cfg.Context.ArchiveThreshold {
		errs = append(errs, fmt.Errorf("context.archive_target must be less than archive_threshold"))
	}

	// Validate API settings
	if cfg.API.Timeout <= 0 {
		errs = append(errs, fmt.Errorf("api.timeout must be positive"))
	}

	// Validate UI settings
	if cfg.UI.Theme != "" && cfg.UI.Theme != "dark" && cfg.UI.Theme != "light" {
		errs = append(errs, fmt.Errorf("ui.theme must be 'dark' or 'light'"))
	}

	return errs
}
