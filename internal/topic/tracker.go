package topic

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/hegner123/nostop/internal/storage"
)

// TopicTracker manages topic state and context allocation.
type TopicTracker struct {
	storage     *storage.SQLite
	topics      []*storage.Topic
	allocations map[string]float64 // topic ID -> % of context budget
	currentID   string             // current topic ID
	mu          sync.RWMutex
}

// NewTopicTracker creates a new TopicTracker instance.
func NewTopicTracker(s *storage.SQLite) *TopicTracker {
	return &TopicTracker{
		storage:     s,
		topics:      make([]*storage.Topic, 0),
		allocations: make(map[string]float64),
	}
}

// LoadTopics loads all active (non-archived) topics for a conversation.
// It also identifies and sets the current topic.
func (tt *TopicTracker) LoadTopics(ctx context.Context, conversationID string) error {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	topics, err := tt.storage.GetActiveTopics(ctx, conversationID)
	if err != nil {
		return err
	}

	tt.topics = make([]*storage.Topic, len(topics))
	tt.currentID = ""

	for i := range topics {
		tt.topics[i] = &topics[i]
		if topics[i].IsCurrent {
			tt.currentID = topics[i].ID
		}
	}

	// Recalculate allocations with current topic
	tt.allocations = tt.calculateAllocations(tt.currentID)

	return nil
}

// RecalculateAllocations implements the allocation formula from the plan:
//
//	topic_allocation = base_weight * relevance_score * recency_factor
//
// Where:
//   - base_weight = 1.0 for current topic, 0.5 for others
//   - relevance_score = 0.0-1.0 (from topic.RelevanceScore)
//   - recency_factor = 1.0 - (hours_since_active / 24), min 0.1
//
// Distribution targets:
//   - Current topic: ~60% base allocation
//   - Recent related topics: ~30%
//   - System prompt overhead: ~10% (reserved)
func (tt *TopicTracker) RecalculateAllocations(currentTopicID string) map[string]float64 {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	tt.currentID = currentTopicID
	tt.allocations = tt.calculateAllocations(currentTopicID)
	return tt.copyAllocations()
}

// calculateAllocations is the internal implementation (caller must hold lock).
func (tt *TopicTracker) calculateAllocations(currentTopicID string) map[string]float64 {
	allocations := make(map[string]float64)

	if len(tt.topics) == 0 {
		return allocations
	}

	// Reserve 10% for system prompt overhead
	const systemOverhead = 0.10
	availableBudget := 1.0 - systemOverhead

	now := time.Now()

	// Calculate raw scores for each topic
	type topicScore struct {
		id    string
		score float64
	}
	scores := make([]topicScore, 0, len(tt.topics))
	var totalScore float64

	for _, topic := range tt.topics {
		if topic.IsArchived() {
			continue
		}

		// Base weight: 1.0 for current, 0.5 for others
		baseWeight := 0.5
		if topic.ID == currentTopicID {
			baseWeight = 1.0
		}

		// Relevance score from topic (0.0-1.0)
		relevanceScore := topic.RelevanceScore
		if relevanceScore <= 0 {
			relevanceScore = 0.1 // minimum floor to avoid zero allocation
		}

		// Recency factor: 1.0 - (hours_since_active / 24), min 0.1
		hoursSinceActive := now.Sub(topic.UpdatedAt).Hours()
		recencyFactor := 1.0 - (hoursSinceActive / 24.0)
		if recencyFactor < 0.1 {
			recencyFactor = 0.1
		}

		score := baseWeight * relevanceScore * recencyFactor
		scores = append(scores, topicScore{id: topic.ID, score: score})
		totalScore += score
	}

	if totalScore == 0 {
		// Edge case: if no scores, distribute evenly
		if len(scores) > 0 {
			evenAlloc := availableBudget / float64(len(scores))
			for _, s := range scores {
				allocations[s.id] = evenAlloc
			}
		}
		return allocations
	}

	// Normalize scores to fit within available budget
	for _, s := range scores {
		allocations[s.id] = (s.score / totalScore) * availableBudget
	}

	return allocations
}

// GetTopicsToArchive returns topics to archive when usage is at or above threshold.
// It sorts by lowest relevance and oldest activity, returning enough topics to free ~45%.
func (tt *TopicTracker) GetTopicsToArchive(usagePercent float64) []*storage.Topic {
	tt.mu.RLock()
	defer tt.mu.RUnlock()

	// Only trigger archival at 95%+ usage
	if usagePercent < 0.95 {
		return nil
	}

	// Target: free enough to get from current usage to 50%
	// e.g., at 95%, need to free 45%
	targetFreePercent := usagePercent - 0.50

	// Calculate total tokens across all active topics
	var totalTokens int
	activeTopics := make([]*storage.Topic, 0)

	for _, topic := range tt.topics {
		if topic.IsArchived() || topic.ID == tt.currentID {
			// Never archive the current topic
			continue
		}
		totalTokens += topic.TokenCount
		activeTopics = append(activeTopics, topic)
	}

	if len(activeTopics) == 0 || totalTokens == 0 {
		return nil
	}

	// Sort by archival priority: lowest relevance first, then oldest
	sort.Slice(activeTopics, func(i, j int) bool {
		// Primary: lower relevance score archives first
		if activeTopics[i].RelevanceScore != activeTopics[j].RelevanceScore {
			return activeTopics[i].RelevanceScore < activeTopics[j].RelevanceScore
		}
		// Secondary: older topics archive first
		return activeTopics[i].UpdatedAt.Before(activeTopics[j].UpdatedAt)
	})

	// Select topics until we free enough
	var toArchive []*storage.Topic
	var freedTokens int
	tokensToFree := int(targetFreePercent * float64(totalTokens) / usagePercent)

	for _, topic := range activeTopics {
		if freedTokens >= tokensToFree {
			break
		}
		toArchive = append(toArchive, topic)
		freedTokens += topic.TokenCount
	}

	return toArchive
}

