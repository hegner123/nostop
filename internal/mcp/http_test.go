package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPTransportSend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %s, want application/json", r.Header.Get("Content-Type"))
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}

		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}

		resp := Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": []any{
					map[string]any{"name": "test-tool", "description": "A test tool"},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	transport := NewHTTPTransport(server.URL, nil, 0)
	ctx := context.Background()

	req := NewRequest(int64(1), "tools/list", nil)
	resp, err := transport.Send(ctx, req)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected result")
	}
}

func TestHTTPTransportCustomHeaders(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")

		resp := Response{JSONRPC: "2.0", ID: float64(1), Result: map[string]any{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	headers := map[string]string{
		"Authorization": "Bearer test-token",
	}
	transport := NewHTTPTransport(server.URL, headers, 0)

	req := NewRequest(int64(1), "ping", nil)
	_, err := transport.Send(context.Background(), req)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-token")
	}
}

func TestHTTPTransportErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	transport := NewHTTPTransport(server.URL, nil, 0)
	req := NewRequest(int64(1), "tools/list", nil)

	_, err := transport.Send(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestHTTPTransportClose(t *testing.T) {
	transport := NewHTTPTransport("http://localhost:9999", nil, 0)
	if err := transport.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
