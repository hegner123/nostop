package api

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"
)

// DiscoverFunc queries the API for available models.
// Set by the Client so the resolver can discover models without a circular dependency.
type DiscoverFunc func(ctx context.Context) ([]ModelInfo, error)

// ModelResolver resolves model IDs by trying alternatives when a requested
// model is unavailable (deprecated, removed, or renamed by Anthropic).
//
// Resolution has three stages:
//  1. Check the cache — if this model was already resolved this session, use that.
//  2. Try the static fallback chain for the same capability tier.
//  3. If static fallbacks are exhausted, query GET /v1/models to discover
//     current models in the same tier and try those.
//
// Once a fallback succeeds, the mapping is cached for the rest of the session.
type ModelResolver struct {
	mu         sync.RWMutex
	resolved   map[string]string   // cache: requested model → working model
	chains     map[string][]string // model → ordered fallback alternatives
	discovered map[string][]string // tier → discovered model IDs (cached)
	discoverFn DiscoverFunc        // set by Client
}

// NewModelResolver creates a resolver with default fallback chains covering
// all known Claude model families.
func NewModelResolver() *ModelResolver {
	return &ModelResolver{
		resolved:   make(map[string]string),
		chains:     defaultFallbackChains(),
		discovered: make(map[string][]string),
	}
}

// defaultFallbackChains builds fallback chains by grouping models into
// capability tiers. Within each tier, every model can fall back to every
// other model in the same tier (newest aliases first).
func defaultFallbackChains() map[string][]string {
	chains := make(map[string][]string)

	// Each tier is ordered newest → oldest. For any model in the tier,
	// fallbacks are all OTHER models in the same tier, preserving order.
	tiers := [][]string{
		{ModelHaiku45Latest, ModelHaiku45, ModelHaiku35Latest, ModelHaiku35},
		{ModelSonnet45Latest, ModelSonnet45, ModelSonnet4},
		{ModelOpus45Latest, ModelOpus45},
	}

	for _, tier := range tiers {
		for _, model := range tier {
			var fallbacks []string
			for _, alt := range tier {
				if alt != model {
					fallbacks = append(fallbacks, alt)
				}
			}
			chains[model] = fallbacks
		}
	}

	return chains
}

// SetDiscoverFunc sets the function used to query available models from the API.
// This is called by the Client during initialization.
func (r *ModelResolver) SetDiscoverFunc(fn DiscoverFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.discoverFn = fn
}

// Resolve returns the cached working model for a requested ID.
// Returns the original model unchanged if no resolution is cached.
func (r *ModelResolver) Resolve(model string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if resolved, ok := r.resolved[model]; ok {
		return resolved
	}
	return model
}

// Fallbacks returns the static fallback alternatives for a model.
// Returns nil if the model has no defined fallback chain.
func (r *ModelResolver) Fallbacks(model string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.chains[model]
}

// Cache records that a requested model resolved to a working model.
func (r *ModelResolver) Cache(requested, working string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.resolved[requested] = working
	log.Printf("[model] Resolved %q → %q (cached for session)", requested, working)
}

// AddFallbacks adds or replaces the fallback chain for a model.
func (r *ModelResolver) AddFallbacks(model string, fallbacks []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.chains[model] = fallbacks
}

// DiscoverFallbacks queries the API for available models in the same tier
// as the given model. Results are cached per tier for the session.
// Returns nil if discovery is unavailable or the tier can't be determined.
func (r *ModelResolver) DiscoverFallbacks(model string, alreadyTried map[string]bool) []string {
	tier := ModelTier(model)
	if tier == "" {
		return nil
	}

	// Check cache under read lock
	r.mu.RLock()
	cached, hasCached := r.discovered[tier]
	discoverFn := r.discoverFn
	r.mu.RUnlock()

	if hasCached {
		return filterUntried(cached, alreadyTried)
	}

	if discoverFn == nil {
		return nil
	}

	// Call the API WITHOUT holding the lock (it's an HTTP call)
	ctx, cancel := context.WithTimeout(context.Background(), 10*secondDuration)
	defer cancel()

	models, err := discoverFn(ctx)
	if err != nil {
		log.Printf("[model] Discovery failed: %v", err)
		return nil
	}

	// Filter to same tier, sort newest first by ID (dated versions sort lexically)
	var tierModels []string
	for _, m := range models {
		if ModelTier(m.ID) == tier {
			tierModels = append(tierModels, m.ID)
		}
	}

	// Sort descending so newest dated versions come first
	sort.Sort(sort.Reverse(sort.StringSlice(tierModels)))

	// Cache under write lock
	r.mu.Lock()
	// Double-check: another goroutine may have filled the cache
	if existing, ok := r.discovered[tier]; ok {
		r.mu.Unlock()
		return filterUntried(existing, alreadyTried)
	}
	r.discovered[tier] = tierModels
	r.mu.Unlock()

	log.Printf("[model] Discovered %d %s-tier models: %v", len(tierModels), tier, tierModels)

	return filterUntried(tierModels, alreadyTried)
}

