package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// mockMCPServer is a bash script that acts as a minimal MCP server.
// It handles initialize, tools/list, and tools/call methods.
const mockMCPServer = `
while IFS= read -r line; do
    method=$(echo "$line" | python3 -c "import sys,json; print(json.loads(sys.stdin.read())['method'])" 2>/dev/null)
    id=$(echo "$line" | python3 -c "import sys,json; print(json.loads(sys.stdin.read())['id'])" 2>/dev/null)

    case "$method" in
        "initialize")
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"protocolVersion\":\"2025-03-26\",\"capabilities\":{\"tools\":{}},\"serverInfo\":{\"name\":\"mock-server\",\"version\":\"1.0.0\"}}}"
            ;;
        "tools/list")
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"tools\":[{\"name\":\"echo\",\"description\":\"Echoes input\",\"inputSchema\":{\"type\":\"object\",\"properties\":{\"text\":{\"type\":\"string\"}}}}]}}"
            ;;
        "tools/call")
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"hello from mock\"}]}}"
            ;;
        "ping")
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}"
            ;;
        *)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}"
            ;;
    esac
done
`

func TestManagerStartAndStop(t *testing.T) {
	mgr := NewServerManager("nostop-test", "0.0.1")

	cfg := &MCPConfig{
		MCPServers: map[string]ServerConfig{
			"mock": {
				Command: "/bin/bash",
				Args:    []string{"-c", mockMCPServer},
				Timeout: "5s",
			},
		},
	}
	mgr.LoadConfig(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("StartAll: %v", err)
	}

	// Check status
	status := mgr.Status()
	if status["mock"] != ServerReady {
		t.Errorf("mock status = %v, want Ready", status["mock"])
	}

	// Check tools were discovered
	tools := mgr.AllTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "echo" {
		t.Errorf("tool name = %q, want %q", tools[0].Name, "echo")
	}

	// Check server info
	info, ok := mgr.ServerInfo("mock")
	if !ok {
		t.Fatal("ServerInfo returned false")
	}
	if info.Status != ServerReady {
		t.Errorf("info.Status = %v, want Ready", info.Status)
	}

	// Stop
	if err := mgr.StopAll(); err != nil {
		t.Errorf("StopAll: %v", err)
	}

	status = mgr.Status()
	if status["mock"] != ServerStopped {
		t.Errorf("mock status after stop = %v, want Stopped", status["mock"])
	}
}

func TestManagerCallTool(t *testing.T) {
	mgr := NewServerManager("nostop-test", "0.0.1")

	cfg := &MCPConfig{
		MCPServers: map[string]ServerConfig{
			"mock": {
				Command: "/bin/bash",
				Args:    []string{"-c", mockMCPServer},
				Timeout: "5s",
			},
		},
	}
	mgr.LoadConfig(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	defer mgr.StopAll()

	result, err := mgr.CallTool(ctx, "mock", "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if result.IsError {
		t.Error("unexpected tool error")
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Text != "hello from mock" {
		t.Errorf("content text = %q, want %q", result.Content[0].Text, "hello from mock")
	}
}

func TestManagerCallToolUnknownServer(t *testing.T) {
	mgr := NewServerManager("nostop-test", "0.0.1")

	ctx := context.Background()
	_, err := mgr.CallTool(ctx, "nonexistent", "tool", nil)
	if err == nil {
		t.Fatal("expected error for unknown server")
	}
}

func TestManagerDisabledServerSkipped(t *testing.T) {
	mgr := NewServerManager("nostop-test", "0.0.1")

	cfg := &MCPConfig{
		MCPServers: map[string]ServerConfig{
			"disabled": {
				Command:  "/bin/echo",
				Disabled: true,
			},
			"active": {
				Command: "/bin/bash",
				Args:    []string{"-c", mockMCPServer},
				Timeout: "5s",
			},
		},
	}
	mgr.LoadConfig(cfg)

	// Disabled server should not be loaded
	names := mgr.ServerNames()
	for _, name := range names {
		if name == "disabled" {
			t.Error("disabled server should not be loaded")
		}
	}
}

func TestManagerServerNames(t *testing.T) {
	mgr := NewServerManager("nostop-test", "0.0.1")

	cfg := &MCPConfig{
		MCPServers: map[string]ServerConfig{
			"alpha": {Command: "/bin/echo"},
			"beta":  {Command: "/bin/echo"},
		},
	}
	mgr.LoadConfig(cfg)

	names := mgr.ServerNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}

	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["alpha"] || !nameSet["beta"] {
		t.Errorf("names = %v, want alpha and beta", names)
	}
}

func TestManagerStartFailedServer(t *testing.T) {
	mgr := NewServerManager("nostop-test", "0.0.1")

	cfg := &MCPConfig{
		MCPServers: map[string]ServerConfig{
			"bad": {
				Command: "/nonexistent/binary/path",
				Timeout: "2s",
			},
		},
	}
	mgr.LoadConfig(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// StartAll should not return error (failures are collected, not propagated)
	mgr.StartAll(ctx)

	status := mgr.Status()
	if status["bad"] != ServerError {
		t.Errorf("bad server status = %v, want Error", status["bad"])
	}
}

// TestClientInitializeIntegration tests the full Initialize → ListTools → CallTool flow.
func TestClientInitializeIntegration(t *testing.T) {
	transport, err := NewStdioTransport("/bin/bash", []string{"-c", mockMCPServer}, nil)
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}
	defer transport.Close()

	client := NewClient(transport)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Initialize
	info, err := client.Initialize(ctx, "test-client", "0.1.0")
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if info.Name != "mock-server" {
		t.Errorf("server name = %q, want %q", info.Name, "mock-server")
	}
	if info.Version != "1.0.0" {
		t.Errorf("server version = %q, want %q", info.Version, "1.0.0")
	}
	if info.Capabilities.Tools == nil {
		t.Error("expected tools capability")
	}

	// ListTools
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	// Verify tool schema
	toolSchema, err := json.Marshal(tools[0].InputSchema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	if string(toolSchema) == "" {
		t.Error("expected non-empty input schema")
	}

	// CallTool
	result, err := client.CallTool(ctx, "echo", map[string]any{"text": "test"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Errorf("unexpected result: %+v", result)
	}

	// Ping
	if err := client.Ping(ctx); err != nil {
		t.Errorf("Ping: %v", err)
	}

	// Info accessor
	storedInfo := client.Info()
	if storedInfo.Name != "mock-server" {
		t.Errorf("Info().Name = %q", storedInfo.Name)
	}

	fmt.Printf("Integration test passed: server=%s, tools=%d\n", info.Name, len(tools))
}
