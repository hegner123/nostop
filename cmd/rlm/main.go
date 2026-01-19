package main

import (
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

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Get API key from environment
	apiKey := os.Getenv(envAPIKey)
	if apiKey == "" {
		return fmt.Errorf("environment variable %s is required", envAPIKey)
	}

	// Determine database path
	dbPath, err := getDBPath()
	if err != nil {
		return fmt.Errorf("failed to determine database path: %w", err)
	}

	// Create RLM configuration
	cfg := rlm.DefaultConfig()
	cfg.APIKey = apiKey
	cfg.DBPath = dbPath

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

// getDBPath returns the path to the RLM database.
// It uses $XDG_DATA_HOME/rlm/rlm.db or ~/.local/share/rlm/rlm.db as default.
func getDBPath() (string, error) {
	// Check for XDG_DATA_HOME
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		// Fall back to ~/.local/share
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		dataHome = filepath.Join(home, ".local", "share")
	}

	// Create rlm directory
	rlmDir := filepath.Join(dataHome, "rlm")
	if err := os.MkdirAll(rlmDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create data directory: %w", err)
	}

	return filepath.Join(rlmDir, defaultDBName), nil
}
