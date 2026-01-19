package storage

import (
	"encoding/json"
	"time"
)

// Conversation represents a conversation in the database.
type Conversation struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Model           string    `json:"model"`
	SystemPrompt    string    `json:"system_prompt"`
	TotalTokenCount int       `json:"total_token_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Topic represents a topic within a conversation.
type Topic struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversation_id"`
	Name           string     `json:"name"`
	Keywords       []string   `json:"keywords"`        // Stored as JSON in DB
	TokenCount     int        `json:"token_count"`
	RelevanceScore float64    `json:"relevance_score"` // 0.0-1.0
	IsCurrent      bool       `json:"is_current"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// IsArchived returns true if the topic has been archived.
func (t *Topic) IsArchived() bool {
	return t.ArchivedAt != nil
}

// KeywordsJSON returns the keywords as a JSON string for database storage.
func (t *Topic) KeywordsJSON() string {
	if len(t.Keywords) == 0 {
		return "[]"
	}
	data, err := json.Marshal(t.Keywords)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// SetKeywordsFromJSON parses keywords from a JSON string.
func (t *Topic) SetKeywordsFromJSON(jsonStr string) error {
	if jsonStr == "" || jsonStr == "null" {
		t.Keywords = nil
		return nil
	}
	return json.Unmarshal([]byte(jsonStr), &t.Keywords)
}

// Role represents the role of a message sender.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message represents a message in the database.
type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	TopicID        *string   `json:"topic_id,omitempty"` // NULL if not assigned to a topic
	Role           Role      `json:"role"`
	Content        string    `json:"content"`     // JSON array of content blocks
	TokenCount     int       `json:"token_count"`
	IsArchived     bool      `json:"is_archived"`
	CreatedAt      time.Time `json:"created_at"`
}

// MessageArchive represents an archived message in cold storage.
type MessageArchive struct {
	ID          string    `json:"id"`
	MessageID   string    `json:"message_id"`
	TopicID     string    `json:"topic_id"`
	FullContent string    `json:"full_content"` // Complete message content preserved
	ArchivedAt  time.Time `json:"archived_at"`
}

// ArchiveAction represents the type of archive operation.
type ArchiveAction string

const (
	ArchiveActionArchive ArchiveAction = "archive"
	ArchiveActionRestore ArchiveAction = "restore"
)

// ArchiveEvent represents an archive/restore event for analytics.
type ArchiveEvent struct {
	ID                 int64         `json:"id"`
	ConversationID     string        `json:"conversation_id"`
	TopicID            string        `json:"topic_id"`
	Action             ArchiveAction `json:"action"`
	TokensAffected     int           `json:"tokens_affected"`
	ContextUsageBefore float64       `json:"context_usage_before"` // 0.0-1.0
	ContextUsageAfter  float64       `json:"context_usage_after"`  // 0.0-1.0
	CreatedAt          time.Time     `json:"created_at"`
}

// ConversationWithTopics represents a conversation with its topics.
type ConversationWithTopics struct {
	Conversation
	Topics []Topic `json:"topics"`
}

// TopicWithMessages represents a topic with its messages.
type TopicWithMessages struct {
	Topic
	Messages []Message `json:"messages"`
}

// ConversationStats contains statistics about a conversation.
type ConversationStats struct {
	MessageCount        int     `json:"message_count"`
	TopicCount          int     `json:"topic_count"`
	ArchivedTopicCount  int     `json:"archived_topic_count"`
	TotalTokens         int     `json:"total_tokens"`
	ArchivedTokens      int     `json:"archived_tokens"`
	ActiveTokens        int     `json:"active_tokens"`
	ContextUsagePercent float64 `json:"context_usage_percent"`
}

// TopicScore represents a topic with its relevance score for archival decisions.
type TopicScore struct {
	TopicID        string  `json:"topic_id"`
	Name           string  `json:"name"`
	TokenCount     int     `json:"token_count"`
	RelevanceScore float64 `json:"relevance_score"`
	RecencyScore   float64 `json:"recency_score"` // Based on last message time
	CombinedScore  float64 `json:"combined_score"`
}

// ArchiveCandidate represents a topic that may be archived.
type ArchiveCandidate struct {
	Topic         Topic   `json:"topic"`
	TokensToFree  int     `json:"tokens_to_free"`
	PriorityScore float64 `json:"priority_score"` // Lower = archive first
}
