package mcp

import (
	"encoding/json"
	"testing"
)

func TestRequestMarshalRoundTrip(t *testing.T) {
	req := NewRequest(int64(1), "tools/list", nil)

	data, err := MarshalRequest(req)
	if err != nil {
		t.Fatalf("MarshalRequest: %v", err)
	}

	// Should end with newline
	if data[len(data)-1] != '\n' {
		t.Error("marshaled request should end with newline")
	}

	// Unmarshal back (without trailing newline)
	var parsed Request
	if err := json.Unmarshal(data[:len(data)-1], &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if parsed.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want %q", parsed.JSONRPC, "2.0")
	}
	if parsed.Method != "tools/list" {
		t.Errorf("method = %q, want %q", parsed.Method, "tools/list")
	}
}

func TestRequestWithParams(t *testing.T) {
	params := map[string]any{
		"name":      "stump",
		"arguments": map[string]any{"dir": "/tmp"},
	}
	req := NewRequest(int64(42), "tools/call", params)

	data, err := MarshalRequest(req)
	if err != nil {
		t.Fatalf("MarshalRequest: %v", err)
	}

	var parsed Request
	if err := json.Unmarshal(data[:len(data)-1], &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if parsed.Params["name"] != "stump" {
		t.Errorf("params.name = %v, want %q", parsed.Params["name"], "stump")
	}
}

func TestResponseUnmarshal(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		raw := `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"stump"}]}}`
		resp, err := UnmarshalResponse([]byte(raw))
		if err != nil {
			t.Fatalf("UnmarshalResponse: %v", err)
		}
		if resp.Error != nil {
			t.Error("expected no error")
		}
		if resp.Result == nil {
			t.Fatal("expected result")
		}
	})

	t.Run("error", func(t *testing.T) {
		raw := `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`
		resp, err := UnmarshalResponse([]byte(raw))
		if err != nil {
			t.Fatalf("UnmarshalResponse: %v", err)
		}
		if resp.Error == nil {
			t.Fatal("expected error")
		}
		if resp.Error.Code != -32601 {
			t.Errorf("error code = %d, want %d", resp.Error.Code, -32601)
		}
	})

	t.Run("string_id", func(t *testing.T) {
		raw := `{"jsonrpc":"2.0","id":"abc-123","result":{}}`
		resp, err := UnmarshalResponse([]byte(raw))
		if err != nil {
			t.Fatalf("UnmarshalResponse: %v", err)
		}
		if resp.ID != "abc-123" {
			t.Errorf("id = %v, want %q", resp.ID, "abc-123")
		}
	})
}

func TestNotificationMarshal(t *testing.T) {
	notif := NewNotification("notifications/initialized", nil)

	data, err := MarshalNotification(notif)
	if err != nil {
		t.Fatalf("MarshalNotification: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Notifications must not have an "id" field
	if _, hasID := parsed["id"]; hasID {
		t.Error("notification should not have an id field")
	}

	if parsed["method"] != "notifications/initialized" {
		t.Errorf("method = %v, want %q", parsed["method"], "notifications/initialized")
	}
}

func TestRPCErrorFormat(t *testing.T) {
	err := &RPCError{Code: -32601, Message: "method not found"}
	got := err.Error()
	want := "JSON-RPC error -32601: method not found"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	errWithData := &RPCError{Code: -32602, Message: "invalid params", Data: "missing field"}
	got = errWithData.Error()
	if got != "JSON-RPC error -32602: invalid params (data: missing field)" {
		t.Errorf("Error() = %q", got)
	}
}

func TestIdToString(t *testing.T) {
	tests := []struct {
		id   any
		want string
	}{
		{"abc", "s:abc"},
		{float64(42), "n:42"},
		{int64(7), "n:7"},
		{int(3), "n:3"},
		{nil, "null"},
	}

	for _, tt := range tests {
		got := idToString(tt.id)
		if got != tt.want {
			t.Errorf("idToString(%v) = %q, want %q", tt.id, got, tt.want)
		}
	}
}
