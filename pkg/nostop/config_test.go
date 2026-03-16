package nostop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultFileConfig(t *testing.T) {
	cfg := DefaultFileConfig()

	if cfg.Context.MaxTokens != 200000 {
		t.Errorf("expected MaxTokens=200000, got %d", cfg.Context.MaxTokens)
	}

	if cfg.Context.ArchiveThreshold != ThresholdArchive {
		t.Errorf("expected ArchiveThreshold=%v, got %v", ThresholdArchive, cfg.Context.ArchiveThreshold)
	}

	if cfg.Context.ArchiveTarget != ArchiveTarget {
		t.Errorf("expected ArchiveTarget=%v, got %v", ArchiveTarget, cfg.Context.ArchiveTarget)
	}

	if cfg.API.Timeout != 120 {
		t.Errorf("expected Timeout=120, got %d", cfg.API.Timeout)
	}

	if cfg.UI.Theme != "dark" {
		t.Errorf("expected Theme=dark, got %s", cfg.UI.Theme)
	}
}

func TestLoadConfigNoFile(t *testing.T) {
	// When no config file exists, should return defaults
	cfg, path, err := LoadConfigWithPath("/nonexistent/path.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
	if cfg != nil {
		t.Error("expected nil config on error")
	}
	if path != "" {
		t.Errorf("expected empty path on error, got %s", path)
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	// Create a temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test.json")

	content := `{
  "api": {
    "timeout": 60
  },
  "context": {
    "max_tokens": 100000,
    "archive_threshold": 0.90,
    "archive_target": 0.40
  },
  "models": {
    "chat": "claude-test-model",
    "detection": "claude-test-haiku"
  },
  "ui": {
    "theme": "light",
    "auto_refresh": false
  }
}`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, path, err := LoadConfigWithPath(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if path != configPath {
		t.Errorf("expected path=%s, got %s", configPath, path)
	}

	if cfg.API.Timeout != 60 {
		t.Errorf("expected Timeout=60, got %d", cfg.API.Timeout)
	}

	if cfg.Context.MaxTokens != 100000 {
		t.Errorf("expected MaxTokens=100000, got %d", cfg.Context.MaxTokens)
	}

	if cfg.Context.ArchiveThreshold != 0.90 {
		t.Errorf("expected ArchiveThreshold=0.90, got %v", cfg.Context.ArchiveThreshold)
	}

	if cfg.Models.Chat != "claude-test-model" {
		t.Errorf("expected Chat=claude-test-model, got %s", cfg.Models.Chat)
	}

	if cfg.UI.Theme != "light" {
		t.Errorf("expected Theme=light, got %s", cfg.UI.Theme)
	}

	if cfg.UI.AutoRefresh != false {
		t.Error("expected AutoRefresh=false")
	}
}

func TestToNostopConfig(t *testing.T) {
	// Clear env so it doesn't override the config file value
	t.Setenv("ANTHROPIC_API_KEY", "")

	fileCfg := FileConfig{
		API: APIConfig{
			Key:     "test-key",
			Timeout: 60,
		},
		Context: ContextConfig{
			MaxTokens:        150000,
			ArchiveThreshold: 0.85,
			ArchiveTarget:    0.45,
		},
		Models: ModelsConfig{
			Chat:      "test-chat-model",
			Detection: "test-detection-model",
		},
		Database: DatabaseConfig{
			Path: "/tmp/test.db",
		},
	}

	cfg := fileCfg.ToNostopConfig()

	if cfg.APIKey != "test-key" {
		t.Errorf("expected APIKey=test-key, got %s", cfg.APIKey)
	}

	if cfg.MaxContextTokens != 150000 {
		t.Errorf("expected MaxContextTokens=150000, got %d", cfg.MaxContextTokens)
	}

	if cfg.ChatModel != "test-chat-model" {
		t.Errorf("expected ChatModel=test-chat-model, got %s", cfg.ChatModel)
	}

	if cfg.DBPath != "/tmp/test.db" {
		t.Errorf("expected DBPath=/tmp/test.db, got %s", cfg.DBPath)
	}
}

func TestToNostopConfigEnvOverride(t *testing.T) {
	// Set environment variable
	origKey := os.Getenv("ANTHROPIC_API_KEY")
	os.Setenv("ANTHROPIC_API_KEY", "env-override-key")
	defer func() {
		if origKey != "" {
			os.Setenv("ANTHROPIC_API_KEY", origKey)
		} else {
			os.Unsetenv("ANTHROPIC_API_KEY")
		}
	}()

	fileCfg := FileConfig{
		API: APIConfig{
			Key: "config-key",
		},
	}

	cfg := fileCfg.ToNostopConfig()

	// Environment variable should override config file value
	if cfg.APIKey != "env-override-key" {
		t.Errorf("expected APIKey=env-override-key (from env), got %s", cfg.APIKey)
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home directory")
	}

	tests := []struct {
		input    string
		expected string
	}{
		{"~/test.db", filepath.Join(home, "test.db")},
		{"/absolute/path.db", "/absolute/path.db"},
		{"relative/path.db", "relative/path.db"},
	}

	for _, tt := range tests {
		result := expandPath(tt.input)
		if result != tt.expected {
			t.Errorf("expandPath(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name        string
		cfg         FileConfig
		expectError bool
	}{
		{
			name:        "valid config",
			cfg:         DefaultFileConfig(),
			expectError: false,
		},
		{
			name: "invalid max_tokens",
			cfg: FileConfig{
				Context: ContextConfig{
					MaxTokens:        0,
					ArchiveThreshold: 0.95,
					ArchiveTarget:    0.50,
				},
				API: APIConfig{Timeout: 60},
			},
			expectError: true,
		},
		{
			name: "threshold >= 1",
			cfg: FileConfig{
				Context: ContextConfig{
					MaxTokens:        200000,
					ArchiveThreshold: 1.5,
					ArchiveTarget:    0.50,
				},
				API: APIConfig{Timeout: 60},
			},
			expectError: true,
		},
		{
			name: "target >= threshold",
			cfg: FileConfig{
				Context: ContextConfig{
					MaxTokens:        200000,
					ArchiveThreshold: 0.50,
					ArchiveTarget:    0.60,
				},
				API: APIConfig{Timeout: 60},
			},
			expectError: true,
		},
		{
			name: "invalid theme",
			cfg: FileConfig{
				Context: ContextConfig{
					MaxTokens:        200000,
					ArchiveThreshold: 0.95,
					ArchiveTarget:    0.50,
				},
				API: APIConfig{Timeout: 60},
				UI:  UIConfig{Theme: "invalid"},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateConfig(&tt.cfg)
			hasErrors := len(errs) > 0
			if hasErrors != tt.expectError {
				t.Errorf("ValidateConfig() returned %d errors, expectError=%v", len(errs), tt.expectError)
				for _, e := range errs {
					t.Logf("  error: %v", e)
				}
			}
		})
	}
}

func TestWriteDefaultConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "subdir", "config.json")

	err := WriteDefaultConfig(configPath)
	if err != nil {
		t.Fatalf("WriteDefaultConfig failed: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("config file was not created")
	}

	// Verify content can be parsed
	cfg, _, err := LoadConfigWithPath(configPath)
	if err != nil {
		t.Fatalf("failed to load written config: %v", err)
	}

	// Should match defaults
	if cfg.Context.MaxTokens != 200000 {
		t.Errorf("expected MaxTokens=200000, got %d", cfg.Context.MaxTokens)
	}

	// Writing again should fail (file exists)
	err = WriteDefaultConfig(configPath)
	if err == nil {
		t.Error("expected error when file already exists")
	}
}
