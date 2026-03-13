package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hegner123/nostop/internal/tui"
	"github.com/hegner123/nostop/pkg/nostop"
)

const (
	// envAPIKey is the environment variable for the Anthropic API key.
	envAPIKey = "ANTHROPIC_API_KEY"

	// defaultDBName is the default database filename.
	defaultDBName = "nostop.db"
)

// Build info - set via ldflags
var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildTime    = "unknown"
)

var (
	configPath  = flag.String("config", "", "Path to config file (default: searches ./nostop.toml, ~/.config/nostop/config.toml, ~/.nostop.toml)")
	writeConfig = flag.Bool("write-config", false, "Write default config to ~/.config/nostop/config.toml and exit")
	showConfig  = flag.Bool("show-config", false, "Show loaded configuration and exit")
	verbose     = flag.Bool("verbose", false, "Enable verbose output")
	version     = flag.Bool("version", false, "Show version and exit")
	debug       = flag.Bool("debug", false, "Enable debug logging to ~/.local/share/nostop/debug.log")
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
		tui.Log("Nostop starting - version=%s commit=%s", buildVersion, buildCommit)
	}

	// Handle version flag
	if *version {
		fmt.Printf("nostop %s\n", buildVersion)
		fmt.Printf("  commit: %s\n", buildCommit)
		fmt.Printf("  built:  %s\n", buildTime)
		return nil
	}

	// Handle write-config flag
	if *writeConfig {
		return handleWriteConfig()
	}

	// Load configuration from file
	fileCfg, configFile, err := nostop.LoadConfigWithPath(*configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// First-run setup: if no config file was found and the user didn't
	// specify one explicitly, run the interactive setup wizard.
	if configFile == "" && *configPath == "" {
		setupPath, setupErr := firstRunSetup()
		if setupErr != nil {
			return fmt.Errorf("setup failed: %w", setupErr)
		}
		// Reload from the newly written config
		fileCfg, configFile, err = nostop.LoadConfigWithPath(setupPath)
		if err != nil {
			return fmt.Errorf("failed to load generated config: %w", err)
		}
	}

	// Validate configuration
	if errs := nostop.ValidateConfig(fileCfg); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "Config error: %v\n", e)
		}
		return fmt.Errorf("invalid configuration")
	}

	// Convert to Nostop config (applies env overrides)
	cfg := fileCfg.ToNostopConfig()

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

	// Initialize Nostop engine
	engine, err := nostop.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize Nostop engine: %w", err)
	}
	defer engine.Close()

	// Root cancellable context — cancelled on SIGINT/SIGTERM so that
	// in-flight API calls, DB queries, and background goroutines shut
	// down promptly instead of running with context.Background().
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
		// Second signal = immediate exit
		<-sigCh
		os.Exit(1)
	}()

	// Create and run the Bubbletea program
	app := tui.NewApp(engine, ctx)

	// Wire engine internals to the TUI so topics, context usage,
	// and archiver state are visible in the Topics/Debug views.
	app.SetTracker(engine.Tracker())
	app.SetContextManager(engine.ContextMgr())
	app.SetArchiver(engine.InternalArchiver())

	// Redirect stdlib log to the debug log file (or discard) so that
	// log.Printf calls from the engine don't corrupt Bubbletea's display.
	log.SetOutput(tui.LogWriter())

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
	path := nostop.GetConfigPath()
	if *configPath != "" {
		path = *configPath
	}

	if err := nostop.WriteDefaultConfig(path); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	fmt.Printf("Default configuration written to: %s\n", path)
	return nil
}

// handleShowConfig displays the current configuration.
func handleShowConfig(fileCfg *nostop.FileConfig, configFile string, cfg *nostop.Config) error {
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
	fmt.Println()

	fmt.Println("[tools]")
	fmt.Printf("  enabled = %v\n", fileCfg.Tools.Enabled)
	fmt.Printf("  timeout = %ds\n", int(cfg.ToolTimeout.Seconds()))
	if len(fileCfg.Tools.DisabledTools) > 0 {
		fmt.Printf("  disabled_tools = %v\n", fileCfg.Tools.DisabledTools)
	}
	if fileCfg.Tools.WorkDir != "" {
		fmt.Printf("  work_dir = %q\n", fileCfg.Tools.WorkDir)
	}

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
