package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigValid(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".mcp.json")
	os.WriteFile(cfgPath, []byte(`{
		"mcpServers": {
			"stump": {
				"command": "stump",
				"args": ["--mcp"]
			},
			"remote": {
				"url": "https://mcp.example.com",
				"headers": {"Authorization": "Bearer token123"},
				"timeout": "10s"
			}
		}
	}`), 0644)

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if len(cfg.MCPServers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.MCPServers))
	}

	stump := cfg.MCPServers["stump"]
	if stump.Command != "stump" {
		t.Errorf("stump.Command = %q, want %q", stump.Command, "stump")
	}
	if stump.TransportType() != "stdio" {
		t.Errorf("stump.TransportType() = %q, want %q", stump.TransportType(), "stdio")
	}

	remote := cfg.MCPServers["remote"]
	if remote.URL != "https://mcp.example.com" {
		t.Errorf("remote.URL = %q", remote.URL)
	}
	if remote.TransportType() != "http" {
		t.Errorf("remote.TransportType() = %q, want %q", remote.TransportType(), "http")
	}
	if remote.ParseTimeout().Seconds() != 10 {
		t.Errorf("remote.ParseTimeout() = %v, want 10s", remote.ParseTimeout())
	}
}

func TestLoadConfigRejectsDoubleUnderscoreInName(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".mcp.json")
	os.WriteFile(cfgPath, []byte(`{
		"mcpServers": {
			"bad__name": {
				"command": "test"
			}
		}
	}`), 0644)

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected error for server name containing __")
	}
}

func TestLoadConfigRejectsBothCommandAndURL(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".mcp.json")
	os.WriteFile(cfgPath, []byte(`{
		"mcpServers": {
			"bad": {
				"command": "test",
				"url": "https://example.com"
			}
		}
	}`), 0644)

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected error for server with both command and url")
	}
}

func TestLoadConfigRejectsNeitherCommandNorURL(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".mcp.json")
	os.WriteFile(cfgPath, []byte(`{
		"mcpServers": {
			"empty": {}
		}
	}`), 0644)

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected error for server with neither command nor url")
	}
}

func TestEnvVarExpansion(t *testing.T) {
	t.Run("simple", func(t *testing.T) {
		t.Setenv("TEST_MCP_TOKEN", "secret123")
		result := expandEnvVars(`Bearer ${TEST_MCP_TOKEN}`)
		if result != "Bearer secret123" {
			t.Errorf("got %q, want %q", result, "Bearer secret123")
		}
	})

	t.Run("with_default", func(t *testing.T) {
		result := expandEnvVars(`${NONEXISTENT_VAR_ABC:-fallback_value}`)
		if result != "fallback_value" {
			t.Errorf("got %q, want %q", result, "fallback_value")
		}
	})

	t.Run("existing_var_ignores_default", func(t *testing.T) {
		t.Setenv("TEST_MCP_EXISTING", "real_value")
		result := expandEnvVars(`${TEST_MCP_EXISTING:-ignored}`)
		if result != "real_value" {
			t.Errorf("got %q, want %q", result, "real_value")
		}
	})

	t.Run("unset_no_default_unchanged", func(t *testing.T) {
		result := expandEnvVars(`${TOTALLY_UNKNOWN_VAR_XYZ}`)
		if result != "${TOTALLY_UNKNOWN_VAR_XYZ}" {
			t.Errorf("got %q, want original placeholder", result)
		}
	})

	t.Run("in_json_context", func(t *testing.T) {
		t.Setenv("TEST_MCP_URL", "https://mcp.example.com")
		input := `{"url": "${TEST_MCP_URL}", "token": "${MISSING:-default_tok}"}`
		result := expandEnvVars(input)
		want := `{"url": "https://mcp.example.com", "token": "default_tok"}`
		if result != want {
			t.Errorf("got %q, want %q", result, want)
		}
	})
}

func TestMergeConfigs(t *testing.T) {
	cfg1 := &MCPConfig{
		MCPServers: map[string]ServerConfig{
			"stump": {Command: "stump-v2"},
			"sig":   {Command: "sig"},
		},
	}
	cfg2 := &MCPConfig{
		MCPServers: map[string]ServerConfig{
			"stump":  {Command: "stump-v1"}, // should be overridden by cfg1
			"repfor": {Command: "repfor"},
		},
	}

	merged := MergeConfigs(cfg1, cfg2)

	if len(merged.MCPServers) != 3 {
		t.Fatalf("expected 3 servers, got %d", len(merged.MCPServers))
	}

	// cfg1's stump should win
	if merged.MCPServers["stump"].Command != "stump-v2" {
		t.Errorf("stump.Command = %q, want %q (cfg1 should take precedence)",
			merged.MCPServers["stump"].Command, "stump-v2")
	}

	// cfg2's repfor should be included
	if merged.MCPServers["repfor"].Command != "repfor" {
		t.Errorf("repfor.Command = %q, want %q", merged.MCPServers["repfor"].Command, "repfor")
	}
}

func TestMergeConfigsWithNil(t *testing.T) {
	cfg := &MCPConfig{
		MCPServers: map[string]ServerConfig{
			"stump": {Command: "stump"},
		},
	}

	merged := MergeConfigs(nil, cfg, nil)
	if len(merged.MCPServers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(merged.MCPServers))
	}
}

func TestDefaultTimeout(t *testing.T) {
	sc := ServerConfig{Command: "test"}
	if sc.ParseTimeout().Seconds() != 30 {
		t.Errorf("default timeout = %v, want 30s", sc.ParseTimeout())
	}
}

func TestDisabledServer(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".mcp.json")
	os.WriteFile(cfgPath, []byte(`{
		"mcpServers": {
			"disabled-server": {
				"command": "test",
				"disabled": true
			},
			"active-server": {
				"command": "test2"
			}
		}
	}`), 0644)

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Both should parse (disabled is just a flag, not a validation error)
	if len(cfg.MCPServers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.MCPServers))
	}

	if !cfg.MCPServers["disabled-server"].Disabled {
		t.Error("disabled-server should be disabled")
	}
	if cfg.MCPServers["active-server"].Disabled {
		t.Error("active-server should not be disabled")
	}
}
