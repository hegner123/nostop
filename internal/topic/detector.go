// Package topic provides topic detection and tracking for conversation context management.
package topic

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hegner123/nostop/internal/api"
)

// Default model for topic detection (fast and cheap).
const DefaultDetectionModel = api.ModelHaiku45Latest

// TopicDetector uses Claude to detect and identify conversation topics.
type TopicDetector struct {
	client *api.Client
	model  string
}

// NewTopicDetector creates a new topic detector.
// If model is empty, uses claude-haiku-4-5-20251001 for fast, cheap detection.
func NewTopicDetector(client *api.Client, model string) *TopicDetector {
	if model == "" {
		model = DefaultDetectionModel
	}
	return &TopicDetector{
		client: client,
		model:  model,
	}
}

// Topic represents a conversation topic with its metadata.
type Topic struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Keywords   []string   `json:"keywords"`
	StartMsgID string     `json:"start_msg_id"`
	EndMsgID   *string    `json:"end_msg_id,omitempty"` // nil if topic is current
	TokenCount int        `json:"token_count"`
	Relevance  float64    `json:"relevance"` // 0.0-1.0
	CreatedAt  time.Time  `json:"created_at"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
}

// IsActive returns true if the topic is currently active (not ended).
func (t *Topic) IsActive() bool {
	return t.EndMsgID == nil
}

// IsArchived returns true if the topic has been archived.
func (t *Topic) IsArchived() bool {
	return t.ArchivedAt != nil
}

// TopicShift represents a detected shift in conversation topic.
type TopicShift struct {
	Detected     bool     `json:"detected"`
	NewTopicName string   `json:"new_topic_name,omitempty"`
	NewKeywords  []string `json:"new_keywords,omitempty"`
	Confidence   float64  `json:"confidence"` // 0.0-1.0
	Reason       string   `json:"reason,omitempty"`
}

// Message is a simplified message for topic analysis.
// This avoids a dependency on storage.Message.
type Message struct {
	ID      string `json:"id"`
	Role    string `json:"role"` // "user" or "assistant"
	Content string `json:"content"`
}

// topicDetectionResponse is the expected JSON response from Claude for topic detection.
type topicDetectionResponse struct {
	TopicShifted bool     `json:"topic_shifted"`
	NewTopicName string   `json:"new_topic_name"`
	Keywords     []string `json:"keywords"`
	Confidence   float64  `json:"confidence"`
	Reason       string   `json:"reason"`
}

// topicIdentificationResponse is the expected JSON response from Claude for topic identification.
type topicIdentificationResponse struct {
	TopicName string   `json:"topic_name"`
	Keywords  []string `json:"keywords"`
}

// DetectTopicShift analyzes recent messages to identify if the conversation topic has changed.
// It compares the last 3-5 messages against the current topic context.
func (td *TopicDetector) DetectTopicShift(ctx context.Context, recentMessages []Message, currentTopic *Topic) (*TopicShift, error) {
	if len(recentMessages) == 0 {
		return &TopicShift{Detected: false}, nil
	}

	// Build the analysis prompt
	prompt := td.buildTopicShiftPrompt(recentMessages, currentTopic)

	// Create the request
	req := &api.Request{
		Model:     td.model,
		MaxTokens: 500,
		Messages: []api.MessageParam{
			api.NewUserMessage(prompt),
		},
	}

	// Send to Claude
	resp, err := td.client.Send(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("send topic detection request: %w", err)
	}

	// Parse the response
	text := resp.GetText()
	return td.parseTopicShiftResponse(text)
}

// IdentifyTopic identifies the main topic from a set of messages.
// Used for initial topic identification when starting a new conversation or topic.
func (td *TopicDetector) IdentifyTopic(ctx context.Context, messages []Message) (*Topic, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("no messages to analyze")
	}

	// Build the identification prompt
	prompt := td.buildTopicIdentificationPrompt(messages)

	// Create the request
	req := &api.Request{
		Model:     td.model,
		MaxTokens: 300,
		Messages: []api.MessageParam{
			api.NewUserMessage(prompt),
		},
	}

	// Send to Claude
	resp, err := td.client.Send(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("send topic identification request: %w", err)
	}

	// Parse the response
	text := resp.GetText()
	return td.parseTopicIdentificationResponse(text, messages)
}

// buildTopicShiftPrompt constructs the prompt for topic shift detection.
func (td *TopicDetector) buildTopicShiftPrompt(messages []Message, currentTopic *Topic) string {
	// Format the messages for analysis
	var messagesText string
	for i, msg := range messages {
		messagesText += fmt.Sprintf("%d. [%s]: %s\n", i+1, msg.Role, truncateText(msg.Content, 500))
	}

	// Include current topic context if available
	var currentTopicContext string
	if currentTopic != nil {
		currentTopicContext = fmt.Sprintf(`
