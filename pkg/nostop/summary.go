// Package nostop provides the main Nostop engine for intelligent topic-based context archival.
package nostop

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hegner123/nostop/internal/storage"
)

// SummarySource indicates where a summary originated.
type SummarySource string

const (
	SummarySourceTopic    SummarySource = "topic"
	SummarySourceWorkUnit SummarySource = "work_unit"
)

// ToolCallInfo tracks tool invocations for summary generation.
type ToolCallInfo struct {
	Name   string
	Target string // file path or other target
	Count  int
}

// SummaryData holds extracted information for building a summary.
type SummaryData struct {
	TopicName      string
	MessageCount   int
	TotalTokens    int
	FilesAccessed  []string
	ToolCalls      map[string]*ToolCallInfo // keyed by tool name
	FirstIntent    string                   // first assistant text (intent)
	LastConclusion string                   // last assistant text (conclusion)
	TestsPassed    int
	TestsFailed    int
}

// BuildArchiveSummary generates a compact summary string from messages about to be archived.
// The summary includes:
// - File paths touched
// - Tool call counts by type
// - First assistant text (intent) and last assistant text (conclusion)
// - Formatted as a compact block suitable for display
func BuildArchiveSummary(messages []storage.Message, topicName string) string {
	data := ExtractSummaryData(messages, topicName)
	return FormatSummary(data)
}

// ExtractSummaryData extracts structured information from messages for summarization.
func ExtractSummaryData(messages []storage.Message, topicName string) *SummaryData {
	data := &SummaryData{
		TopicName:     topicName,
		MessageCount:  len(messages),
		ToolCalls:     make(map[string]*ToolCallInfo),
		FilesAccessed: []string{},
	}

	var assistantTexts []string
	filesMap := make(map[string]struct{})

	for _, msg := range messages {
		data.TotalTokens += msg.TokenCount

		// Parse content blocks from JSON
		var blocks []ContentBlock
		if err := json.Unmarshal([]byte(msg.Content), &blocks); err != nil {
			// If not valid JSON array, treat as raw text
			if msg.Role == storage.RoleAssistant {
				assistantTexts = append(assistantTexts, msg.Content)
			}
			continue
		}

		for _, block := range blocks {
			switch block.Type {
			case "text":
				if msg.Role == storage.RoleAssistant && block.Text != "" {
					assistantTexts = append(assistantTexts, block.Text)
				}

			case "tool_use":
				toolName := block.Name
				if toolName == "" {
					continue
				}

				if data.ToolCalls[toolName] == nil {
					data.ToolCalls[toolName] = &ToolCallInfo{Name: toolName}
				}
				data.ToolCalls[toolName].Count++

				// Extract file paths from tool input
				if block.Input != nil {
					extractFilePaths(block.Input, filesMap, data.ToolCalls[toolName])
				}

			case "tool_result":
				// Check for test results in tool output
				if block.Content != "" {
					checkTestResults(block.Content, data)
				}
			}
		}
	}

	// Convert files map to sorted slice
	for f := range filesMap {
		data.FilesAccessed = append(data.FilesAccessed, f)
	}
	sort.Strings(data.FilesAccessed)

	// Extract first intent and last conclusion
	if len(assistantTexts) > 0 {
		data.FirstIntent = truncateText(assistantTexts[0], 200)
		if len(assistantTexts) > 1 {
			data.LastConclusion = truncateText(assistantTexts[len(assistantTexts)-1], 200)
		}
	}

	return data
}

// ContentBlock represents a content block in the message content JSON.
type ContentBlock struct {
	Type    string         `json:"type"`
	Text    string         `json:"text,omitempty"`
	Name    string         `json:"name,omitempty"`
	ID      string         `json:"id,omitempty"`
	Input   map[string]any `json:"input,omitempty"`
	Content string         `json:"content,omitempty"`
}

// extractFilePaths pulls file paths from tool input parameters.
func extractFilePaths(input map[string]any, filesMap map[string]struct{}, toolInfo *ToolCallInfo) {
	// Common parameter names for file paths
	pathKeys := []string{"file_path", "path", "file", "target", "source", "dir", "directory"}

	for _, key := range pathKeys {
		if val, ok := input[key]; ok {
			switch v := val.(type) {
			case string:
				if v != "" {
					filesMap[v] = struct{}{}
					if toolInfo.Target == "" {
						toolInfo.Target = v
					}
				}
			case []any:
				for _, item := range v {
					if s, ok := item.(string); ok && s != "" {
						filesMap[s] = struct{}{}
					}
				}
			}
		}
	}
}

