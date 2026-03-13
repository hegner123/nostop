package topic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hegner123/nostop/internal/api"
)

// DefaultScoringModel is the default model for relevance scoring (fast and cheap).
const DefaultScoringModel = api.ModelHaiku45Latest

// TopicScorer uses Claude to score topic relevance to the current query/context.
type TopicScorer struct {
	client *api.Client
	model  string
}

// NewTopicScorer creates a new topic scorer.
// If model is empty, uses claude-haiku-4-5-20251001 for cost efficiency.
func NewTopicScorer(client *api.Client, model string) *TopicScorer {
	if model == "" {
		model = DefaultScoringModel
	}
	return &TopicScorer{
		client: client,
		model:  model,
	}
}

// RelevanceScore represents the result of scoring a topic's relevance.
type RelevanceScore struct {
	Score  float64 `json:"score"`  // 0.0-1.0
	Reason string  `json:"reason"` // Brief explanation
}

// relevanceScoringResponse is the expected JSON response from Claude for single topic scoring.
type relevanceScoringResponse struct {
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// batchScoringResponse is the expected JSON response from Claude for batch scoring.
type batchScoringResponse struct {
	Scores []topicScoreEntry `json:"scores"`
}

// topicScoreEntry represents a single topic's score in a batch response.
type topicScoreEntry struct {
	TopicID string  `json:"topic_id"`
	Score   float64 `json:"score"`
	Reason  string  `json:"reason"`
}

// ScoreRelevance uses Claude (Haiku) to score how relevant a topic is to the current query/context.
// Returns a score from 0.0 to 1.0.
//
// Scoring criteria:
//   - 0.9-1.0: Direct continuation or explicit reference
//   - 0.7-0.8: Closely related, same domain
//   - 0.4-0.6: Tangentially related
//   - 0.1-0.3: Weak connection
//   - 0.0: Completely unrelated
func (ts *TopicScorer) ScoreRelevance(ctx context.Context, topic Topic, currentQuery string) (float64, error) {
	if currentQuery == "" {
		return 0.0, fmt.Errorf("current query cannot be empty")
	}

	// Build the scoring prompt
	prompt := ts.buildSingleScoringPrompt(topic, currentQuery)

	// Create the request
	req := &api.Request{
		Model:     ts.model,
		MaxTokens: 300,
		Messages: []api.MessageParam{
			api.NewUserMessage(prompt),
		},
	}

	// Send to Claude
	resp, err := ts.client.Send(ctx, req)
	if err != nil {
		return 0.0, fmt.Errorf("send relevance scoring request: %w", err)
	}

	// Parse the response
	text := resp.GetText()
	score, err := ts.parseSingleScoringResponse(text)
	if err != nil {
		return 0.0, err
	}

	return score.Score, nil
}

// ScoreRelevanceWithReason is like ScoreRelevance but also returns the reasoning.
func (ts *TopicScorer) ScoreRelevanceWithReason(ctx context.Context, topic Topic, currentQuery string) (*RelevanceScore, error) {
	if currentQuery == "" {
		return nil, fmt.Errorf("current query cannot be empty")
	}

	// Build the scoring prompt
	prompt := ts.buildSingleScoringPrompt(topic, currentQuery)

	// Create the request
	req := &api.Request{
		Model:     ts.model,
		MaxTokens: 300,
		Messages: []api.MessageParam{
			api.NewUserMessage(prompt),
		},
	}

	// Send to Claude
	resp, err := ts.client.Send(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("send relevance scoring request: %w", err)
	}

	// Parse the response
	text := resp.GetText()
	return ts.parseSingleScoringResponse(text)
}

// ScoreAllTopics scores multiple topics at once for efficiency.
// Returns a map of topic ID -> relevance score.
func (ts *TopicScorer) ScoreAllTopics(ctx context.Context, topics []Topic, currentQuery string) (map[string]float64, error) {
	if len(topics) == 0 {
		return make(map[string]float64), nil
	}

	if currentQuery == "" {
		return nil, fmt.Errorf("current query cannot be empty")
	}

	// For a single topic, use the simpler single-topic method
	if len(topics) == 1 {
		score, err := ts.ScoreRelevance(ctx, topics[0], currentQuery)
		if err != nil {
			return nil, err
		}
		return map[string]float64{topics[0].ID: score}, nil
	}

	// Build the batch scoring prompt
	prompt := ts.buildBatchScoringPrompt(topics, currentQuery)

	// Create the request
	req := &api.Request{
		Model:     ts.model,
		MaxTokens: 100 + (len(topics) * 100), // Scale max tokens with number of topics
		Messages: []api.MessageParam{
			api.NewUserMessage(prompt),
		},
	}

	// Send to Claude
	resp, err := ts.client.Send(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("send batch scoring request: %w", err)
	}

	// Parse the response
	text := resp.GetText()
	return ts.parseBatchScoringResponse(text, topics)
}

// ScoreAllTopicsWithReasons scores multiple topics and returns scores with reasoning.
func (ts *TopicScorer) ScoreAllTopicsWithReasons(ctx context.Context, topics []Topic, currentQuery string) (map[string]*RelevanceScore, error) {
	if len(topics) == 0 {
		return make(map[string]*RelevanceScore), nil
	}

	if currentQuery == "" {
		return nil, fmt.Errorf("current query cannot be empty")
	}

	// For a single topic, use the simpler single-topic method
	if len(topics) == 1 {
		score, err := ts.ScoreRelevanceWithReason(ctx, topics[0], currentQuery)
		if err != nil {
			return nil, err
		}
		return map[string]*RelevanceScore{topics[0].ID: score}, nil
	}

	// Build the batch scoring prompt
	prompt := ts.buildBatchScoringPrompt(topics, currentQuery)

	// Create the request
	req := &api.Request{
		Model:     ts.model,
		MaxTokens: 100 + (len(topics) * 100),
		Messages: []api.MessageParam{
			api.NewUserMessage(prompt),
		},
	}

	// Send to Claude
	resp, err := ts.client.Send(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("send batch scoring request: %w", err)
	}

	// Parse the response
	text := resp.GetText()
	return ts.parseBatchScoringResponseWithReasons(text, topics)
}

// buildSingleScoringPrompt constructs the prompt for scoring a single topic.
func (ts *TopicScorer) buildSingleScoringPrompt(topic Topic, currentQuery string) string {
	return fmt.Sprintf(`You are a topic relevance analyzer. Score how relevant the given topic is to the current query/context.

Topic Information:
- Name: %s
- Keywords: %s

Current Query/Context:
%s

Task: Analyze the relationship between this topic and the current query. Provide a relevance score from 0.0 to 1.0.

Scoring Guidelines:
- 0.9-1.0: Direct continuation or explicit reference to the topic
- 0.7-0.8: Closely related, same domain or subject area
- 0.4-0.6: Tangentially related, some connection
- 0.1-0.3: Weak connection, vague relationship
- 0.0: Completely unrelated

Consider these factors:
1. Explicit references to the topic or its keywords
2. Keyword overlap between query and topic
3. Semantic relatedness (same domain, related concepts)
4. Whether the query builds on or continues the topic

Respond with ONLY valid JSON in this exact format:
{"score": 0.0, "reason": "Brief explanation of the score"}

The score must be a number between 0.0 and 1.0.
The reason should be 1-2 sentences explaining why.`, topic.Name, formatKeywords(topic.Keywords), currentQuery)
}

// buildBatchScoringPrompt constructs the prompt for scoring multiple topics.
func (ts *TopicScorer) buildBatchScoringPrompt(topics []Topic, currentQuery string) string {
	var topicsText strings.Builder
	for i, topic := range topics {
		topicsText.WriteString(fmt.Sprintf("%d. ID: %s\n   Name: %s\n   Keywords: %s\n\n",
			i+1, topic.ID, topic.Name, formatKeywords(topic.Keywords)))
	}

	return fmt.Sprintf(`You are a topic relevance analyzer. Score how relevant each topic is to the current query/context.

Topics to Score:
%s
Current Query/Context:
%s

Task: Analyze the relationship between each topic and the current query. Provide a relevance score from 0.0 to 1.0 for each.

Scoring Guidelines:
- 0.9-1.0: Direct continuation or explicit reference to the topic
- 0.7-0.8: Closely related, same domain or subject area
- 0.4-0.6: Tangentially related, some connection
- 0.1-0.3: Weak connection, vague relationship
- 0.0: Completely unrelated

Consider these factors:
1. Explicit references to the topic or its keywords
2. Keyword overlap between query and topic
3. Semantic relatedness (same domain, related concepts)
4. Whether the query builds on or continues the topic

Respond with ONLY valid JSON in this exact format:
{
  "scores": [
    {"topic_id": "id1", "score": 0.0, "reason": "Brief explanation"},
    {"topic_id": "id2", "score": 0.0, "reason": "Brief explanation"}
  ]
}

Each score must be a number between 0.0 and 1.0.
Each reason should be 1-2 sentences explaining why.
Include ALL topics in the response.`, topicsText.String(), currentQuery)
}

// parseSingleScoringResponse parses Claude's response for single topic scoring.
func (ts *TopicScorer) parseSingleScoringResponse(text string) (*RelevanceScore, error) {
	// Try to extract JSON from the response
	jsonStr := extractJSON(text)
	if jsonStr == "" {
		return nil, fmt.Errorf("no valid JSON found in response: %s", truncateText(text, 200))
	}

	var resp relevanceScoringResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		return nil, fmt.Errorf("parse relevance scoring response: %w (json: %s)", err, jsonStr)
	}

	// Clamp score to valid range
	score := clampScore(resp.Score)

	return &RelevanceScore{
		Score:  score,
		Reason: resp.Reason,
	}, nil
}

