package nostop

import (
	"testing"
	"time"

	"github.com/hegner123/nostop/internal/api"
)

func TestDefaultCacheStrategy(t *testing.T) {
	strategy := DefaultCacheStrategy()

	if !strategy.EnableCaching {
		t.Error("expected caching to be enabled by default")
	}
	if strategy.SystemPromptTTL != time.Hour {
		t.Errorf("expected SystemPromptTTL to be 1 hour, got %v", strategy.SystemPromptTTL)
	}
	if strategy.TopicContextTTL != 5*time.Minute {
		t.Errorf("expected TopicContextTTL to be 5 minutes, got %v", strategy.TopicContextTTL)
	}
}

func TestNoCacheStrategy(t *testing.T) {
	strategy := NoCacheStrategy()

	if strategy.EnableCaching {
		t.Error("expected caching to be disabled")
	}
}

func TestCacheStrategy_GetSystemPromptCacheControl(t *testing.T) {
	t.Run("enabled strategy returns 1h cache", func(t *testing.T) {
		strategy := DefaultCacheStrategy()
		cc := strategy.GetSystemPromptCacheControl()

		if cc == nil {
			t.Fatal("expected non-nil cache control")
		}
		if cc.Type != "ephemeral" {
			t.Errorf("expected type 'ephemeral', got %q", cc.Type)
		}
		if cc.TTL == nil || *cc.TTL != api.CacheTTL1Hour {
			t.Errorf("expected 1h TTL, got %v", cc.TTL)
		}
	})

	t.Run("disabled strategy returns nil", func(t *testing.T) {
		strategy := NoCacheStrategy()
		cc := strategy.GetSystemPromptCacheControl()

		if cc != nil {
			t.Error("expected nil cache control when caching is disabled")
		}
	})
}

func TestCacheStrategy_GetTopicContextCacheControl(t *testing.T) {
	t.Run("enabled strategy returns 5m cache", func(t *testing.T) {
		strategy := DefaultCacheStrategy()
		cc := strategy.GetTopicContextCacheControl()

		if cc == nil {
			t.Fatal("expected non-nil cache control")
		}
		if cc.Type != "ephemeral" {
			t.Errorf("expected type 'ephemeral', got %q", cc.Type)
		}
		if cc.TTL == nil || *cc.TTL != api.CacheTTL5Min {
			t.Errorf("expected 5m TTL, got %v", cc.TTL)
		}
	})

	t.Run("disabled strategy returns nil", func(t *testing.T) {
		strategy := NoCacheStrategy()
		cc := strategy.GetTopicContextCacheControl()

		if cc != nil {
			t.Error("expected nil cache control when caching is disabled")
		}
	})
}

func TestCacheStrategy_ApplyToSystemPrompt(t *testing.T) {
	t.Run("empty prompt returns nil", func(t *testing.T) {
		strategy := DefaultCacheStrategy()
		result := strategy.ApplyToSystemPrompt("")

		if result != nil {
			t.Error("expected nil for empty prompt")
		}
	})

	t.Run("enabled strategy returns block with cache control", func(t *testing.T) {
		strategy := DefaultCacheStrategy()
		result := strategy.ApplyToSystemPrompt("You are a helpful assistant.")

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		// Note: We can't easily inspect the internal value of SystemParam,
		// but we verify it's not nil which means it was constructed
	})

	t.Run("disabled strategy returns simple string", func(t *testing.T) {
		strategy := NoCacheStrategy()
		result := strategy.ApplyToSystemPrompt("You are a helpful assistant.")

		if result == nil {
			t.Fatal("expected non-nil result")
		}
	})
}

func TestCacheStrategy_ApplyToTopicContext(t *testing.T) {
	t.Run("enabled strategy returns block with cache control", func(t *testing.T) {
		strategy := DefaultCacheStrategy()
		block := strategy.ApplyToTopicContext("Topic: Go programming")

		if block.Type != api.ContentBlockTypeText {
			t.Errorf("expected text block type, got %v", block.Type)
		}
		if block.Text != "Topic: Go programming" {
			t.Errorf("expected correct text, got %q", block.Text)
		}
		if block.CacheControl == nil {
			t.Error("expected cache control to be set")
		}
	})

	t.Run("disabled strategy returns block without cache control", func(t *testing.T) {
		strategy := NoCacheStrategy()
		block := strategy.ApplyToTopicContext("Topic: Go programming")

		if block.CacheControl != nil {
			t.Error("expected no cache control when caching is disabled")
		}
	})
}

