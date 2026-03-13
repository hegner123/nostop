// Package nostop provides the main Nostop engine for intelligent topic-based context archival.
package nostop

import (
	"context"
	"fmt"
	"sync"

	"github.com/hegner123/nostop/internal/api"
	"github.com/hegner123/nostop/internal/storage"
	"github.com/hegner123/nostop/internal/topic"
)

// Context usage thresholds as defined in the plan.
// These are lower bounds for each zone:
//   - Normal: 0% to <50%
//   - Monitor: 50% to <80%
//   - Warning: 80% to <95%
//   - Archive: 95%+
const (
	// ThresholdMonitor is the lower bound for monitoring zone (50-80%).
	// In this zone, allocations are recalculated more frequently.
	ThresholdMonitor = 0.50

	// ThresholdWarning is the lower bound for warning zone (80-95%).
	// In this zone, topics are prepared for potential archival.
	ThresholdWarning = 0.80

	// ThresholdArchive is the trigger point for archival (95%+).
	// At or above this threshold, archival is triggered.
	ThresholdArchive = 0.95

	// ArchiveTarget is the target usage after archival (50%).
	ArchiveTarget = 0.50
)

// ContextZone represents the current context usage zone.
type ContextZone int

const (
	// ZoneNormal indicates normal operation (0-50% usage).
	ZoneNormal ContextZone = iota
	// ZoneMonitor indicates monitoring zone (50-80% usage).
	ZoneMonitor
	// ZoneWarning indicates warning zone (80-95% usage).
	ZoneWarning
	// ZoneArchive indicates archival trigger zone (95%+ usage).
	ZoneArchive
)

// String returns a human-readable description of the zone.
func (z ContextZone) String() string {
	switch z {
	case ZoneNormal:
		return "normal"
	case ZoneMonitor:
		return "monitor"
	case ZoneWarning:
		return "warning"
	case ZoneArchive:
		return "archive"
	default:
		return "unknown"
	}
}

// TopicUsage contains usage information for a single topic.
type TopicUsage struct {
	TopicID    string  // Topic identifier
	TopicName  string  // Human-readable topic name
	TokenCount int     // Number of tokens in this topic
	Allocation float64 // Current % allocation (0.0-1.0)
	Relevance  float64 // Current relevance score (0.0-1.0)
	IsArchived bool    // Whether the topic is archived
	IsCurrent  bool    // Whether this is the current topic
}

// ContextUsage contains overall context usage information.
type ContextUsage struct {
	TotalTokens    int                   // Total tokens across all active topics
	MaxTokens      int                   // Maximum allowed tokens
	UsagePercent   float64               // Usage as percentage (0.0-1.0)
	Zone           ContextZone           // Current usage zone
	TopicBreakdown map[string]TopicUsage // Usage by topic ID
}

// ContextManager manages context budget and usage tracking.
type ContextManager struct {
	maxTokens int
	tracker   *topic.TopicTracker
	client    *api.Client
	model     string // Model to use for token counting

	// Token count cache
	cacheMu    sync.RWMutex
	tokenCache map[string]int // message content hash -> token count
}

// NewContextManager creates a new ContextManager instance.
func NewContextManager(maxTokens int, tracker *topic.TopicTracker, client *api.Client, model string) *ContextManager {
	return &ContextManager{
		maxTokens:  maxTokens,
		tracker:    tracker,
		client:     client,
		model:      model,
		tokenCache: make(map[string]int),
	}
}

// GetUsage calculates and returns the current context usage.
func (cm *ContextManager) GetUsage(ctx context.Context, conversation *storage.Conversation) (*ContextUsage, error) {
	topics := cm.tracker.GetTopics()
	allocations := cm.tracker.GetAllocations()

	usage := &ContextUsage{
		MaxTokens:      cm.maxTokens,
		TopicBreakdown: make(map[string]TopicUsage),
	}

	var totalTokens int

	for _, t := range topics {
		if t.IsArchived() {
			// Include archived topics in breakdown but don't count their tokens
			usage.TopicBreakdown[t.ID] = TopicUsage{
				TopicID:    t.ID,
				TopicName:  t.Name,
				TokenCount: t.TokenCount,
				Allocation: 0,
				Relevance:  t.RelevanceScore,
				IsArchived: true,
				IsCurrent:  t.IsCurrent,
			}
			continue
		}

		totalTokens += t.TokenCount

		usage.TopicBreakdown[t.ID] = TopicUsage{
			TopicID:    t.ID,
			TopicName:  t.Name,
			TokenCount: t.TokenCount,
			Allocation: allocations[t.ID],
			Relevance:  t.RelevanceScore,
			IsArchived: false,
			IsCurrent:  t.IsCurrent,
		}
	}

	usage.TotalTokens = totalTokens

	if cm.maxTokens > 0 {
		usage.UsagePercent = float64(totalTokens) / float64(cm.maxTokens)
	}

	usage.Zone = cm.determineZone(usage.UsagePercent)

	return usage, nil
}

