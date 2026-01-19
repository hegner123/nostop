// Package rlm provides the main RLM engine for intelligent topic-based context archival.
package rlm

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/user/rlm/internal/storage"
	"github.com/user/rlm/internal/topic"
)

// Archiver errors.
var (
	ErrTopicNotFound    = errors.New("topic not found")
	ErrTopicNotArchived = errors.New("topic is not archived")
	ErrNoTopicsToArchive = errors.New("no topics available to archive")
)

// Archiver handles moving topics to and from SQLite cold storage.
// Key principle: Archive, don't compact. Full messages are preserved verbatim.
type Archiver struct {
	storage *storage.SQLite
	tracker *topic.TopicTracker
}

// NewArchiver creates a new Archiver instance.
func NewArchiver(s *storage.SQLite, tracker *topic.TopicTracker) *Archiver {
	return &Archiver{
		storage: s,
		tracker: tracker,
	}
}

// ArchiveTopic moves a topic's messages to cold storage (message_archive table).
// Messages are preserved in FULL, not summarized.
//
// This method:
//   - Moves topic's messages to the message_archive table
//   - Marks the topic as archived
//   - Updates the topic tracker
//   - Logs the archive event with context usage before/after
func (a *Archiver) ArchiveTopic(ctx context.Context, t *storage.Topic, usageBefore, usageAfter float64) error {
	if t == nil {
		return ErrTopicNotFound
	}

	// Cannot archive the current topic
	currentTopic := a.tracker.GetCurrentTopic()
	if currentTopic != nil && currentTopic.ID == t.ID {
		return errors.New("cannot archive the current topic")
	}

	// Perform the archive operation in storage (handles transaction)
	if err := a.storage.ArchiveTopic(ctx, t.ID, usageBefore, usageAfter); err != nil {
		return fmt.Errorf("failed to archive topic: %w", err)
	}

	// Update tracker to remove the archived topic
	a.tracker.RemoveTopic(t.ID)

	log.Printf("Archived topic %q (ID: %s) - usage: %.1f%% -> %.1f%%",
		t.Name, t.ID, usageBefore*100, usageAfter*100)

	return nil
}

// RestoreTopic brings an archived topic back into active context.
// Used when user references archived content.
//
// This method:
//   - Restores messages from the message_archive table
//   - Clears the archived_at timestamp on the topic
//   - Updates the topic tracker
//   - Logs the restore event
func (a *Archiver) RestoreTopic(ctx context.Context, topicID string, usageBefore, usageAfter float64) (*storage.Topic, error) {
	// Verify topic exists and is archived
	t, err := a.storage.GetTopic(ctx, topicID)
	if err != nil {
		return nil, fmt.Errorf("failed to get topic: %w", err)
	}
	if t == nil {
		return nil, ErrTopicNotFound
	}
	if !t.IsArchived() {
		return nil, ErrTopicNotArchived
	}

	// Perform the restore operation in storage (handles transaction)
	if err := a.storage.RestoreTopic(ctx, topicID, usageBefore, usageAfter); err != nil {
		return nil, fmt.Errorf("failed to restore topic: %w", err)
	}

	// Reload the topic to get updated state
	t, err = a.storage.GetTopic(ctx, topicID)
	if err != nil {
		return nil, fmt.Errorf("failed to reload topic: %w", err)
	}

	// Reload tracker to include the restored topic
	if err := a.tracker.LoadTopics(ctx, t.ConversationID); err != nil {
		return nil, fmt.Errorf("failed to reload tracker: %w", err)
	}

	log.Printf("Restored topic %q (ID: %s) - usage: %.1f%% -> %.1f%%",
		t.Name, t.ID, usageBefore*100, usageAfter*100)

	return t, nil
}

// ArchiveUntilTarget archives lowest-relevance topics until usage drops to targetPercent.
// This is the main archival method called when context usage reaches 95%.
//
// Parameters:
//   - ctx: Context for cancellation
//   - conversationID: The conversation to archive topics from
//   - currentUsage: Current context usage percentage (0.0-1.0)
//   - maxTokens: Maximum context tokens for the model
//   - targetPercent: Target usage percentage after archival (typically 0.50)
//
// Returns:
//   - List of archived topics
//   - Error if archival fails
//
// Key behaviors:
//   - Never archives the current topic
//   - Uses tracker.GetTopicsToArchive() to get candidates ordered by priority
//   - Stops archiving when target is reached
func (a *Archiver) ArchiveUntilTarget(ctx context.Context, conversationID string, currentUsage float64, maxTokens int, targetPercent float64) ([]storage.Topic, error) {
	if currentUsage < ThresholdArchive {
		// No archival needed
		return nil, nil
	}

	// Get topics to archive, sorted by archival priority (lowest relevance first)
	candidates := a.tracker.GetTopicsToArchive(currentUsage)
	if len(candidates) == 0 {
		return nil, ErrNoTopicsToArchive
	}

	// Calculate how many tokens we need to free
	currentTokens := int(currentUsage * float64(maxTokens))
	targetTokens := int(targetPercent * float64(maxTokens))
	tokensToFree := currentTokens - targetTokens

	if tokensToFree <= 0 {
		return nil, nil
	}

	var archived []storage.Topic
	var freedTokens int
	usageBefore := currentUsage

	for _, candidate := range candidates {
		if freedTokens >= tokensToFree {
			break
		}

		// Calculate usage after this archive
		tokensAfterArchive := currentTokens - freedTokens - candidate.TokenCount
		usageAfter := float64(tokensAfterArchive) / float64(maxTokens)

		// Archive the topic
		if err := a.ArchiveTopic(ctx, candidate, usageBefore, usageAfter); err != nil {
			// Log error but continue with other candidates
			log.Printf("Warning: failed to archive topic %q: %v", candidate.Name, err)
			continue
		}

		archived = append(archived, *candidate)
		freedTokens += candidate.TokenCount
		usageBefore = usageAfter
	}

	if len(archived) == 0 {
		return nil, ErrNoTopicsToArchive
	}

	log.Printf("Archived %d topics, freed %d tokens (%.1f%% -> %.1f%%)",
		len(archived), freedTokens, currentUsage*100, usageBefore*100)

	return archived, nil
}