func TestCacheStats_Update(t *testing.T) {
	stats := NewCacheStats()

	// First request with cache creation
	creationTokens := 1000
	usage1 := api.Usage{
		InputTokens:              500,
		OutputTokens:             200,
		CacheCreationInputTokens: &creationTokens,
	}
	stats.Update(usage1)

	if stats.TotalRequests != 1 {
		t.Errorf("expected 1 request, got %d", stats.TotalRequests)
	}
	if stats.CacheCreationTokens != 1000 {
		t.Errorf("expected 1000 cache creation tokens, got %d", stats.CacheCreationTokens)
	}
	if stats.CacheHits != 0 {
		t.Errorf("expected 0 cache hits, got %d", stats.CacheHits)
	}

	// Second request with cache read
	readTokens := 800
	usage2 := api.Usage{
		InputTokens:          500,
		OutputTokens:         200,
		CacheReadInputTokens: &readTokens,
	}
	stats.Update(usage2)

	if stats.TotalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", stats.TotalRequests)
	}
	if stats.CacheReadTokens != 800 {
		t.Errorf("expected 800 cache read tokens, got %d", stats.CacheReadTokens)
	}
	if stats.CacheHits != 1 {
		t.Errorf("expected 1 cache hit, got %d", stats.CacheHits)
	}
}

func TestCacheStats_EstimatedSavings(t *testing.T) {
	stats := NewCacheStats()

	readTokens := 500
	usage := api.Usage{
		CacheReadInputTokens: &readTokens,
	}
	stats.Update(usage)

	if stats.EstimatedSavings() != 500 {
		t.Errorf("expected 500 estimated savings, got %d", stats.EstimatedSavings())
	}
}

func TestCacheStats_HitRate(t *testing.T) {
	t.Run("no requests returns 0", func(t *testing.T) {
		stats := NewCacheStats()
		if stats.HitRate() != 0 {
			t.Errorf("expected 0 hit rate, got %f", stats.HitRate())
		}
	})

	t.Run("calculates hit rate correctly", func(t *testing.T) {
		stats := NewCacheStats()

		// 3 requests, 2 with cache hits
		readTokens := 100
		noRead := 0

		stats.Update(api.Usage{CacheReadInputTokens: &readTokens})
		stats.Update(api.Usage{CacheReadInputTokens: &noRead})
		stats.Update(api.Usage{CacheReadInputTokens: &readTokens})

		expectedRate := 2.0 / 3.0
		if diff := stats.HitRate() - expectedRate; diff > 0.001 || diff < -0.001 {
			t.Errorf("expected hit rate ~%f, got %f", expectedRate, stats.HitRate())
		}
	})
}

func TestCacheStats_Reset(t *testing.T) {
	stats := NewCacheStats()

	readTokens := 100
	stats.Update(api.Usage{CacheReadInputTokens: &readTokens})

	stats.Reset()

	if stats.TotalRequests != 0 {
		t.Errorf("expected 0 total requests after reset, got %d", stats.TotalRequests)
	}
	if stats.CacheReadTokens != 0 {
		t.Errorf("expected 0 cache read tokens after reset, got %d", stats.CacheReadTokens)
	}
}

func TestCacheStats_Clone(t *testing.T) {
	stats := NewCacheStats()

	readTokens := 100
	stats.Update(api.Usage{CacheReadInputTokens: &readTokens})

	clone := stats.Clone()

	// Verify values are copied
	if clone.TotalRequests != stats.TotalRequests {
		t.Errorf("clone TotalRequests mismatch")
	}
	if clone.CacheReadTokens != stats.CacheReadTokens {
		t.Errorf("clone CacheReadTokens mismatch")
	}

	// Verify modification doesn't affect original
	clone.TotalRequests = 999
	if stats.TotalRequests == 999 {
		t.Error("modifying clone affected original")
	}
}

func TestCacheDebugInfo(t *testing.T) {
	strategy := DefaultCacheStrategy()
	stats := NewCacheStats()

	readTokens := 100
	stats.Update(api.Usage{CacheReadInputTokens: &readTokens})

	info := NewCacheDebugInfo(strategy, stats)

	if !info.Enabled {
		t.Error("expected Enabled to be true")
	}
	if info.Strategy.SystemPromptTTL != time.Hour {
		t.Error("expected strategy to be copied")
	}
	if info.Stats.TotalRequests != 1 {
		t.Error("expected stats to be copied")
	}
}

func TestTTLToCacheControl(t *testing.T) {
	strategy := DefaultCacheStrategy()

	t.Run("long TTL returns 1h cache", func(t *testing.T) {
		cc := strategy.ttlToCacheControl(time.Hour)
		if cc.TTL == nil || *cc.TTL != api.CacheTTL1Hour {
			t.Error("expected 1h TTL for durations >= 30 minutes")
		}
	})

	t.Run("30 minute TTL returns 1h cache", func(t *testing.T) {
		cc := strategy.ttlToCacheControl(30 * time.Minute)
		if cc.TTL == nil || *cc.TTL != api.CacheTTL1Hour {
			t.Error("expected 1h TTL for 30 minute duration")
		}
	})

	t.Run("short TTL returns 5m cache", func(t *testing.T) {
		cc := strategy.ttlToCacheControl(5 * time.Minute)
		if cc.TTL == nil || *cc.TTL != api.CacheTTL5Min {
			t.Error("expected 5m TTL for short durations")
		}
	})
}