// determineZone returns the context zone for a given usage percentage.
func (cm *ContextManager) determineZone(usagePercent float64) ContextZone {
	switch {
	case usagePercent >= ThresholdArchive:
		return ZoneArchive
	case usagePercent >= ThresholdWarning:
		return ZoneWarning
	case usagePercent >= ThresholdMonitor:
		return ZoneMonitor
	default:
		return ZoneNormal
	}
}

// ShouldArchive returns true if context usage is at or above the archive threshold (95%).
func (cm *ContextManager) ShouldArchive(usage *ContextUsage) bool {
	return usage.UsagePercent >= ThresholdArchive
}

// GetArchiveTarget returns the target usage percentage after archival (0.50 = 50%).
func (cm *ContextManager) GetArchiveTarget() float64 {
	return ArchiveTarget
}

// TokensToFree calculates how many tokens need to be freed to reach the archive target.
func (cm *ContextManager) TokensToFree(usage *ContextUsage) int {
	if !cm.ShouldArchive(usage) {
		return 0
	}

	targetTokens := int(float64(cm.maxTokens) * ArchiveTarget)
	tokensToFree := usage.TotalTokens - targetTokens

	if tokensToFree < 0 {
		return 0
	}

	return tokensToFree
}

// EstimateTokens uses the Claude API to count tokens for a set of messages.
// Results are cached when possible.
func (cm *ContextManager) EstimateTokens(ctx context.Context, messages []api.MessageParam) (int, error) {
	if cm.client == nil {
		// Fallback: rough estimate of 4 characters per token
		return cm.roughEstimate(messages), nil
	}

	req := &api.TokenCountRequest{
		Model:    cm.model,
		Messages: messages,
	}

	resp, err := cm.client.CountTokens(ctx, req)
	if err != nil {
		// On error, fall back to rough estimate
		return cm.roughEstimate(messages), nil
	}

	return resp.InputTokens, nil
}

// roughEstimate provides a fallback token estimate when the API is unavailable.
// Uses approximately 4 characters per token as a rough heuristic.
func (cm *ContextManager) roughEstimate(messages []api.MessageParam) int {
	var totalChars int

	for _, msg := range messages {
		switch content := msg.Content.(type) {
		case string:
			totalChars += len(content)
		case []any:
			for _, block := range content {
				if textBlock, ok := block.(api.TextBlockParam); ok {
					totalChars += len(textBlock.Text)
				}
			}
		}
	}

	// Rough estimate: ~4 characters per token
	return totalChars / 4
}

// GetCachedTokenCount returns a cached token count for a content hash, if available.
func (cm *ContextManager) GetCachedTokenCount(contentHash string) (int, bool) {
	cm.cacheMu.RLock()
	defer cm.cacheMu.RUnlock()

	count, ok := cm.tokenCache[contentHash]
	return count, ok
}

// SetCachedTokenCount stores a token count in the cache.
func (cm *ContextManager) SetCachedTokenCount(contentHash string, count int) {
	cm.cacheMu.Lock()
	defer cm.cacheMu.Unlock()

	cm.tokenCache[contentHash] = count
}

// ClearCache clears the token count cache.
func (cm *ContextManager) ClearCache() {
	cm.cacheMu.Lock()
	defer cm.cacheMu.Unlock()

	cm.tokenCache = make(map[string]int)
}

// MaxTokens returns the maximum token budget.
func (cm *ContextManager) MaxTokens() int {
	return cm.maxTokens
}

// SetMaxTokens updates the maximum token budget.
func (cm *ContextManager) SetMaxTokens(maxTokens int) {
	cm.maxTokens = maxTokens
}

// RecalculationInterval returns the recommended interval for recalculating allocations
// based on the current usage zone.
func (cm *ContextManager) RecalculationInterval(zone ContextZone) string {
	switch zone {
	case ZoneNormal:
		return "every 10 messages"
	case ZoneMonitor:
		return "every 5 messages"
	case ZoneWarning:
		return "every message"
	case ZoneArchive:
		return "immediate"
	default:
		return "every 10 messages"
	}
}

// Summary returns a human-readable summary of the context usage.
func (u *ContextUsage) Summary() string {
	activeCount := 0
	archivedCount := 0

	for _, t := range u.TopicBreakdown {
		if t.IsArchived {
			archivedCount++
		} else {
			activeCount++
		}
	}

	return formatSummary(u.TotalTokens, u.MaxTokens, u.UsagePercent, u.Zone, activeCount, archivedCount)
}

// formatSummary formats the usage summary string.
func formatSummary(totalTokens, maxTokens int, usagePercent float64, zone ContextZone, activeTopics, archivedTopics int) string {
	return fmt.Sprintf(
		"Context: %d/%d tokens (%.1f%%) [%s] - %d active topics, %d archived",
		totalTokens,
		maxTokens,
		usagePercent*100,
		zone.String(),
		activeTopics,
		archivedTopics,
	)
}