// parseBatchScoringResponse parses Claude's response for batch scoring.
func (ts *TopicScorer) parseBatchScoringResponse(text string, topics []Topic) (map[string]float64, error) {
	// Try to extract JSON from the response
	jsonStr := extractJSON(text)
	if jsonStr == "" {
		return nil, fmt.Errorf("no valid JSON found in response: %s", truncateText(text, 200))
	}

	var resp batchScoringResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		return nil, fmt.Errorf("parse batch scoring response: %w (json: %s)", err, jsonStr)
	}

	// Build result map
	result := make(map[string]float64, len(topics))

	// Create a set of valid topic IDs for validation
	validIDs := make(map[string]bool, len(topics))
	for _, topic := range topics {
		validIDs[topic.ID] = true
		// Initialize with default score in case some are missing
		result[topic.ID] = 0.0
	}

	// Populate from response
	for _, entry := range resp.Scores {
		if validIDs[entry.TopicID] {
			result[entry.TopicID] = clampScore(entry.Score)
		}
	}

	return result, nil
}

// parseBatchScoringResponseWithReasons parses Claude's response for batch scoring with reasons.
func (ts *TopicScorer) parseBatchScoringResponseWithReasons(text string, topics []Topic) (map[string]*RelevanceScore, error) {
	// Try to extract JSON from the response
	jsonStr := extractJSON(text)
	if jsonStr == "" {
		return nil, fmt.Errorf("no valid JSON found in response: %s", truncateText(text, 200))
	}

	var resp batchScoringResponse
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		return nil, fmt.Errorf("parse batch scoring response: %w (json: %s)", err, jsonStr)
	}

	// Build result map
	result := make(map[string]*RelevanceScore, len(topics))

	// Create a set of valid topic IDs for validation
	validIDs := make(map[string]bool, len(topics))
	for _, topic := range topics {
		validIDs[topic.ID] = true
		// Initialize with default score in case some are missing
		result[topic.ID] = &RelevanceScore{Score: 0.0, Reason: "Not scored"}
	}

	// Populate from response
	for _, entry := range resp.Scores {
		if validIDs[entry.TopicID] {
			result[entry.TopicID] = &RelevanceScore{
				Score:  clampScore(entry.Score),
				Reason: entry.Reason,
			}
		}
	}

	return result, nil
}

// formatKeywords formats a slice of keywords for display.
func formatKeywords(keywords []string) string {
	if len(keywords) == 0 {
		return "(none)"
	}
	return strings.Join(keywords, ", ")
}

// clampScore ensures the score is within the valid range [0.0, 1.0].
func clampScore(score float64) float64 {
	if score < 0.0 {
		return 0.0
	}
	if score > 1.0 {
		return 1.0
	}
	return score
}

// Model returns the model being used for relevance scoring.
func (ts *TopicScorer) Model() string {
	return ts.model
}

// SetModel updates the model used for relevance scoring.
func (ts *TopicScorer) SetModel(model string) {
	ts.model = model
}
