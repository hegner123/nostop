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
    Keywords       []string   `json:"keywords"` // Stored as JSON in DB
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
    RoleSystem    Role = "system" // Used for summary messages
)

// SummarySource indicates where a summary message originated.
type SummarySource string

const (
    SummarySourceNone     SummarySource = ""          // Not a summary message
    SummarySourceTopic    SummarySource = "topic"     // Summarizes an archived topic
    SummarySourceWorkUnit SummarySource = "work_unit" // Summarizes a completed work unit
)

// Message represents a message in the database.
type Message struct {
    ID             string    `json:"id"`
    ConversationID string    `json:"conversation_id"`
    TopicID        *string   `json:"topic_id,omitempty"` // NULL if not assigned to a topic
    Role           Role      `json:"role"`
    Content        string    `json:"content"` // JSON array of content blocks
    TokenCount     int       `json:"token_count"`
    IsArchived     bool      `json:"is_archived"`
    CreatedAt      time.Time `json:"created_at"`

    // Summary message fields (Phase A: Summary-on-Archive)
    IsSummary       bool          `json:"is_summary"`                  // TRUE for summary messages
    SummarySource   SummarySource `json:"summary_source,omitempty"`    // 'topic' or 'work_unit'
    SummarySourceID *string       `json:"summary_source_id,omitempty"` // topic_id or work_unit_id being summarized

    // Work unit tracking (Phase B: Plan-Driven Execution)
    WorkUnitID *string `json:"work_unit_id,omitempty"` // NULL if not part of a plan-driven session
}

// IsSummaryMessage returns true if this is a summary message that stays in active context.
func (m *Message) IsSummaryMessage() bool {
    return m.IsSummary && m.SummarySource != SummarySourceNone
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

// TopicWithCounts represents a topic with message count for history display.
type TopicWithCounts struct {
    Topic
    MessageCount int `json:"message_count"`
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

// ----------------------------------------------------------------------------
// Work Unit Types (Phase B: Plan-Driven Execution)
// ----------------------------------------------------------------------------

// WorkUnitStatus represents the state of a work unit.
type WorkUnitStatus int

const (
    WorkUnitPending  WorkUnitStatus = iota // Not started
    WorkUnitActive                         // Currently being worked on
    WorkUnitComplete                       // Finished but not archived
    WorkUnitArchived                       // Finished and archived
)

// String returns the string representation of the status.
func (s WorkUnitStatus) String() string {
    switch s {
    case WorkUnitPending:
        return "pending"
    case WorkUnitActive:
        return "active"
    case WorkUnitComplete:
        return "complete"
    case WorkUnitArchived:
        return "archived"
    default:
        return "unknown"
    }
}

// WorkUnit represents a work unit from a plan in the database.
type WorkUnit struct {
    ID             string          `json:"id"`              // Hierarchical ID from parser: "phase-1/step-1.1"
    ConversationID string          `json:"conversation_id"` // Links to conversation
    PlanFile       string          `json:"plan_file"`       // Path to the plan markdown file
    Name           string          `json:"name"`            // Human-readable name
    Level          string          `json:"level"`           // Level name: "phase", "step", "item"
    Status         WorkUnitStatus  `json:"status"`          // Current status
    ParentID       *string         `json:"parent_id,omitempty"`
    LineNumber     int             `json:"line_number"`     // Source line in plan file (1-based)
    CreatedAt      time.Time       `json:"created_at"`
    CompletedAt    *time.Time      `json:"completed_at,omitempty"`
}

// IsLeaf returns true if this work unit has no children (based on ID structure).
func (w *WorkUnit) IsLeaf() bool {
    // Leaf status is determined by children count, not ID structure
    // This is a placeholder - actual logic requires querying children
    return true
}

// WorkUnitWithChildren represents a work unit with its child unit IDs.
type WorkUnitWithChildren struct {
    WorkUnit
    Children []string `json:"children"` // Child work unit IDs in order
}