// secondDuration avoids importing time in a way that conflicts with test mocking.
const secondDuration = 1_000_000_000 // time.Second as a raw Duration value

// filterUntried returns models from candidates that are not in alreadyTried.
func filterUntried(candidates []string, alreadyTried map[string]bool) []string {
	var result []string
	for _, m := range candidates {
		if !alreadyTried[m] {
			result = append(result, m)
		}
	}
	return result
}

// ModelTier returns the capability tier for a model ID: "haiku", "sonnet",
// "opus", or "" for unknown models.
func ModelTier(modelID string) string {
	lower := strings.ToLower(modelID)
	switch {
	case strings.Contains(lower, "haiku"):
		return "haiku"
	case strings.Contains(lower, "sonnet"):
		return "sonnet"
	case strings.Contains(lower, "opus"):
		return "opus"
	default:
		return ""
	}
}

// IsModelNotFound returns true if err is a not-found API error whose
// message references a model. This distinguishes "model doesn't exist"
// from other 404 responses.
func IsModelNotFound(err error) bool {
	apiErr := ExtractAPIError(err)
	if apiErr == nil {
		return false
	}
	if apiErr.ErrorDetails.Type != ErrorTypeNotFound {
		return false
	}
	return strings.Contains(strings.ToLower(apiErr.ErrorDetails.Message), "model")
}

// withModelFallback wraps an API call with automatic model resolution.
// If the resolver is nil, the call passes straight through.
//
// The flow:
//  1. Apply any cached resolution (from a prior fallback this session).
//  2. Attempt the call.
//  3. On model-not-found, iterate the static fallback chain.
//  4. If static fallbacks are exhausted, discover models via the API and try those.
//  5. Cache the first model that succeeds.
//  6. Restore the original model on the request before returning.
func withModelFallback[T any](resolver *ModelResolver, req *Request, fn func() (T, error)) (T, error) {
	originalModel := req.Model

	// Step 1: apply cached resolution
	if resolver != nil {
		req.Model = resolver.Resolve(req.Model)
	}

	// Step 2: try the (possibly resolved) model
	result, err := fn()
	if err == nil {
		return result, nil
	}

	// Only proceed with fallback on model-not-found
	if resolver == nil || !IsModelNotFound(err) {
		var zero T
		return zero, err
	}

	// Track what we've already tried
	tried := map[string]bool{req.Model: true}

	// Step 3: try static fallback chain
	for _, fallback := range resolver.Fallbacks(originalModel) {
		if tried[fallback] {
			continue
		}
		log.Printf("[model] %q not found, trying fallback %q", req.Model, fallback)
		req.Model = fallback
		tried[fallback] = true

		result, err = fn()
		if err == nil {
			resolver.Cache(originalModel, fallback)
			return result, nil
		}
		if !IsModelNotFound(err) {
			break // different error type — stop
		}
	}

	// Step 4: discover models via API if static chain is exhausted
	if IsModelNotFound(err) {
		discovered := resolver.DiscoverFallbacks(originalModel, tried)
		for _, fallback := range discovered {
			log.Printf("[model] Trying discovered model %q", fallback)
			req.Model = fallback
			tried[fallback] = true

			result, err = fn()
			if err == nil {
				resolver.Cache(originalModel, fallback)
				// Also add to static chains for future use
				resolver.AddFallbacks(originalModel, discovered)
				return result, nil
			}
			if !IsModelNotFound(err) {
				break
			}
		}
	}

	// Step 6: restore original model
	req.Model = originalModel

	var zero T
	return zero, err
}
