package mcp

import (
	"context"
	"testing"
	"time"
)

func TestStdioTransportEchoServer(t *testing.T) {
	// Use a simple shell script as a mock MCP server that echoes back
	// a valid JSON-RPC response for any request.
	// The script reads a line from stdin, extracts the id, and returns a result.
	script := `while IFS= read -r line; do
id=$(echo "$line" | python3 -c "import sys,json; print(json.loads(sys.stdin.read())['id'])" 2>/dev/null || echo "1")
echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"status\":\"ok\"}}"
done`

	transport, err := NewStdioTransport("/bin/bash", []string{"-c", script}, nil)
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}
	defer transport.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := NewRequest(int64(1), "ping", nil)
	resp, err := transport.Send(ctx, req)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	status, ok := resp.Result["status"].(string)
	if !ok || status != "ok" {
		t.Errorf("result.status = %v, want %q", resp.Result["status"], "ok")
	}
}

func TestStdioTransportProcessExit(t *testing.T) {
	// Server that exits immediately
	transport, err := NewStdioTransport("/bin/bash", []string{"-c", "exit 0"}, nil)
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}
	defer transport.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := NewRequest(int64(1), "ping", nil)
	_, err = transport.Send(ctx, req)
	if err == nil {
		t.Fatal("expected error when server exits immediately")
	}
}

func TestStdioTransportCloseIdempotent(t *testing.T) {
	transport, err := NewStdioTransport("/bin/cat", nil, nil)
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}

	if err := transport.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Second close should be a no-op
	if err := transport.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestStdioTransportSendAfterClose(t *testing.T) {
	transport, err := NewStdioTransport("/bin/cat", nil, nil)
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}
	transport.Close()

	ctx := context.Background()
	req := NewRequest(int64(1), "ping", nil)
	_, err = transport.Send(ctx, req)
	if err == nil {
		t.Fatal("expected error when sending after close")
	}
}
