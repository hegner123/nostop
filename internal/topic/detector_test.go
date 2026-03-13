package topic

import (
	"context"
	"os"
	"testing"

	"github.com/hegner123/nostop/internal/api"
)

// ---------------------------------------------------------------------------
// extractJSON
// ---------------------------------------------------------------------------

func TestExtractJSON_CleanObject(t *testing.T) {
	input := `{"topic_name":"Go concurrency","keywords":["goroutine","channel","mutex"]}`
	got := extractJSON(input)
	if got != input {
		t.Errorf("expected identical JSON, got %q", got)
	}
}

func TestExtractJSON_SurroundingText(t *testing.T) {
	input := `Here is the JSON:\n{"topic_name":"Go concurrency","keywords":["goroutine"]}\nHope this helps!`
	want := `{"topic_name":"Go concurrency","keywords":["goroutine"]}`
	got := extractJSON(input)
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestExtractJSON_NestedBraces(t *testing.T) {
	input := `{"outer":{"inner":"value"}}`
	got := extractJSON(input)
	if got != input {
		t.Errorf("expected full nested JSON, got %q", got)
	}
}

func TestExtractJSON_NoJSON(t *testing.T) {
	input := "This response has no JSON at all."
	got := extractJSON(input)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestExtractJSON_EmptyString(t *testing.T) {
	got := extractJSON("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestExtractJSON_MarkdownCodeBlock(t *testing.T) {
	input := "```json\n{\"topic_name\":\"Testing\",\"keywords\":[\"unit\",\"test\"]}\n```"
	want := `{"topic_name":"Testing","keywords":["unit","test"]}`
	got := extractJSON(input)
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestExtractJSON_UnbalancedBraces(t *testing.T) {
	input := `{"broken": "missing close`
	got := extractJSON(input)
	if got != "" {
		t.Errorf("expected empty for unbalanced braces, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// parseTopicIdentificationResponse
// ---------------------------------------------------------------------------

func TestParseTopicIdentification_Valid(t *testing.T) {
	td := &TopicDetector{}
	messages := []Message{{ID: "msg-1", Role: "user", Content: "hello"}}

	tests := []struct {
		name         string
		input        string
		wantName     string
		wantKeywords int
	}{
		{
			name:         "clean JSON",
			input:        `{"topic_name":"Go error handling","keywords":["errors","wrapping","sentinel"]}`,
			wantName:     "Go error handling",
			wantKeywords: 3,
		},
		{
			name:         "with surrounding text",
			input:        "Here is the analysis:\n{\"topic_name\":\"Database design\",\"keywords\":[\"sql\",\"schema\"]}\n",
			wantName:     "Database design",
			wantKeywords: 2,
		},
		{
			name:         "markdown code block",
			input:        "```json\n{\"topic_name\":\"API design\",\"keywords\":[\"rest\",\"graphql\",\"grpc\",\"openapi\"]}\n```",
			wantName:     "API design",
			wantKeywords: 4,
		},
		{
			name:         "empty keywords",
			input:        `{"topic_name":"General chat","keywords":[]}`,
			wantName:     "General chat",
			wantKeywords: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topic, err := td.parseTopicIdentificationResponse(tt.input, messages)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if topic.Name != tt.wantName {
				t.Errorf("name: got %q, want %q", topic.Name, tt.wantName)
			}
			if len(topic.Keywords) != tt.wantKeywords {
				t.Errorf("keywords count: got %d, want %d", len(topic.Keywords), tt.wantKeywords)
			}
			if topic.StartMsgID != "msg-1" {
				t.Errorf("start msg ID: got %q, want %q", topic.StartMsgID, "msg-1")
			}
			if topic.Relevance != 1.0 {
				t.Errorf("relevance: got %f, want 1.0", topic.Relevance)
			}
		})
	}
}

func TestParseTopicIdentification_Invalid(t *testing.T) {
	td := &TopicDetector{}
	messages := []Message{{ID: "msg-1", Role: "user", Content: "hello"}}

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "no JSON at all",
			input: "I think the topic is about Go programming.",
		},
		{
			name:  "empty string",
			input: "",
		},
		{
			name:  "invalid JSON",
			input: `{"topic_name": broken}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := td.parseTopicIdentificationResponse(tt.input, messages)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseTopicShiftResponse
// ---------------------------------------------------------------------------

func TestParseTopicShift_ShiftDetected(t *testing.T) {
	td := &TopicDetector{}
	input := `{
		"topic_shifted": true,
		"new_topic_name": "Database optimization",
		"keywords": ["indexing", "query", "performance"],
		"confidence": 0.85,
		"reason": "User switched from API design to database tuning"
	}`

	shift, err := td.parseTopicShiftResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !shift.Detected {
		t.Error("expected shift to be detected")
	}
	if shift.NewTopicName != "Database optimization" {
		t.Errorf("topic name: got %q, want %q", shift.NewTopicName, "Database optimization")
	}
	if len(shift.NewKeywords) != 3 {
		t.Errorf("keywords count: got %d, want 3", len(shift.NewKeywords))
	}
	if shift.Confidence != 0.85 {
		t.Errorf("confidence: got %f, want 0.85", shift.Confidence)
	}
}

func TestParseTopicShift_NoShift(t *testing.T) {
	td := &TopicDetector{}
	input := `{
		"topic_shifted": false,
		"new_topic_name": "",
		"keywords": [],
		"confidence": 0.95,
		"reason": "Still discussing the same topic"
	}`

	shift, err := td.parseTopicShiftResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shift.Detected {
		t.Error("expected no shift detected")
	}
}

func TestParseTopicShift_WithSurroundingText(t *testing.T) {
	td := &TopicDetector{}
	input := "Based on the messages, here is my analysis:\n\n```json\n{\"topic_shifted\":true,\"new_topic_name\":\"Testing strategies\",\"keywords\":[\"unit\",\"integration\"],\"confidence\":0.7,\"reason\":\"shifted\"}\n```"

	shift, err := td.parseTopicShiftResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !shift.Detected {
		t.Error("expected shift to be detected")
	}
	if shift.NewTopicName != "Testing strategies" {
		t.Errorf("topic name: got %q, want %q", shift.NewTopicName, "Testing strategies")
	}
}

// ---------------------------------------------------------------------------
// truncateText
// ---------------------------------------------------------------------------

func TestTruncateText(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"needs truncation", "hello world", 8, "hello..."},
		{"very short max", "hello", 2, "he"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateText(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Integration test — calls the real Haiku API
// ---------------------------------------------------------------------------

// TestIdentifyTopic_Integration calls the real Haiku API to verify topic
// identification works end-to-end. Skipped when ANTHROPIC_API_KEY is unset.
func TestIdentifyTopic_Integration(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set — skipping integration test")
	}

	client := api.NewClient(apiKey)
	detector := NewTopicDetector(client, api.ModelHaiku45Latest)

	messages := []Message{
		{ID: "1", Role: "user", Content: "How do I set up a PostgreSQL connection pool in Go using pgxpool?"},
	}

	ctx := context.Background()
	topic, err := detector.IdentifyTopic(ctx, messages)
	if err != nil {
		t.Fatalf("IdentifyTopic failed: %v", err)
	}

	t.Logf("Topic name: %q", topic.Name)
	t.Logf("Keywords:   %v", topic.Keywords)
	t.Logf("Relevance:  %f", topic.Relevance)

	if topic.Name == "" {
		t.Error("expected non-empty topic name")
	}
	if len(topic.Keywords) == 0 {
		t.Error("expected at least one keyword")
	}
}

// TestDetectTopicShift_Integration calls the real Haiku API to verify shift
// detection works end-to-end. Skipped when ANTHROPIC_API_KEY is unset.
func TestDetectTopicShift_Integration(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set — skipping integration test")
	}

	client := api.NewClient(apiKey)
	detector := NewTopicDetector(client, api.ModelHaiku45Latest)

	currentTopic := &Topic{
		Name:     "Go concurrency patterns",
		Keywords: []string{"goroutine", "channel", "mutex", "waitgroup"},
	}

	// Messages that clearly shift topic
	messages := []Message{
		{ID: "1", Role: "user", Content: "Can you explain goroutine leak detection?"},
		{ID: "2", Role: "assistant", Content: "Goroutine leaks occur when..."},
		{ID: "3", Role: "user", Content: "Actually, let's switch gears. How do I write a Dockerfile for a Go microservice with multi-stage builds?"},
	}

	ctx := context.Background()
	shift, err := detector.DetectTopicShift(ctx, messages, currentTopic)
	if err != nil {
		t.Fatalf("DetectTopicShift failed: %v", err)
	}

	t.Logf("Detected:  %v", shift.Detected)
	t.Logf("New topic: %q", shift.NewTopicName)
	t.Logf("Keywords:  %v", shift.NewKeywords)
	t.Logf("Confidence: %f", shift.Confidence)
	t.Logf("Reason:    %q", shift.Reason)

	if !shift.Detected {
		t.Error("expected topic shift to be detected — user explicitly changed subject")
	}
	if shift.NewTopicName == "" {
		t.Error("expected non-empty new topic name")
	}
}
