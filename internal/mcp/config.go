package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// MCPConfig is the top-level structure of a .mcp.json file.
// Compatible with Claude Code's format.
type MCPConfig struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

// ServerConfig holds the configuration for a single MCP server.
type ServerConfig struct {
	// stdio transport
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// HTTP transport
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	// Common
	Disabled bool   `json:"disabled,omitempty"`
	Timeout  string `json:"timeout,omitempty"` // duration string, default "30s"
}

// TransportType returns "stdio", "http", or "" if invalid.
func (c ServerConfig) TransportType() string {
	hasCommand := c.Command != ""
	hasURL := c.URL != ""

	if hasCommand && hasURL {
		return "" // invalid: both set
	}
	if hasCommand {
		return "stdio"
	}
	if hasURL {
		return "http"
	}
	return "" // invalid: neither set
}

// ParseTimeout returns the configured timeout or the default (30s).
func (c ServerConfig) ParseTimeout() time.Duration {
	if c.Timeout == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(c.Timeout)
	if err != nil {
		return 30 * time.Second
	}
	return d
}

// LoadConfig reads and parses a single .mcp.json file.
// Environment variables in string values are expanded: ${VAR} and ${VAR:-default}.
func LoadConfig(path string) (*MCPConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	// Expand environment variables before parsing.
	expanded := expandEnvVars(string(data))

	var cfg MCPConfig
	if err := json.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	// Validate each server config.
	for name, sc := range cfg.MCPServers {
		if err := validateServerConfig(name, sc); err != nil {
			return nil, err
		}
	}

	return &cfg, nil
}

// MergeConfigs merges multiple configs with earlier entries taking precedence.
// If the same server name appears in multiple configs, the first one wins.
func MergeConfigs(configs ...*MCPConfig) *MCPConfig {
	merged := &MCPConfig{
		MCPServers: make(map[string]ServerConfig),
	}

	for _, cfg := range configs {
		if cfg == nil {
			continue
		}
		for name, sc := range cfg.MCPServers {
			if _, exists := merged.MCPServers[name]; !exists {
				merged.MCPServers[name] = sc
			}
		}
	}

	return merged
}

// DiscoverConfigs loads and merges configs from the standard search paths.
// Search order (lowest number wins for same-named servers):
//  1. explicit path (from --mcp-config flag)
//  2. .mcp.json in working directory
//  3. .mcp.json in git root
//  4. ~/.config/nostop/mcp.json
func DiscoverConfigs(explicitPath, workDir string) (*MCPConfig, error) {
	var configs []*MCPConfig

	paths := discoverPaths(explicitPath, workDir)
	for _, path := range paths {
		cfg, err := LoadConfig(path)
		if err != nil {
			// Log but continue — a missing or broken config shouldn't prevent
			// other configs from loading.
			fmt.Fprintf(os.Stderr, "warning: skipping MCP config %s: %v\n", path, err)
			continue
		}
		configs = append(configs, cfg)
	}

	if len(configs) == 0 {
		return &MCPConfig{MCPServers: make(map[string]ServerConfig)}, nil
	}

	return MergeConfigs(configs...), nil
}

// discoverPaths returns the ordered list of config file paths to try.
func discoverPaths(explicitPath, workDir string) []string {
	var paths []string

	// 1. Explicit path from CLI flag
	if explicitPath != "" {
		paths = append(paths, explicitPath)
	}

	// 2. .mcp.json in working directory
	if workDir != "" {
		p := filepath.Join(workDir, ".mcp.json")
		if fileExists(p) {
			paths = append(paths, p)
		}
	}

	// 3. .mcp.json in git root
	gitRoot := findGitRoot(workDir)
	if gitRoot != "" && gitRoot != workDir {
		p := filepath.Join(gitRoot, ".mcp.json")
		if fileExists(p) {
			paths = append(paths, p)
		}
	}

	// 4. ~/.config/nostop/mcp.json
	home, err := os.UserHomeDir()
	if err == nil {
		p := filepath.Join(home, ".config", "nostop", "mcp.json")
		if fileExists(p) {
			paths = append(paths, p)
		}
	}

	return paths
}

// validateServerConfig checks a server config for errors.
func validateServerConfig(name string, sc ServerConfig) error {
	if strings.Contains(name, "__") {
		return fmt.Errorf("MCP server name %q must not contain \"__\" (double underscore)", name)
	}

	tt := sc.TransportType()
	if tt == "" {
		if sc.Command != "" && sc.URL != "" {
			return fmt.Errorf("MCP server %q has both \"command\" and \"url\" — use one or the other", name)
		}
		return fmt.Errorf("MCP server %q must have either \"command\" (stdio) or \"url\" (HTTP)", name)
	}

	if sc.Timeout != "" {
		if _, err := time.ParseDuration(sc.Timeout); err != nil {
			return fmt.Errorf("MCP server %q has invalid timeout %q: %w", name, sc.Timeout, err)
		}
	}

	return nil
}

// envVarPattern matches ${VAR} and ${VAR:-default}.
var envVarPattern = regexp.MustCompile(`\$\{([^}:]+)(?::-([^}]*))?\}`)

// expandEnvVars replaces ${VAR} and ${VAR:-default} in the input string.
func expandEnvVars(input string) string {
	return envVarPattern.ReplaceAllStringFunc(input, func(match string) string {
		parts := envVarPattern.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}

		varName := parts[1]
		defaultVal := ""
		if len(parts) >= 3 {
			defaultVal = parts[2]
		}

		val, ok := os.LookupEnv(varName)
		if !ok || val == "" {
			if defaultVal != "" {
				return defaultVal
			}
			return match // leave unexpanded if no default
		}
		return val
	})
}

// findGitRoot walks up from dir to find the .git directory.
func findGitRoot(dir string) string {
	if dir == "" {
		return ""
	}

	// Use git rev-parse if available, fall back to manual walk.
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// fileExists returns true if the path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