// GetArchivedTopics retrieves all archived topics for a conversation.
func (a *Archiver) GetArchivedTopics(ctx context.Context, conversationID string) ([]storage.Topic, error) {
	// Get all topics for the conversation
	allTopics, err := a.storage.ListTopics(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to list topics: %w", err)
	}

	// Filter to only archived topics
	var archived []storage.Topic
	for _, t := range allTopics {
		if t.IsArchived() {
			archived = append(archived, t)
		}
	}

	return archived, nil
}

// GetArchiveHistory retrieves the archive/restore event history for a conversation.
func (a *Archiver) GetArchiveHistory(ctx context.Context, conversationID string) ([]storage.ArchiveEvent, error) {
	// Default limit of 100 events
	return a.GetArchiveHistoryWithLimit(ctx, conversationID, 100)
}

// GetArchiveHistoryWithLimit retrieves archive history with a custom limit.
func (a *Archiver) GetArchiveHistoryWithLimit(ctx context.Context, conversationID string, limit int) ([]storage.ArchiveEvent, error) {
	events, err := a.storage.ListArchiveEvents(ctx, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list archive events: %w", err)
	}
	return events, nil
}

// GetArchiveStats returns statistics about archived content for a conversation.
func (a *Archiver) GetArchiveStats(ctx context.Context, conversationID string, maxTokens int) (*ArchiveStats, error) {
	stats, err := a.storage.GetConversationStats(ctx, conversationID, maxTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation stats: %w", err)
	}

	return &ArchiveStats{
		TotalTopics:         stats.TopicCount,
		ArchivedTopics:      stats.ArchivedTopicCount,
		ActiveTopics:        stats.TopicCount - stats.ArchivedTopicCount,
		TotalTokens:         stats.TotalTokens,
		ArchivedTokens:      stats.ArchivedTokens,
		ActiveTokens:        stats.ActiveTokens,
		ContextUsagePercent: stats.ContextUsagePercent,
	}, nil
}

// ArchiveStats contains statistics about archived content.
type ArchiveStats struct {
	TotalTopics         int     `json:"total_topics"`
	ArchivedTopics      int     `json:"archived_topics"`
	ActiveTopics        int     `json:"active_topics"`
	TotalTokens         int     `json:"total_tokens"`
	ArchivedTokens      int     `json:"archived_tokens"`
	ActiveTokens        int     `json:"active_tokens"`
	ContextUsagePercent float64 `json:"context_usage_percent"`
}

// ShouldRestore checks if a topic should be restored based on a user query.
// This is a simple keyword-based check; the full implementation would use
// Claude to detect topic references.
func (a *Archiver) ShouldRestore(ctx context.Context, archivedTopic *storage.Topic, userQuery string) bool {
	if archivedTopic == nil || !archivedTopic.IsArchived() {
		return false
	}

	// Simple keyword matching - check if topic name or keywords appear in query
	// A full implementation would use Claude to detect semantic references
	query := userQuery

	// Check topic name
	if containsIgnoreCase(query, archivedTopic.Name) {
		return true
	}

	// Check keywords
	for _, keyword := range archivedTopic.Keywords {
		if containsIgnoreCase(query, keyword) {
			return true
		}
	}

	return false
}

// FindTopicsToRestore finds archived topics that may be relevant to a user query.
func (a *Archiver) FindTopicsToRestore(ctx context.Context, conversationID, userQuery string) ([]storage.Topic, error) {
	archived, err := a.GetArchivedTopics(ctx, conversationID)
	if err != nil {
		return nil, err
	}

	var toRestore []storage.Topic
	for _, t := range archived {
		if a.ShouldRestore(ctx, &t, userQuery) {
			toRestore = append(toRestore, t)
		}
	}

	return toRestore, nil
}

// containsIgnoreCase checks if s contains substr (case-insensitive).
func containsIgnoreCase(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}

	// Convert both to lowercase for comparison
	sLower := toLower(s)
	substrLower := toLower(substr)

	return contains(sLower, substrLower)
}

// toLower converts a string to lowercase (simple ASCII implementation).
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