Current Topic:
- Name: %s
- Keywords: %v
`, currentTopic.Name, currentTopic.Keywords)
	} else {
		currentTopicContext = "No current topic established."
	}

	return fmt.Sprintf(`You are a topic analysis assistant. Analyze the following conversation messages to determine if the topic has shifted.

%s

Recent Messages:
%s

Task: Determine if the conversation has shifted to a new topic.

A topic shift occurs when:
- The user explicitly introduces a new subject
- The conversation direction changes significantly
- A new domain or area of discussion begins

A topic shift does NOT occur when:
- The conversation continues on the same general subject
- The user asks follow-up questions on the same topic
- There are minor tangents that relate to the main topic

Respond with ONLY valid JSON in this exact format:
{
  "topic_shifted": true/false,
  "new_topic_name": "Short descriptive name (3-5 words)",
  "keywords": ["keyword1", "keyword2", "keyword3", "keyword4", "keyword5"],
  "confidence": 0.0-1.0,
  "reason": "Brief explanation"
}

If topic_shifted is false, leave new_topic_name empty and keywords as empty array.
Confidence should reflect how certain you are about the shift (or lack thereof).`, currentTopicContext, messagesText)
}

// buildTopicIdentificationPrompt constructs the prompt for initial topic identification.
func (td *TopicDetector) buildTopicIdentificationPrompt(messages []Message) string {
	// Format the messages
	var messagesText string
	for i, msg := range messages {
		messagesText += fmt.Sprintf("%d. [%s]: %s\n", i+1, msg.Role, truncateText(msg.Content, 500))
	}

	return fmt.Sprintf(`You are a topic analysis assistant. Identify the main topic of this conversation.

Messages:
%s

Task: Identify the main topic being discussed.

Respond with ONLY valid JSON in this exact format:
{
  "topic_name": "Short descriptive name (3-5 words)",
  "keywords": ["keyword1", "keyword2", "keyword3", "keyword4", "keyword5"]
}

The topic name should be concise but descriptive.
Keywords should be 3-5 relevant terms that capture the essence of the discussion.`, messagesText)
}

// parseTopicShiftResponse parses Claude's response for topic shift detection.
func (td *TopicDetector) parseTopicShiftResponse(text string) (*TopicShift, error) {
	// Try to extract JSON from the response
	jsonStr := extractJSON(text)
	if jsonStr == "" {
		return nil, fmt.Errorf("no valid JSON found in response: %s", truncateText(text, 200))
	}

	var resp topicDetectionResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		return nil, fmt.Errorf("parse topic detection response: %w (json: %s)", err, jsonStr)
	}

	return &TopicShift{
		Detected:     resp.TopicShifted,
		NewTopicName: resp.NewTopicName,
		NewKeywords:  resp.Keywords,
		Confidence:   resp.Confidence,
		Reason:       resp.Reason,
	}, nil
}

// parseTopicIdentificationResponse parses Claude's response for topic identification.
func (td *TopicDetector) parseTopicIdentificationResponse(text string, messages []Message) (*Topic, error) {
	// Try to extract JSON from the response
	jsonStr := extractJSON(text)
	if jsonStr == "" {
		return nil, fmt.Errorf("no valid JSON found in response: %s", truncateText(text, 200))
	}

	var resp topicIdentificationResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		return nil, fmt.Errorf("parse topic identification response: %w (json: %s)", err, jsonStr)
	}

	// Get the first message ID as the start
	var startMsgID string
	if len(messages) > 0 {
		startMsgID = messages[0].ID
	}

	return &Topic{
		Name:       resp.TopicName,
		Keywords:   resp.Keywords,
		StartMsgID: startMsgID,
		Relevance:  1.0, // New topics start with full relevance
		CreatedAt:  time.Now(),
	}, nil
}

// extractJSON attempts to extract a JSON object from text.
// It handles cases where the model may include extra text before/after the JSON.
func extractJSON(text string) string {
	// Find the first { and last }
	start := -1
	end := -1
	depth := 0

	for i, ch := range text {
		if ch == '{' {
			if start == -1 {
				start = i
			}
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 && start != -1 {
				end = i + 1
				break
			}
		}
	}

	if start == -1 || end == -1 || end <= start {
		return ""
	}

	return text[start:end]
}

// truncateText truncates text to a maximum length, adding ellipsis if needed.
func truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	if maxLen <= 3 {
		return text[:maxLen]
	}
	return text[:maxLen-3] + "..."
}

// Model returns the model being used for topic detection.
func (td *TopicDetector) Model() string {
	return td.model
}

// SetModel updates the model used for topic detection.
func (td *TopicDetector) SetModel(model string) {
	td.model = model
}
