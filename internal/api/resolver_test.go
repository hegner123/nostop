package api

import (
	"context"
	"os"
	"testing"
)

func TestNewModelResolver(t *testing.T) {
	r := NewModelResolver()
	if r == nil {
		t.Fatal("expected non-nil resolver")
	}
	if len(r.chains) == 0 {
		t.Error("expected non-empty fallback chains")
	}
}

func TestDefaultFallbackChains_AllModelsHaveFallbacks(t *testing.T) {
	r := NewModelResolver()

	models := []string{
		ModelHaiku45Latest, ModelHaiku45, ModelHaiku35Latest, ModelHaiku35,
		ModelSonnet45Latest, ModelSonnet45, ModelSonnet4,
		ModelOpus45Latest, ModelOpus45,
	}

	for _, model := range models {
		fallbacks := r.Fallbacks(model)
		if len(fallbacks) == 0 {
			t.Errorf("model %q has no fallbacks", model)
		}
	}
}

func TestDefaultFallbackChains_StaysInTier(t *testing.T) {
	r := NewModelResolver()

	haikuModels := map[string]bool{
		ModelHaiku45Latest: true, ModelHaiku45: true,
		ModelHaiku35Latest: true, ModelHaiku35: true,
	}
	for _, fb := range r.Fallbacks(ModelHaiku35Latest) {
		if !haikuModels[fb] {
			t.Errorf("haiku fallback %q is not a haiku model", fb)
		}
	}

	sonnetModels := map[string]bool{
		ModelSonnet45Latest: true, ModelSonnet45: true, ModelSonnet4: true,
	}
	for _, fb := range r.Fallbacks(ModelSonnet4) {
		if !sonnetModels[fb] {
			t.Errorf("sonnet fallback %q is not a sonnet model", fb)
		}
	}
}

func TestDefaultFallbackChains_NoSelfReference(t *testing.T) {
	r := NewModelResolver()
	for model, fallbacks := range r.chains {
		for _, fb := range fallbacks {
			if fb == model {
				t.Errorf("model %q has itself in its fallback chain", model)
			}
		}
	}
}

func TestResolve_NoCache(t *testing.T) {
	r := NewModelResolver()
	got := r.Resolve("claude-3-5-haiku-latest")
	if got != "claude-3-5-haiku-latest" {
		t.Errorf("expected original model, got %q", got)
	}
}

func TestResolve_Cached(t *testing.T) {
	r := NewModelResolver()
	r.Cache("claude-3-5-haiku-latest", "claude-haiku-4-5-20251001")

	got := r.Resolve("claude-3-5-haiku-latest")
	if got != "claude-haiku-4-5-20251001" {
		t.Errorf("expected cached resolution, got %q", got)
	}
}

func TestResolve_CacheDoesNotAffectOtherModels(t *testing.T) {
	r := NewModelResolver()
	r.Cache("claude-3-5-haiku-latest", "claude-haiku-4-5")

	got := r.Resolve("claude-sonnet-4-20250514")
	if got != "claude-sonnet-4-20250514" {
		t.Errorf("caching haiku should not affect sonnet, got %q", got)
	}
}

func TestAddFallbacks(t *testing.T) {
	r := NewModelResolver()
	r.AddFallbacks("my-custom-model", []string{"fallback-1", "fallback-2"})

	got := r.Fallbacks("my-custom-model")
	if len(got) != 2 {
		t.Fatalf("expected 2 fallbacks, got %d", len(got))
	}
	if got[0] != "fallback-1" || got[1] != "fallback-2" {
		t.Errorf("unexpected fallbacks: %v", got)
	}
}