// checkTestResults looks for test pass/fail indicators in tool output.
func checkTestResults(content string, data *SummaryData) {
	lower := strings.ToLower(content)

	// Simple heuristics for test results
	if strings.Contains(lower, "pass") || strings.Contains(lower, "ok") {
		if strings.Contains(lower, "test") || strings.Contains(lower, "spec") {
			data.TestsPassed++
		}
	}
	if strings.Contains(lower, "fail") || strings.Contains(lower, "error") {
		if strings.Contains(lower, "test") || strings.Contains(lower, "spec") {
			data.TestsFailed++
		}
	}
}

// truncateText shortens text to maxLen, adding ellipsis if truncated.
func truncateText(text string, maxLen int) string {
	text = strings.TrimSpace(text)
	// Replace newlines with spaces for compact display
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ") // normalize whitespace

	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen-3] + "..."
}

// FormatSummary creates a formatted summary string from extracted data.
func FormatSummary(data *SummaryData) string {
	var sb strings.Builder

	// Header line
	sb.WriteString(fmt.Sprintf("[Archived: %q — %d messages, %s tokens]\n",
		data.TopicName,
		data.MessageCount,
		formatTokenCount(data.TotalTokens),
	))

	// Files line (if any)
	if len(data.FilesAccessed) > 0 {
		files := data.FilesAccessed
		if len(files) > 5 {
			files = append(files[:5], fmt.Sprintf("(+%d more)", len(data.FilesAccessed)-5))
		}
		sb.WriteString("Files: ")
		sb.WriteString(strings.Join(files, ", "))
		sb.WriteString("\n")
	}

	// Actions line (tool calls)
	if len(data.ToolCalls) > 0 {
		sb.WriteString("Actions: ")
		var actions []string
		for _, tc := range sortedToolCalls(data.ToolCalls) {
			actions = append(actions, formatToolCall(tc))
		}
		sb.WriteString(strings.Join(actions, ", "))

		// Append test results if any
		if data.TestsPassed > 0 || data.TestsFailed > 0 {
			if data.TestsFailed > 0 {
				sb.WriteString(fmt.Sprintf(", %d test fail", data.TestsFailed))
			}
			if data.TestsPassed > 0 {
				sb.WriteString(fmt.Sprintf(", %d test pass", data.TestsPassed))
			}
		}
		sb.WriteString("\n")
	}

	// Result line (conclusion or intent)
	if data.LastConclusion != "" {
		sb.WriteString("Result: ")
		sb.WriteString(data.LastConclusion)
	} else if data.FirstIntent != "" {
		sb.WriteString("Intent: ")
		sb.WriteString(data.FirstIntent)
	}

	return strings.TrimRight(sb.String(), "\n")
}

// formatTokenCount formats token count with K suffix for thousands.
func formatTokenCount(tokens int) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fK", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

// sortedToolCalls returns tool calls sorted by count (descending).
func sortedToolCalls(tools map[string]*ToolCallInfo) []*ToolCallInfo {
	var result []*ToolCallInfo
	for _, tc := range tools {
		result = append(result, tc)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Count > result[j].Count
	})
	return result
}

// formatToolCall formats a tool call for display.
func formatToolCall(tc *ToolCallInfo) string {
	name := simplifyToolName(tc.Name)
	if tc.Count == 1 {
		return fmt.Sprintf("1 %s", name)
	}
	return fmt.Sprintf("%d %s", tc.Count, name)
}

// simplifyToolName converts tool names to simpler display names.
func simplifyToolName(name string) string {
	// Map common tool names to shorter versions
	switch name {
	case "read", "Read":
		return "read"
	case "write", "Write":
		return "write"
	case "bash", "Bash":
		return "bash"
	case "edit", "Edit":
		return "edit"
	case "checkfor":
		return "search"
	case "repfor":
		return "replace"
	default:
		return strings.ToLower(name)
	}
}