// AddTopic adds a new topic to the tracker and persists it.
func (tt *TopicTracker) AddTopic(ctx context.Context, topic *storage.Topic) error {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	if err := tt.storage.CreateTopic(ctx, topic); err != nil {
		return err
	}

	tt.topics = append(tt.topics, topic)

	// Recalculate allocations
	tt.allocations = tt.calculateAllocations(tt.currentID)

	return nil
}

// SetCurrentTopic switches the current topic.
func (tt *TopicTracker) SetCurrentTopic(ctx context.Context, topicID string) error {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	// Find the topic to get its conversation ID
	var conversationID string
	for _, topic := range tt.topics {
		if topic.ID == topicID {
			conversationID = topic.ConversationID
			break
		}
	}

	if conversationID == "" {
		// Topic not found in memory, try to get from storage
		topic, err := tt.storage.GetTopic(ctx, topicID)
		if err != nil {
			return err
		}
		if topic == nil {
			return storage.ErrTopicNotFound
		}
		conversationID = topic.ConversationID
	}

	// Update in database
	if err := tt.storage.SetCurrentTopic(ctx, conversationID, topicID); err != nil {
		return err
	}

	// Update local state
	for _, topic := range tt.topics {
		topic.IsCurrent = (topic.ID == topicID)
	}
	tt.currentID = topicID

	// Recalculate allocations with new current topic
	tt.allocations = tt.calculateAllocations(topicID)

	return nil
}

// GetCurrentTopic returns the current topic.
func (tt *TopicTracker) GetCurrentTopic() *storage.Topic {
	tt.mu.RLock()
	defer tt.mu.RUnlock()

	if tt.currentID == "" {
		return nil
	}

	for _, topic := range tt.topics {
		if topic.ID == tt.currentID {
			return topic
		}
	}
	return nil
}

// GetAllocation returns the allocation percentage for a topic.
func (tt *TopicTracker) GetAllocation(topicID string) float64 {
	tt.mu.RLock()
	defer tt.mu.RUnlock()

	return tt.allocations[topicID]
}

// GetAllocations returns a copy of all allocations.
func (tt *TopicTracker) GetAllocations() map[string]float64 {
	tt.mu.RLock()
	defer tt.mu.RUnlock()

	return tt.copyAllocations()
}

// copyAllocations returns a copy of the allocations map (caller must hold lock).
func (tt *TopicTracker) copyAllocations() map[string]float64 {
	result := make(map[string]float64, len(tt.allocations))
	for k, v := range tt.allocations {
		result[k] = v
	}
	return result
}

// UpdateRelevance updates the relevance score for a topic.
func (tt *TopicTracker) UpdateRelevance(ctx context.Context, topicID string, score float64) error {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	// Clamp score to valid range
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	// Find and update in memory
	var topic *storage.Topic
	for _, t := range tt.topics {
		if t.ID == topicID {
			topic = t
			break
		}
	}

	if topic == nil {
		// Try to get from storage
		var err error
		topic, err = tt.storage.GetTopic(ctx, topicID)
		if err != nil {
			return err
		}
		if topic == nil {
			return storage.ErrTopicNotFound
		}
	}

	// Update the score
	topic.RelevanceScore = score

	// Persist to storage
	if err := tt.storage.UpdateTopic(ctx, topic); err != nil {
		return err
	}

	// Recalculate allocations with updated scores
	tt.allocations = tt.calculateAllocations(tt.currentID)

	return nil
}

// GetTopics returns all active topics.
func (tt *TopicTracker) GetTopics() []*storage.Topic {
	tt.mu.RLock()
	defer tt.mu.RUnlock()

	result := make([]*storage.Topic, len(tt.topics))
	copy(result, tt.topics)
	return result
}

// GetTopic returns a topic by ID.
func (tt *TopicTracker) GetTopic(topicID string) *storage.Topic {
	tt.mu.RLock()
	defer tt.mu.RUnlock()

	for _, topic := range tt.topics {
		if topic.ID == topicID {
			return topic
		}
	}
	return nil
}

// TopicCount returns the number of active topics.
func (tt *TopicTracker) TopicCount() int {
	tt.mu.RLock()
	defer tt.mu.RUnlock()

	return len(tt.topics)
}

// TotalTokens returns the total token count across all active topics.
func (tt *TopicTracker) TotalTokens() int {
	tt.mu.RLock()
	defer tt.mu.RUnlock()

	var total int
	for _, topic := range tt.topics {
		total += topic.TokenCount
	}
	return total
}

// RemoveTopic removes a topic from the tracker (does not delete from storage).
func (tt *TopicTracker) RemoveTopic(topicID string) {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	for i, topic := range tt.topics {
		if topic.ID == topicID {
			tt.topics = append(tt.topics[:i], tt.topics[i+1:]...)
			break
		}
	}

	delete(tt.allocations, topicID)

	if tt.currentID == topicID {
		tt.currentID = ""
		// Set current to most recent topic if available
		if len(tt.topics) > 0 {
			var mostRecent *storage.Topic
			for _, t := range tt.topics {
				if mostRecent == nil || t.UpdatedAt.After(mostRecent.UpdatedAt) {
					mostRecent = t
				}
			}
			if mostRecent != nil {
				tt.currentID = mostRecent.ID
				mostRecent.IsCurrent = true
			}
		}
	}

	// Recalculate allocations
	tt.allocations = tt.calculateAllocations(tt.currentID)
}
