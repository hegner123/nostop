// Package nostop provides the main Nostop engine for intelligent topic-based context archival.
package nostop

import (
	"sync"
	"time"

	"github.com/hegner123/nostop/internal/api"
)

// CacheStrategy defines the caching approach for different content types.
// The strategy specifies TTLs for content that rarely changes vs content
// that changes more frequently.
type CacheStrategy struct {
	// SystemPromptTTL is the cache duration for system prompts.
	// System prompts rarely change, so 1 hour is recommended.
	// Default: 1 hour
	SystemPromptTTL time.Duration

	// TopicContextTTL is the cache duration for topic context summaries.
	// Topic context changes moderately, so 5 minutes is recommended.
	// Default: 5 minutes
	TopicContextTTL time.Duration

	// EnableCaching controls whether caching is enabled at all.
	// When false, no cache_control fields are added to requests.
	EnableCaching bool
}

// DefaultCacheStrategy returns a CacheStrategy with recommended defaults:
//   - System prompts: 1 hour TTL (rarely changes)
//   - Topic context: 5 minutes TTL (moderately changing)
//   - Caching enabled
func DefaultCacheStrategy() CacheStrategy {
	return CacheStrategy{
		SystemPromptTTL: time.Hour,
		TopicContextTTL: 5 * time.Minute,
		EnableCaching:   true,
	}
}

// NoCacheStrategy returns a CacheStrategy with caching disabled.
func NoCacheStrategy() CacheStrategy {
	return CacheStrategy{
		EnableCaching: false,
	}
}

// GetSystemPromptCacheControl returns the appropriate CacheControl for system prompts.
// Returns nil if caching is disabled.
func (cs *CacheStrategy) GetSystemPromptCacheControl() *api.CacheControl {
	if !cs.EnableCaching {
		return nil
	}
	return cs.ttlToCacheControl(cs.SystemPromptTTL)
}

// GetTopicContextCacheControl returns the appropriate CacheControl for topic context.
// Returns nil if caching is disabled.
func (cs *CacheStrategy) GetTopicContextCacheControl() *api.CacheControl {
	if !cs.EnableCaching {
		return nil
	}
	return cs.ttlToCacheControl(cs.TopicContextTTL)
}

// ttlToCacheControl converts a time.Duration to the appropriate CacheControl.
// Returns 1h TTL for durations >= 30 minutes, otherwise 5m TTL.
func (cs *CacheStrategy) ttlToCacheControl(ttl time.Duration) *api.CacheControl {
	if ttl >= 30*time.Minute {
		return api.WithLongCache()
	}
	return api.WithEphemeralCache()
}

// ApplyToSystemPrompt applies cache control to a system prompt.
// Returns the system prompt formatted as TextBlockParams with cache control.
// If the system prompt is empty or caching is disabled, returns nil.
func (cs *CacheStrategy) ApplyToSystemPrompt(systemPrompt string) *api.SystemParam {
	if systemPrompt == "" {
		return nil
	}

	if !cs.EnableCaching {
		param := api.NewSystemString(systemPrompt)
		return &param
	}

	// Use text blocks to enable cache control on system prompt
	block := api.NewTextBlockWithCache(systemPrompt, cs.GetSystemPromptCacheControl())
	param := api.NewSystemBlocks(block)
	return &param
}

// ApplyToTopicContext applies cache control to topic context content.
// Returns a TextBlockParam with appropriate cache control.
func (cs *CacheStrategy) ApplyToTopicContext(content string) api.TextBlockParam {
	if !cs.EnableCaching {
		return api.NewTextBlock(content)
	}
	return api.NewTextBlockWithCache(content, cs.GetTopicContextCacheControl())
}

// CacheStats tracks cache performance metrics from API responses.
// All methods are safe for concurrent use.
type CacheStats struct {
	mu sync.RWMutex

	// CacheCreationTokens is the total tokens used to create cache entries.
	CacheCreationTokens int `json:"cache_creation_tokens"`

	// CacheReadTokens is the total tokens read from cache entries.
	CacheReadTokens int `json:"cache_read_tokens"`

	// CacheCreation5m is the tokens used for 5-minute cache entries.
	CacheCreation5m int `json:"cache_creation_5m"`

	// CacheCreation1h is the tokens used for 1-hour cache entries.
	CacheCreation1h int `json:"cache_creation_1h"`

	// TotalRequests tracks the number of requests for averaging.
	TotalRequests int `json:"total_requests"`

	// CacheHits counts requests that had any cache reads.
	CacheHits int `json:"cache_hits"`
}

// NewCacheStats creates a new empty CacheStats.
func NewCacheStats() *CacheStats {
	return &CacheStats{}
}

// Update adds usage information from an API response to the stats.
func (cs *CacheStats) Update(usage api.Usage) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.TotalRequests++

	if usage.CacheCreationInputTokens != nil {
		cs.CacheCreationTokens += *usage.CacheCreationInputTokens
	}

	if usage.CacheReadInputTokens != nil {
		cs.CacheReadTokens += *usage.CacheReadInputTokens
		if *usage.CacheReadInputTokens > 0 {
			cs.CacheHits++
		}
	}

	if usage.CacheCreation != nil {
		cs.CacheCreation5m += usage.CacheCreation.Ephemeral5mInputTokens
		cs.CacheCreation1h += usage.CacheCreation.Ephemeral1hInputTokens
	}
}

// EstimatedSavings estimates the tokens saved through caching.
func (cs *CacheStats) EstimatedSavings() int {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.CacheReadTokens
}

// HitRate returns the cache hit rate as a percentage (0.0-1.0).
func (cs *CacheStats) HitRate() float64 {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	if cs.TotalRequests == 0 {
		return 0
	}
	return float64(cs.CacheHits) / float64(cs.TotalRequests)
}

// Reset clears all accumulated stats.
func (cs *CacheStats) Reset() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.CacheCreationTokens = 0
	cs.CacheReadTokens = 0
	cs.CacheCreation5m = 0
	cs.CacheCreation1h = 0
	cs.TotalRequests = 0
	cs.CacheHits = 0
}

// Clone returns a copy of the CacheStats.
func (cs *CacheStats) Clone() *CacheStats {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return &CacheStats{
		CacheCreationTokens: cs.CacheCreationTokens,
		CacheReadTokens:     cs.CacheReadTokens,
		CacheCreation5m:     cs.CacheCreation5m,
		CacheCreation1h:     cs.CacheCreation1h,
		TotalRequests:       cs.TotalRequests,
		CacheHits:           cs.CacheHits,
	}
}

// CacheDebugInfo contains cache information for debug view display.
type CacheDebugInfo struct {
	// Enabled indicates if caching is active.
	Enabled bool `json:"enabled"`

	// Strategy contains the active cache strategy settings.
	Strategy CacheStrategy `json:"strategy"`

	// Stats contains cumulative cache performance stats.
	Stats *CacheStats `json:"stats"`

	// LastRequestStats contains stats from the most recent request only.
	LastRequestStats *CacheStats `json:"last_request_stats,omitempty"`
}

// NewCacheDebugInfo creates debug info from a strategy and stats.
func NewCacheDebugInfo(strategy CacheStrategy, stats *CacheStats) *CacheDebugInfo {
	return &CacheDebugInfo{
		Enabled:  strategy.EnableCaching,
		Strategy: strategy,
		Stats:    stats,
	}
}
