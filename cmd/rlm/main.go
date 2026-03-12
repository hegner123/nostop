package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/user/rlm/internal/tui"
	"github.com/user/rlm/pkg/rlm"
)

const (
	// envAPIKey is the environment variable for the Anthropic API key.
	envAPIKey = "ANTHROPIC_API_KEY"

	// defaultDBName is the default database filename.
	defaultDBName = "rlm.db"
)

// Build info - set via ldflags
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildTime    = "unknown"
)

var (
	configPath  = flag.String("config", "", "Path to config file (default: searches ./rlm.toml, ~/.config/rlm/config.toml, ~/.rlm.toml)")
	writeConfig = flag.Bool("write-config", false, "Write default config to ~/.config/rlm/config.toml and exit")
	showConfig  = flag.Bool("show-config", false, "Show loaded configuration and exit")
	verbose     = flag.Bool("verbose", false, "Enable verbose output")
	version     = flag.Bool("version", false, "Show version and exit")
	debug       = flag.Bool("debug", false, "Enable debug logging to ~/.local/share/rlm/debug.log")
	debugPath   = flag.String("debug-log", "", "Custom path for debug log file")
)

func main() {
	flag.Parse()

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Initialize debug logging early
	if *debug || *debugPath != "" {
		if err := tui.InitLogger(*debugPath, true); err != nil {
			return fmt.Errorf("failed to initialize debug logger: %w", err)
		}
		defer tui.CloseLogger()
		tui.Log("RLM starting - version=%s commit=%s", buildVersion, buildCommit)
	}

	// Handle version flag
	if *version {
		fmt.Printf("rlm %s\n", buildVersion)
		fmt.Printf("  commit: %s\n", buildCommit)
		fmt.Printf("  built:  %s\n", buildTime)
		return nil
	}

	// Handle write-config flag
	if *writeConfig {
		return handleWriteConfig()
	}

	// Load configuration from file
	fileCfg, configFile, err := rlm.LoadConfigWithPath(*configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Validate configuration
	if errs := rlm.ValidateConfig(fileCfg); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "Config error: %v\n", e)
		}
		return fmt.Errorf("invalid configuration")
	}

	// Convert to RLM config (applies env overrides)
	cfg := fileCfg.ToRLMConfig()

	// Ensure database directory exists
	if err := ensureDBDir(cfg.DBPath); err != nil {
		return err
	}

	// Handle show-config flag
	if *showConfig {
		return handleShowConfig(fileCfg, configFile, &cfg)
	}

	// Verbose output
	if *verbose {
		if configFile != "" {
			fmt.Fprintf(os.Stderr, "Config loaded from: %s\n", configFile)
		} else {
			fmt.Fprintf(os.Stderr, "Using default configuration (no config file found)\n")
		}
		fmt.Fprintf(os.Stderr, "Database: %s\n", cfg.DBPath)
		fmt.Fprintf(os.Stderr, "Chat model: %s\n", cfg.ChatModel)
		fmt.Fprintf(os.Stderr, "Detection model: %s\n", cfg.DetectionModel)
	}

	// Check for API key
	if cfg.APIKey == "" {
		return fmt.Errorf("API key is required. Set %s environment variable or add 'key' to [api] section in config file", envAPIKey)
	}

	// Initialize RLM engine
	engine, err := rlm.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize RLM engine: %w", err)
	}
	defer engine.Close()

	// Create and run the Bubbletea program
	app := tui.NewApp(engine)
	p := tea.NewProgram(
		app,
		tea.WithAltScreen(),       // Use alternate screen buffer
		tea.WithMouseCellMotion(), // Enable mouse support
	)

	// Run the program
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running TUI: %w", err)
	}

	return nil
}

// handleWriteConfig writes the default configuration file.
func handleWriteConfig() error {
	path := rlm.GetConfigPath()
	if *configPath != "" {
		path = *configPath
	}

	if err := rlm.WriteDefaultConfig(path); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	fmt.Printf("Default configuration written to: %s\n", path)
	return nil
}

// handleShowConfig displays the current configuration.
func handleShowConfig(fileCfg *rlm.FileConfig, configFile string, cfg *rlm.Config) error {
	if configFile != "" {
		fmt.Printf("Config file: %s\n", configFile)
	} else {
		fmt.Printf("Config file: (none - using defaults)\n")
	}
	fmt.Println()

	fmt.Println("[api]")
	if cfg.APIKey != "" {
		fmt.Printf("  key = %q (set)\n", maskAPIKey(cfg.APIKey))
	} else {
		fmt.Printf("  key = (not set)\n")
	}
	fmt.Printf("  base_url = %q\n", fileCfg.API.BaseURL)
	fmt.Printf("  timeout = %d\n", fileCfg.API.Timeout)
	fmt.Println()

	fmt.Println("[context]")
	fmt.Printf("  max_tokens = %d\n", cfg.MaxContextTokens)
	fmt.Printf("  archive_threshold = %.2f\n", cfg.ArchiveThreshold)
	fmt.Printf("  archive_target = %.2f\n", cfg.ArchiveTarget)
	fmt.Println()

	fmt.Println("[models]")
	fmt.Printf("  chat = %q\n", cfg.ChatModel)
	fmt.Printf("  detection = %q\n", cfg.DetectionModel)
	fmt.Println()

	fmt.Println("[database]")
	fmt.Printf("  path = %q\n", cfg.DBPath)
	fmt.Println()

	fmt.Println("[ui]")
	fmt.Printf("  theme = %q\n", fileCfg.UI.Theme)
	fmt.Printf("  auto_refresh = %v\n", fileCfg.UI.AutoRefresh)

	return nil
}

// maskAPIKey masks an API key for display.
func maskAPIKey(key string) string {
	if len(key) < 12 {
		return "***"
	}
	return key[:8] + "..." + key[len(key)-4:]
}

// ensureDBDir ensures the database directory exists.
func ensureDBDir(dbPath string) error {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
	}
	return nil
}
