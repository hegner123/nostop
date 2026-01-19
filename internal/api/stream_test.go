package api

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// mockReadCloser wraps a string reader and implements io.ReadCloser
type mockReadCloser struct {
	*strings.Reader
	closed bool
}

func (m *mockReadCloser) Close() error {
	m.closed = true
	return nil
}

func newMockReadCloser(s string) *mockReadCloser {
	return &mockReadCloser{Reader: strings.NewReader(s)}
}

func TestStreamReader_ParseSSE(t *testing.T) {
	// Test basic SSE parsing with a simple text stream
	sseData := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-20241022","stop_reason":null,"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}

event: message_stop
data: {"type":"message_stop"}

`

	reader := NewStreamReader(newMockReadCloser(sseData))
	defer reader.Close()

	// Collect all events
	var events []*StreamEvent
	for {
		event, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		events = append(events, event)
	}

	// Verify we got the expected number of events (ping is skipped, message_stop is included)
	if len(events) != 7 {
		t.Errorf("expected 7 events, got %d", len(events))
	}

	// Verify event types
	expectedTypes := []StreamEventType{
		StreamEventMessageStart,
		StreamEventContentBlockStart,
		StreamEventContentBlockDelta,
		StreamEventContentBlockDelta,
		StreamEventContentBlockStop,
		StreamEventMessageDelta,
		StreamEventMessageStop,
	}

	for i, expected := range expectedTypes {
		if i >= len(events) {
			break
		}
		if events[i].Type != expected {
			t.Errorf("event %d: expected type %s, got %s", i, expected, events[i].Type)
		}
	}

	// Verify message_start event
	if events[0].Message == nil {
		t.Error("message_start should have Message field")
	} else {
		if events[0].Message.ID != "msg_123" {
			t.Errorf("expected message ID msg_123, got %s", events[0].Message.ID)
		}
	}

	// Verify content_block_delta events
	if events[2].Delta == nil {
		t.Error("content_block_delta should have Delta field")
	} else {
		if events[2].Delta.Text != "Hello" {
			t.Errorf("expected delta text 'Hello', got '%s'", events[2].Delta.Text)
		}
	}
}

func TestStreamReader_Collect(t *testing.T) {
	// Test Collect method assembles the full response
	sseData := `event: message_start
data: {"type":"message_start","message":{"id":"msg_456","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-20241022","stop_reason":null,"usage":{"input_tokens":15,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello, "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world!"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}

event: message_stop
data: {"type":"message_stop"}

`

	reader := NewStreamReader(newMockReadCloser(sseData))
	response, err := reader.Collect()
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	// Verify response
	if response.ID != "msg_456" {
		t.Errorf("expected message ID msg_456, got %s", response.ID)
	}

	if response.StopReason != StopReasonEndTurn {
		t.Errorf("expected stop_reason end_turn, got %s", response.StopReason)
	}

	if len(response.Content) != 1 {
		t.Errorf("expected 1 content block, got %d", len(response.Content))
	}

	if response.Content[0].Text != "Hello, world!" {
		t.Errorf("expected text 'Hello, world!', got '%s'", response.Content[0].Text)
	}
}

func TestStreamReader_Close(t *testing.T) {
	mock := newMockReadCloser("event: ping\ndata: {}\n\n")
	reader := NewStreamReader(mock)

	if mock.closed {
		t.Error("reader should not be closed yet")
	}

	if err := reader.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	if !mock.closed {
		t.Error("underlying reader should be closed")
	}

	// Calling Next after Close should return EOF
	_, err := reader.Next()
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected EOF after Close, got %v", err)
	}
}

func TestStreamReader_PingEventsSkipped(t *testing.T) {
	sseData := `event: ping
data: {"type":"ping"}

event: message_start
data: {"type":"message_start","message":{"id":"msg_789","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-20241022","stop_reason":null,"usage":{"input_tokens":5,"output_tokens":0}}}

event: ping
data: {"type":"ping"}

event: message_stop
data: {"type":"message_stop"}

`

	reader := NewStreamReader(newMockReadCloser(sseData))
	defer reader.Close()

	// First event should be message_start (pings are skipped)
	event, err := reader.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != StreamEventMessageStart {
		t.Errorf("expected message_start, got %s", event.Type)
	}

	// Second event should be message_stop (ping is skipped)
	event, err = reader.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event.Type != StreamEventMessageStop {
		t.Errorf("expected message_stop, got %s", event.Type)
	}
}

func TestStreamReader_ErrorEvent(t *testing.T) {
	sseData := `event: error
data: {"type":"error","error":{"type":"rate_limit_error","message":"Rate limit exceeded"}}

`

	reader := NewStreamReader(newMockReadCloser(sseData))
	defer reader.Close()

	event, err := reader.Next()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}

	if apiErr.ErrorDetails.Type != ErrorTypeRateLimit {
		t.Errorf("expected rate_limit_error, got %s", apiErr.ErrorDetails.Type)
	}

	if event == nil {
		t.Error("expected event to be returned with error")
	}
	if event != nil && event.Type != StreamEventError {
		t.Errorf("expected error event, got %s", event.Type)
	}
}

func TestStreamReader_ToolUseStream(t *testing.T) {
	// Test tool use streaming with input_json_delta
	sseData := `event: message_start
data: {"type":"message_start","message":{"id":"msg_tool","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-20241022","stop_reason":null,"usage":{"input_tokens":20,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_123","name":"get_weather"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"loc"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"ation\": \"NYC\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":10}}

event: message_stop
data: {"type":"message_stop"}

`

	reader := NewStreamReader(newMockReadCloser(sseData))
	response, err := reader.Collect()
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if response.StopReason != StopReasonToolUse {
		t.Errorf("expected stop_reason tool_use, got %s", response.StopReason)
	}

	if len(response.Content) != 1 {
		t.Errorf("expected 1 content block, got %d", len(response.Content))
	}

	// The partial JSON should be accumulated
	expectedJSON := `{"location": "NYC"}`
	if string(response.Content[0].Input) != expectedJSON {
		t.Errorf("expected input JSON '%s', got '%s'", expectedJSON, string(response.Content[0].Input))
	}
}

func TestStreamReader_MultipleContentBlocks(t *testing.T) {
	// Test stream with multiple content blocks
	sseData := `event: message_start
data: {"type":"message_start","message":{"id":"msg_multi","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-20241022","stop_reason":null,"usage":{"input_tokens":25,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"First block"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Second block"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}

event: message_stop
data: {"type":"message_stop"}

`

	reader := NewStreamReader(newMockReadCloser(sseData))
	response, err := reader.Collect()
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if len(response.Content) != 2 {
		t.Errorf("expected 2 content blocks, got %d", len(response.Content))
	}

	if response.Content[0].Text != "First block" {
		t.Errorf("expected first block text 'First block', got '%s'", response.Content[0].Text)
	}

	if response.Content[1].Text != "Second block" {
		t.Errorf("expected second block text 'Second block', got '%s'", response.Content[1].Text)
	}
}