func TestModelTier(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"claude-haiku-4-5-20251001", "haiku"},
		{"claude-3-5-haiku-latest", "haiku"},
		{"claude-sonnet-4-5", "sonnet"},
		{"claude-sonnet-4-20250514", "sonnet"},
		{"claude-opus-4-5", "opus"},
		{"gpt-4", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := ModelTier(tt.model)
			if got != tt.want {
				t.Errorf("ModelTier(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestIsModelNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "model not found",
			err: &APIError{
				ErrorDetails: ErrorDetail{
					Type:    ErrorTypeNotFound,
					Message: "model: claude-3-5-haiku-latest",
				},
			},
			want: true,
		},
		{
			name: "not found but not model",
			err: &APIError{
				ErrorDetails: ErrorDetail{
					Type:    ErrorTypeNotFound,
					Message: "resource not found",
				},
			},
			want: false,
		},
		{
			name: "rate limit error",
			err: &APIError{
				ErrorDetails: ErrorDetail{
					Type:    ErrorTypeRateLimit,
					Message: "rate limited",
				},
			},
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsModelNotFound(tt.err)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// withModelFallback — static chain tests
// ---------------------------------------------------------------------------

func TestWithModelFallback_NilResolver(t *testing.T) {
	req := &Request{Model: "some-model"}
	called := false

	_, err := withModelFallback[*Response](nil, req, func() (*Response, error) {
		called = true
		return &Response{}, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("function should have been called")
	}
	if req.Model != "some-model" {
		t.Errorf("model should be unchanged, got %q", req.Model)
	}
}

func TestWithModelFallback_SuccessOnFirstTry(t *testing.T) {
	r := NewModelResolver()
	req := &Request{Model: ModelHaiku45}

	resp, err := withModelFallback(r, req, func() (*Response, error) {
		return &Response{Model: req.Model}, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Model != ModelHaiku45 {
		t.Errorf("expected model %q, got %q", ModelHaiku45, resp.Model)
	}
}

func TestWithModelFallback_FallsBackOnModelNotFound(t *testing.T) {
	r := NewModelResolver()
	req := &Request{Model: ModelHaiku35Latest}

	attempts := 0
	resp, err := withModelFallback(r, req, func() (*Response, error) {
		attempts++
		if req.Model == ModelHaiku35Latest || req.Model == ModelHaiku35 {
			return nil, &APIError{
				ErrorDetails: ErrorDetail{
					Type:    ErrorTypeNotFound,
					Message: "model: " + req.Model,
				},
			}
		}
		return &Response{Model: req.Model}, nil
	})

	if err != nil {
		t.Fatalf("expected fallback to succeed, got: %v", err)
	}
	if attempts < 2 {
		t.Errorf("expected at least 2 attempts, got %d", attempts)
	}
	if resp.Model != ModelHaiku45Latest && resp.Model != ModelHaiku45 {
		t.Errorf("expected haiku 4.5 model, got %q", resp.Model)
	}

	resolved := r.Resolve(ModelHaiku35Latest)
	if resolved == ModelHaiku35Latest {
		t.Error("expected resolution to be cached")
	}
}

func TestWithModelFallback_NonModelErrorStopsFallback(t *testing.T) {
	r := NewModelResolver()
	req := &Request{Model: ModelHaiku35Latest}

	attempts := 0
	_, err := withModelFallback[*Response](r, req, func() (*Response, error) {
		attempts++
		if attempts == 1 {
			return nil, &APIError{
				ErrorDetails: ErrorDetail{
					Type:    ErrorTypeNotFound,
					Message: "model: " + req.Model,
				},
			}
		}
		return nil, &APIError{
			ErrorDetails: ErrorDetail{
				Type:    ErrorTypeAuthentication,
				Message: "invalid API key",
			},
		}
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 2 {
		t.Errorf("should have stopped after auth error, attempts: %d", attempts)
	}
}

func TestWithModelFallback_RestoresOriginalModel(t *testing.T) {
	r := NewModelResolver()
	req := &Request{Model: ModelHaiku35Latest}

	_, err := withModelFallback[*Response](r, req, func() (*Response, error) {
		return nil, &APIError{
			ErrorDetails: ErrorDetail{
				Type:    ErrorTypeNotFound,
				Message: "model: " + req.Model,
			},
		}
	})

	if err == nil {
		t.Fatal("expected error when all fallbacks fail")
	}
	if req.Model != ModelHaiku35Latest {
		t.Errorf("expected model restored to %q, got %q", ModelHaiku35Latest, req.Model)
	}
}

func TestWithModelFallback_UsesCachedResolution(t *testing.T) {
	r := NewModelResolver()
	r.Cache(ModelHaiku35Latest, ModelHaiku45)
	req := &Request{Model: ModelHaiku35Latest}

	resp, err := withModelFallback(r, req, func() (*Response, error) {
		return &Response{Model: req.Model}, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Model != ModelHaiku45 {
		t.Errorf("expected cached model %q, got %q", ModelHaiku45, resp.Model)
	}
}

// ---------------------------------------------------------------------------
// withModelFallback — discovery tests
// ---------------------------------------------------------------------------

func TestWithModelFallback_DiscoveryAfterStaticChainExhausted(t *testing.T) {
	r := NewModelResolver()

	// Set up a discover function that returns a "future" haiku model
	r.SetDiscoverFunc(func(ctx context.Context) ([]ModelInfo, error) {
		return []ModelInfo{
			{ID: "claude-haiku-5-0-20260101", Type: "model"},
			{ID: "claude-sonnet-5-0-20260101", Type: "model"}, // wrong tier
		}, nil
	})

	req := &Request{Model: ModelHaiku35Latest}

	// All known models fail
	resp, err := withModelFallback(r, req, func() (*Response, error) {
		if req.Model == "claude-haiku-5-0-20260101" {
			return &Response{Model: req.Model}, nil
		}
		return nil, &APIError{
			ErrorDetails: ErrorDetail{
				Type:    ErrorTypeNotFound,
				Message: "model: " + req.Model,
			},
		}
	})

	if err != nil {
		t.Fatalf("expected discovery to find working model, got: %v", err)
	}
	if resp.Model != "claude-haiku-5-0-20260101" {
		t.Errorf("expected discovered model, got %q", resp.Model)
	}

	// Should be cached
	resolved := r.Resolve(ModelHaiku35Latest)
	if resolved != "claude-haiku-5-0-20260101" {
		t.Errorf("expected discovered model to be cached, got %q", resolved)
	}
}

func TestWithModelFallback_DiscoveryFiltersToSameTier(t *testing.T) {
	r := NewModelResolver()

	var triedModels []string
	r.SetDiscoverFunc(func(ctx context.Context) ([]ModelInfo, error) {
		return []ModelInfo{
			{ID: "claude-opus-99", Type: "model"},
			{ID: "claude-sonnet-99", Type: "model"},
			{ID: "claude-haiku-99", Type: "model"},
		}, nil
	})

	// Set up: only haiku fallbacks, requesting a haiku model
	// Clear static chains so we go straight to discovery
	r.chains = map[string][]string{}

	req := &Request{Model: "claude-haiku-old"}

	_, err := withModelFallback[*Response](r, req, func() (*Response, error) {
		triedModels = append(triedModels, req.Model)
		return nil, &APIError{
			ErrorDetails: ErrorDetail{
				Type:    ErrorTypeNotFound,
				Message: "model: " + req.Model,
			},
		}
	})

	if err == nil {
		t.Fatal("expected error (all models fail)")
	}

	// Should only have tried haiku models, never opus/sonnet
	for _, m := range triedModels {
		if m == "claude-opus-99" || m == "claude-sonnet-99" {
			t.Errorf("discovery tried wrong-tier model %q", m)
		}
	}
}

func TestDiscoverFallbacks_CachesPerTier(t *testing.T) {
	r := NewModelResolver()

	callCount := 0
	r.SetDiscoverFunc(func(ctx context.Context) ([]ModelInfo, error) {
		callCount++
		return []ModelInfo{
			{ID: "claude-haiku-99", Type: "model"},
		}, nil
	})

	tried := map[string]bool{"claude-haiku-old": true}

	// First call — hits the API
	result1 := r.DiscoverFallbacks("claude-haiku-old", tried)
	if len(result1) != 1 {
		t.Fatalf("expected 1 discovered model, got %d", len(result1))
	}
	if callCount != 1 {
		t.Errorf("expected 1 API call, got %d", callCount)
	}

	// Second call — should use cache
	result2 := r.DiscoverFallbacks("claude-haiku-other", tried)
	if callCount != 1 {
		t.Errorf("expected cache hit (still 1 API call), got %d", callCount)
	}
	if len(result2) != 1 {
		t.Errorf("expected 1 cached model, got %d", len(result2))
	}
}

func TestDiscoverFallbacks_NilDiscoverFunc(t *testing.T) {
	r := NewModelResolver()
	// No discover func set
	result := r.DiscoverFallbacks("claude-haiku-old", nil)
	if result != nil {
		t.Errorf("expected nil without discover func, got %v", result)
	}
}

func TestDiscoverFallbacks_UnknownTier(t *testing.T) {
	r := NewModelResolver()
	r.SetDiscoverFunc(func(ctx context.Context) ([]ModelInfo, error) {
		t.Error("discover func should not be called for unknown tier")
		return nil, nil
	})

	result := r.DiscoverFallbacks("gpt-4-turbo", nil)
	if result != nil {
		t.Errorf("expected nil for unknown tier, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// Integration test — calls the real Models API
// ---------------------------------------------------------------------------

func TestListModels_Integration(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set — skipping integration test")
	}

	client := NewClient(apiKey)
	ctx := context.Background()

	models, err := client.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}

	if len(models) == 0 {
		t.Fatal("expected at least one model")
	}

	t.Logf("Found %d models:", len(models))
	var haikuCount, sonnetCount, opusCount int
	for _, m := range models {
		tier := ModelTier(m.ID)
		switch tier {
		case "haiku":
			haikuCount++
		case "sonnet":
			sonnetCount++
		case "opus":
			opusCount++
		}
		t.Logf("  %s (%s) tier=%s", m.ID, m.DisplayName, tier)
	}

	t.Logf("Tiers: %d haiku, %d sonnet, %d opus", haikuCount, sonnetCount, opusCount)

	if haikuCount == 0 {
		t.Error("expected at least one haiku model")
	}
}
