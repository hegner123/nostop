package rlm

import (
	"testing"

	"github.com/user/rlm/internal/api"
)

func TestContextZoneString(t *testing.T) {
	tests := []struct {
		zone     ContextZone
		expected string
	}{
		{ZoneNormal, "normal"},
		{ZoneMonitor, "monitor"},
		{ZoneWarning, "warning"},
		{ZoneArchive, "archive"},
		{ContextZone(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.zone.String()
		if got != tt.expected {
			t.Errorf("ContextZone(%d).String() = %q, want %q", tt.zone, got, tt.expected)
		}
	}
}

func TestDetermineZone(t *testing.T) {
	cm := NewContextManager(100000, nil, nil, "claude-haiku-4-5")

	// Zones based on plan thresholds (lower bounds):
	// 0% to <50%   -> Normal
	// 50% to <80%  -> Monitor
	// 80% to <95%  -> Warning
	// 95%+         -> Archive

	tests := []struct {
		usagePercent float64
		expected     ContextZone
	}{
		{0.0, ZoneNormal},
		{0.25, ZoneNormal},
		{0.49, ZoneNormal},
		{0.50, ZoneMonitor}, // >= 0.50 and < 0.80
		{0.65, ZoneMonitor},
		{0.79, ZoneMonitor},
		{0.80, ZoneWarning}, // >= 0.80 and < 0.95
		{0.90, ZoneWarning},
		{0.94, ZoneWarning},
		{0.95, ZoneArchive}, // >= 0.95
		{0.99, ZoneArchive},
		{1.0, ZoneArchive},
	}

	for _, tt := range tests {
		got := cm.determineZone(tt.usagePercent)
		if got != tt.expected {
			t.Errorf("determineZone(%.2f) = %v, want %v", tt.usagePercent, got, tt.expected)
		}
	}
}

func TestShouldArchive(t *testing.T) {
	cm := NewContextManager(100000, nil, nil, "claude-haiku-4-5")

	tests := []struct {
		usagePercent float64
		expected     bool
	}{
		{0.0, false},
		{0.50, false},
		{0.80, false},
		{0.94, false},
		{0.95, true},
		{0.99, true},
		{1.0, true},
	}

	for _, tt := range tests {
		usage := &ContextUsage{UsagePercent: tt.usagePercent}
		got := cm.ShouldArchive(usage)
		if got != tt.expected {
			t.Errorf("ShouldArchive(%.2f) = %v, want %v", tt.usagePercent, got, tt.expected)
		}
	}
}

func TestGetArchiveTarget(t *testing.T) {
	cm := NewContextManager(100000, nil, nil, "claude-haiku-4-5")

	target := cm.GetArchiveTarget()
	if target != 0.50 {
		t.Errorf("GetArchiveTarget() = %v, want 0.50", target)
	}
}

func TestTokensToFree(t *testing.T) {
	cm := NewContextManager(100000, nil, nil, "claude-haiku-4-5")

	tests := []struct {
		name         string
		totalTokens  int
		usagePercent float64
		expected     int
	}{
		{
			name:         "below threshold - no tokens to free",
			totalTokens:  50000,
			usagePercent: 0.50,
			expected:     0,
		},
		{
			name:         "at threshold - need to free tokens",
			totalTokens:  95000,
			usagePercent: 0.95,
			expected:     45000, // 95000 - 50000 (target)
		},
		{
			name:         "above threshold - need to free tokens",
			totalTokens:  100000,
			usagePercent: 1.0,
			expected:     50000, // 100000 - 50000 (target)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := &ContextUsage{
				TotalTokens:  tt.totalTokens,
				UsagePercent: tt.usagePercent,
			}
			got := cm.TokensToFree(usage)
			if got != tt.expected {
				t.Errorf("TokensToFree() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestTokenCache(t *testing.T) {
	cm := NewContextManager(100000, nil, nil, "claude-haiku-4-5")

	// Test cache miss
	_, ok := cm.GetCachedTokenCount("hash1")
	if ok {
		t.Error("Expected cache miss for non-existent key")
	}

	// Test cache set and hit
	cm.SetCachedTokenCount("hash1", 100)
	count, ok := cm.GetCachedTokenCount("hash1")
	if !ok {
		t.Error("Expected cache hit for existing key")
	}
	if count != 100 {
		t.Errorf("GetCachedTokenCount() = %d, want 100", count)
	}

	// Test cache clear
	cm.ClearCache()
	_, ok = cm.GetCachedTokenCount("hash1")
	if ok {
		t.Error("Expected cache miss after clear")
	}
}

func TestRecalculationInterval(t *testing.T) {
	cm := NewContextManager(100000, nil, nil, "claude-haiku-4-5")

	tests := []struct {
		zone     ContextZone
		expected string
	}{
		{ZoneNormal, "every 10 messages"},
		{ZoneMonitor, "every 5 messages"},
		{ZoneWarning, "every message"},
		{ZoneArchive, "immediate"},
	}

	for _, tt := range tests {
		got := cm.RecalculationInterval(tt.zone)
		if got != tt.expected {
			t.Errorf("RecalculationInterval(%v) = %q, want %q", tt.zone, got, tt.expected)
		}
	}
}

func TestContextUsageSummary(t *testing.T) {
	// 75% usage is in the Monitor zone (50-80%)
	usage := &ContextUsage{
		TotalTokens:  75000,
		MaxTokens:    100000,
		UsagePercent: 0.75,
		Zone:         ZoneMonitor,
		TopicBreakdown: map[string]TopicUsage{
			"topic1": {TopicID: "topic1", IsArchived: false},
			"topic2": {TopicID: "topic2", IsArchived: false},
			"topic3": {TopicID: "topic3", IsArchived: true},
		},
	}

	summary := usage.Summary()

	// Check that summary contains expected information
	if summary == "" {
		t.Error("Summary should not be empty")
	}

	// Summary should mention the zone
	expectedContains := "monitor"
	if !containsString(summary, expectedContains) {
		t.Errorf("Summary %q should contain %q", summary, expectedContains)
	}
}

func TestMaxTokensGetSet(t *testing.T) {
	cm := NewContextManager(100000, nil, nil, "claude-haiku-4-5")

	if cm.MaxTokens() != 100000 {
		t.Errorf("MaxTokens() = %d, want 100000", cm.MaxTokens())
	}

	cm.SetMaxTokens(200000)
	if cm.MaxTokens() != 200000 {
		t.Errorf("After SetMaxTokens(200000), MaxTokens() = %d, want 200000", cm.MaxTokens())
	}
}

func TestRoughEstimate(t *testing.T) {
	cm := NewContextManager(100000, nil, nil, "claude-haiku-4-5")

	// Test with string content (48 characters)
	messages := []api.MessageParam{
		{Role: api.RoleUser, Content: "Hello, this is a test message with some content."},
	}

	estimate := cm.roughEstimate(messages)

	// 48 characters / 4 = 12 tokens approximately
	if estimate < 10 || estimate > 15 {
		t.Errorf("roughEstimate() = %d, expected between 10 and 15", estimate)
	}
}

func TestRoughEstimateWithBlocks(t *testing.T) {
	cm := NewContextManager(100000, nil, nil, "claude-haiku-4-5")

	// Test with content blocks
	messages := []api.MessageParam{
		{
			Role: api.RoleUser,
			Content: []any{
				api.TextBlockParam{
					Type: api.ContentBlockTypeText,
					Text: "This is a test message.", // 23 characters
				},
			},
		},
	}

	estimate := cm.roughEstimate(messages)

	// 23 characters / 4 = ~5-6 tokens
	if estimate < 4 || estimate > 8 {
		t.Errorf("roughEstimate() = %d, expected between 4 and 8", estimate)
	}
}

func TestThresholdConstants(t *testing.T) {
	// Verify threshold constants are correctly defined (lower bounds)
	// Normal zone is implicit (0 to ThresholdMonitor)
	if ThresholdMonitor != 0.50 {
		t.Errorf("ThresholdMonitor = %v, want 0.50", ThresholdMonitor)
	}
	if ThresholdWarning != 0.80 {
		t.Errorf("ThresholdWarning = %v, want 0.80", ThresholdWarning)
	}
	if ThresholdArchive != 0.95 {
		t.Errorf("ThresholdArchive = %v, want 0.95", ThresholdArchive)
	}
	if ArchiveTarget != 0.50 {
		t.Errorf("ArchiveTarget = %v, want 0.50", ArchiveTarget)
	}
}

func TestNewContextManager(t *testing.T) {
	cm := NewContextManager(200000, nil, nil, "claude-sonnet-4-5")

	if cm.maxTokens != 200000 {
		t.Errorf("maxTokens = %d, want 200000", cm.maxTokens)
	}
	if cm.model != "claude-sonnet-4-5" {
		t.Errorf("model = %s, want claude-sonnet-4-5", cm.model)
	}
	if cm.tokenCache == nil {
		t.Error("tokenCache should be initialized")
	}
}

// containsString checks if a string contains a substring.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStringHelper(s, substr))
}

func containsStringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
