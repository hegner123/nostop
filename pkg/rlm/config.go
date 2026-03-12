// Package rlm provides configuration file support for RLM.
package rlm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/user/rlm/internal/api"
)

// Config file locations (in order of precedence):
// 1. ./rlm.toml (current directory)
// 2. ~/.config/rlm/config.toml (XDG)
// 3. ~/.rlm.toml (home directory)

// FileConfig represents the structure of the TOML configuration file.
type FileConfig struct {
	API      APIConfig      `toml:"api"`
	Context  ContextConfig  `toml:"context"`
	Models   ModelsConfig   `toml:"models"`
	Database DatabaseConfig `toml:"database"`
	UI       UIConfig       `toml:"ui"`
}

// APIConfig contains API-related configuration.
type APIConfig struct {
	Key     string `toml:"key"`      // Can also use ANTHROPIC_API_KEY env
	BaseURL string `toml:"base_url"` // Optional override
	Timeout int    `toml:"timeout"`  // Seconds (default: 120)
}

// ContextConfig contains context management configuration.
type ContextConfig struct {
	MaxTokens        int     `toml:"max_tokens"`        // Default: 200000
	ArchiveThreshold float64 `toml:"archive_threshold"` // Default: 0.95
	ArchiveTarget    float64 `toml:"archive_target"`    // Default: 0.50
}

// ModelsConfig contains model selection configuration.
type ModelsConfig struct {
	Chat      string `toml:"chat"`      // Default: claude-sonnet-4-20250514
	Detection string `toml:"detection"` // Default: claude-3-5-haiku-latest
}

// DatabaseConfig contains database configuration.
type DatabaseConfig struct {
	Path string `toml:"path"` // Default: ~/.local/share/rlm/rlm.db
}

// UIConfig contains UI-related configuration.
type UIConfig struct {
	Theme       string `toml:"theme"`        // "dark" or "light"
	AutoRefresh bool   `toml:"auto_refresh"` // Debug view auto-refresh
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
			Detection: api.ModelHaiku35Latest,
		},
		Database: DatabaseConfig{
			Path: "~/.local/share/rlm/rlm.db",
		},
		UI: UIConfig{
			Theme:       "dark",
			AutoRefresh: true,
		},
	}
}

// LoadConfig loads configuration from the first found config file.
// It searches in order: ./rlm.toml, ~/.config/rlm/config.toml, ~/.rlm.toml
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
		if _, err := os.Stat(expanded); err != nil {
			return nil, "", fmt.Errorf("config file not found: %s", configPath)
		}
		if _, err := toml.DecodeFile(expanded, &cfg); err != nil {
			return nil, "", fmt.Errorf("failed to parse config file %s: %w", configPath, err)
		}
		return &cfg, expanded, nil
	}

	// Search default locations
	paths := getConfigPaths()
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, &cfg); err != nil {
				return nil, "", fmt.Errorf("failed to parse config file %s: %w", path, err)
			}
			return &cfg, path, nil
		}
	}

	// No config file found, return defaults
	return &cfg, "", nil
}

// getConfigPaths returns the list of config file paths to search.
func getConfigPaths() []string {
	var paths []string

	// 1. Current directory
	paths = append(paths, "rlm.toml")

	// 2. XDG config directory
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			configHome = filepath.Join(home, ".config")
		}
	}
	if configHome != "" {
		paths = append(paths, filepath.Join(configHome, "rlm", "config.toml"))
	}

	// 3. Home directory
	home, err := os.UserHomeDir()
	if err == nil {
		paths = append(paths, filepath.Join(home, ".rlm.toml"))
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

// ToRLMConfig converts a FileConfig to the RLM engine Config.
// It applies environment variable overrides.
func (fc *FileConfig) ToRLMConfig() Config {
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

	if envDBPath := os.Getenv("RLM_DB_PATH"); envDBPath != "" {
		cfg.DBPath = expandPath(envDBPath)
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

	// Write default config with comments
	content := defaultConfigTemplate()
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// defaultConfigTemplate returns the default config file content with comments.
func defaultConfigTemplate() string {
	return `# RLM Configuration File
# https://github.com/user/rlm

[api]
# API key for Anthropic Claude API
# Can also be set via ANTHROPIC_API_KEY environment variable
# key = "sk-ant-..."

# Optional API base URL override (for proxies or custom endpoints)
# base_url = "https://api.anthropic.com"

# Request timeout in seconds
timeout = 120

[context]
# Maximum context tokens (model limit)
max_tokens = 200000

# Archive threshold - triggers archival when context usage exceeds this percentage
archive_threshold = 0.95

# Archive target - archive until context usage drops to this percentage
archive_target = 0.50

[models]
# Model for chat responses
chat = "claude-sonnet-4-20250514"

# Model for topic detection (should be fast/cheap)
detection = "claude-3-5-haiku-latest"

[database]
# Path to SQLite database
# Can also be set via RLM_DB_PATH environment variable
path = "~/.local/share/rlm/rlm.db"

[ui]
# UI theme: "dark" or "light"
theme = "dark"

# Enable auto-refresh in debug view
auto_refresh = true
`
}

// GetConfigPath returns the path where a new config would be written.
// Prefers XDG config directory.
func GetConfigPath() string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "rlm.toml"
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "rlm", "config.toml")
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
