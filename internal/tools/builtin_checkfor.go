package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// checkforMatch is a single match within a file.
type checkforMatch struct {
	Line          int      `json:"line"`
	EndLine       int      `json:"end_line,omitempty"`
	Content       string   `json:"content"`
	ContextBefore []string `json:"context_before,omitempty"`
	ContextAfter  []string `json:"context_after,omitempty"`
}

// checkforFileMatch groups matches within a file.
type checkforFileMatch struct {
	Path    string          `json:"path"`
	Matches []checkforMatch `json:"matches"`
}

// checkforDirResult groups matches within a directory.
type checkforDirResult struct {
	Dir             string              `json:"dir"`
	MatchesFound    int                 `json:"matches_found"`
	OriginalMatches int                 `json:"original_matches,omitempty"`
	FilteredMatches int                 `json:"filtered_matches,omitempty"`
	Files           []checkforFileMatch `json:"files"`
}

// checkforResult is the top-level output.
type checkforResult struct {
	Directories []checkforDirResult `json:"directories"`
}

// checkforConfig holds search parameters.
type checkforConfig struct {
	dirs            []string
	files           []string
	search          string
	ext             string
	exclude         []string
	caseInsensitive bool
	wholeWord       bool
	contextLines    int
	hideFilterStats bool
}

// CheckforDef searches files for exact string matches.
var CheckforDef = ToolDef{
	Name:        "checkfor",
	Description: "Search files in directories for a string pattern. Single-depth (non-recursive) scanning with optional extension filtering, case-insensitive search, whole-word matching, and context lines.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"search": map[string]any{
				"type":        "string",
				"description": "String pattern to search for. Supports multi-line search: \\n in the string matches literal newlines.",
			},
			"dir": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Array of directory paths to search. Defaults to current directory if neither dir nor file is provided.",
			},
			"file": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Array of file paths to search directly. Bypasses directory scanning.",
			},
			"ext": map[string]any{
				"type":        "string",
				"description": "File extension to filter (e.g., '.go', '.rtf').",
			},
			"case_insensitive": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Perform case-insensitive search.",
			},
			"whole_word": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Match whole words only.",
			},
			"context": map[string]any{
				"type":        "integer",
				"default":     0,
				"description": "Number of context lines before and after each match.",
			},
			"exclude": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Array of strings to exclude from results.",
			},
		},
		"required": []string{"search"},
	},
	Builtin: builtinCheckfor,
	Timeout: 30 * time.Second,
}

func builtinCheckfor(ctx context.Context, input map[string]any, workDir string) Result {
	search, ok := input["search"].(string)
	if !ok || search == "" {
		return Result{IsError: true, Error: "search is required"}
	}

	cfg := checkforConfig{
		search: search,
	}

	// Parse dir param (string or array)
	if v, exists := input["dir"]; exists {
		switch d := v.(type) {
		case string:
			cfg.dirs = []string{d}
		case []any:
			for _, item := range d {
				if s, ok := item.(string); ok {
					cfg.dirs = append(cfg.dirs, s)
				}
			}
		}
	}

	// Parse file param (string or array)
	if v, exists := input["file"]; exists {
		switch f := v.(type) {
		case string:
			cfg.files = []string{f}
		case []any:
			for _, item := range f {
				if s, ok := item.(string); ok {
					cfg.files = append(cfg.files, s)
				}
			}
		}
	}

	// Default to current directory
	if len(cfg.dirs) == 0 && len(cfg.files) == 0 {
		if workDir != "" {
			cfg.dirs = []string{workDir}
		} else {
			cfg.dirs = []string{"."}
		}
	}

	// Resolve relative dirs
	for i := range cfg.dirs {
		cfg.dirs[i] = resolvePath(cfg.dirs[i], workDir)
	}
	for i := range cfg.files {
		cfg.files[i] = resolvePath(cfg.files[i], workDir)
	}

	if v, ok := input["ext"].(string); ok {
		cfg.ext = v
	}
	if v, ok := input["case_insensitive"].(bool); ok {
		cfg.caseInsensitive = v
	}
	if v, ok := input["whole_word"].(bool); ok {
		cfg.wholeWord = v
	}
	if v, exists := input["context"]; exists {
		cfg.contextLines = toInt(v)
	}
	if v, ok := input["hide_filter_stats"].(bool); ok {
		cfg.hideFilterStats = v
	}
	if v, ok := input["exclude"].([]any); ok {
		for _, item := range v {
			if s, ok := item.(string); ok {
				cfg.exclude = append(cfg.exclude, s)
			}
		}
	}

	r, err := doCheckforSearch(cfg)
	if err != nil {
		return Result{IsError: true, Error: err.Error()}
	}

	return resultJSON(r)
}

func doCheckforSearch(cfg checkforConfig) (*checkforResult, error) {
	result := &checkforResult{
		Directories: make([]checkforDirResult, 0, len(cfg.dirs)+1),
	}

	for _, dir := range cfg.dirs {
		dirResult, err := checkforSearchDir(dir, cfg)
		if err != nil {
			return nil, err
		}
		result.Directories = append(result.Directories, *dirResult)
	}

	if len(cfg.files) > 0 {
		fileResult, err := checkforSearchFiles(cfg)
		if err != nil {
			return nil, err
		}
		result.Directories = append(result.Directories, *fileResult)
	}

	return result, nil
}

func checkforSearchDir(dir string, cfg checkforConfig) (*checkforDirResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	dirResult := &checkforDirResult{
		Dir:   dir,
		Files: make([]checkforFileMatch, 0),
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if cfg.ext != "" && !strings.HasSuffix(name, cfg.ext) {
			continue
		}

		fullPath := filepath.Join(dir, name)
		matches, origCount, filtCount, searchErr := checkforSearchFile(fullPath, cfg)
		if searchErr != nil {
			continue // skip unreadable files
		}

		if !cfg.hideFilterStats && len(cfg.exclude) > 0 {
			dirResult.OriginalMatches += origCount
			dirResult.FilteredMatches += filtCount
		}

		if len(matches) > 0 {
			dirResult.Files = append(dirResult.Files, checkforFileMatch{
				Path:    name,
				Matches: matches,
			})
			dirResult.MatchesFound += len(matches)
		}
	}

	return dirResult, nil
}

func checkforSearchFiles(cfg checkforConfig) (*checkforDirResult, error) {
	dirResult := &checkforDirResult{
		Dir:   "(files)",
		Files: make([]checkforFileMatch, 0),
	}

	for _, filePath := range cfg.files {
		if cfg.ext != "" && !strings.HasSuffix(filePath, cfg.ext) {
			continue
		}

		matches, origCount, filtCount, err := checkforSearchFile(filePath, cfg)
		if err != nil {
			continue
		}

		if !cfg.hideFilterStats && len(cfg.exclude) > 0 {
			dirResult.OriginalMatches += origCount
			dirResult.FilteredMatches += filtCount
		}

		if len(matches) > 0 {
			dirResult.Files = append(dirResult.Files, checkforFileMatch{
				Path:    filePath,
				Matches: matches,
			})
			dirResult.MatchesFound += len(matches)
		}
	}

	return dirResult, nil
}

func checkforSearchFile(path string, cfg checkforConfig) ([]checkforMatch, int, int, error) {
	if checkforIsMultiline(cfg.search) {
		return checkforSearchFileMultiline(path, cfg)
	}
	return checkforSearchFileSingleLine(path, cfg)
}

func checkforSearchFileSingleLine(path string, cfg checkforConfig) ([]checkforMatch, int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, 0, 0, scanErr
	}

	var matches []checkforMatch
	originalCount := 0
	filteredCount := 0

	searchTerm := cfg.search
	if cfg.caseInsensitive {
		searchTerm = strings.ToLower(searchTerm)
	}

	for i, line := range lines {
		lineToCheck := line
		if cfg.caseInsensitive {
			lineToCheck = strings.ToLower(line)
		}

		found := false
		if cfg.wholeWord {
			found = checkforContainsWholeWord(lineToCheck, searchTerm)
		} else {
			found = strings.Contains(lineToCheck, searchTerm)
		}

		if !found {
			continue
		}

		originalCount++

		if checkforIsExcluded(line, lineToCheck, cfg) {
			filteredCount++
			continue
		}

		match := checkforMatch{
			Line:    i + 1,
			Content: line,
		}
		if cfg.contextLines > 0 {
			match.ContextBefore = checkforGetContextBefore(lines, i, cfg.contextLines)
			match.ContextAfter = checkforGetContextAfter(lines, i, cfg.contextLines)
		}
		matches = append(matches, match)
	}

	return matches, originalCount, filteredCount, nil
}

func checkforSearchFileMultiline(path string, cfg checkforConfig) ([]checkforMatch, int, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}

	content := string(data)

	// Detect and normalize line endings
	lineEnding := "\n"
	if strings.Contains(content, "\r\n") {
		lineEnding = "\r\n"
	}

	search := cfg.search
	if lineEnding == "\r\n" {
		search = strings.ReplaceAll(search, "\n", "\r\n")
	}

	searchTerm := search
	contentToSearch := content
	if cfg.caseInsensitive {
		searchTerm = strings.ToLower(search)
		contentToSearch = strings.ToLower(content)
	}

	lines := strings.Split(content, lineEnding)

	var matches []checkforMatch
	originalCount := 0
	filteredCount := 0

	offset := 0
	for {
		var idx int
		if cfg.wholeWord {
			idx = checkforIndexOfWholeWord(contentToSearch[offset:], searchTerm)
		} else {
			idx = strings.Index(contentToSearch[offset:], searchTerm)
		}

		if idx == -1 {
			break
		}

		actualIdx := offset + idx
		matchEnd := actualIdx + len(searchTerm)
		originalCount++

		matchedContent := content[actualIdx:matchEnd]

		startLine := strings.Count(content[:actualIdx], "\n") + 1
		endLine := startLine
		if matchEnd > actualIdx {
			endLine = strings.Count(content[:matchEnd-1], "\n") + 1
		}

		// Check exclude
		excluded := false
		if len(cfg.exclude) > 0 {
			lineStart := strings.LastIndex(content[:actualIdx], lineEnding)
			if lineStart == -1 {
				lineStart = 0
			} else {
				lineStart += len(lineEnding)
			}
			lineEndPos := strings.Index(content[matchEnd:], lineEnding)
			if lineEndPos == -1 {
				lineEndPos = len(content)
			} else {
				lineEndPos = matchEnd + lineEndPos
			}
			spanningLines := content[lineStart:lineEndPos]

			for _, excl := range cfg.exclude {
				exclToCheck := excl
				linesToCheck := spanningLines
				if cfg.caseInsensitive {
					exclToCheck = strings.ToLower(excl)
					linesToCheck = strings.ToLower(spanningLines)
				}
				if strings.Contains(linesToCheck, exclToCheck) {
					excluded = true
					break
				}
			}
		}

		if excluded {
			filteredCount++
		} else {
			match := checkforMatch{
				Line:    startLine,
				Content: matchedContent,
			}
			if startLine != endLine {
				match.EndLine = endLine
			}
			if cfg.contextLines > 0 {
				match.ContextBefore = checkforGetContextBefore(lines, startLine-1, cfg.contextLines)
				match.ContextAfter = checkforGetContextAfter(lines, endLine-1, cfg.contextLines)
			}
			matches = append(matches, match)
		}

		offset = matchEnd
	}

	return matches, originalCount, filteredCount, nil
}

func checkforIsMultiline(search string) bool {
	return strings.Contains(search, "\n")
}

func checkforContainsWholeWord(text, word string) bool {
	offset := 0
	for {
		idx := strings.Index(text[offset:], word)
		if idx == -1 {
			return false
		}
		actualIdx := offset + idx
		beforeOk := actualIdx == 0 || !checkforIsWordChar(rune(text[actualIdx-1]))
		afterIdx := actualIdx + len(word)
		afterOk := afterIdx >= len(text) || !checkforIsWordChar(rune(text[afterIdx]))

		if beforeOk && afterOk {
			return true
		}
		offset = actualIdx + 1
	}
}

func checkforIndexOfWholeWord(text, word string) int {
	offset := 0
	for {
		idx := strings.Index(text[offset:], word)
		if idx == -1 {
			return -1
		}
		actualIdx := offset + idx
		beforeOk := actualIdx == 0 || !checkforIsWordChar(rune(text[actualIdx-1]))
		afterIdx := actualIdx + len(word)
		afterOk := afterIdx >= len(text) || !checkforIsWordChar(rune(text[afterIdx]))

		if beforeOk && afterOk {
			return actualIdx
		}
		offset = actualIdx + 1
	}
}

func checkforIsWordChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

func checkforIsExcluded(originalLine, loweredLine string, cfg checkforConfig) bool {
	for _, excl := range cfg.exclude {
		exclToCheck := excl
		lineToCheck := originalLine
		if cfg.caseInsensitive {
			exclToCheck = strings.ToLower(excl)
			lineToCheck = loweredLine
		}
		if strings.Contains(lineToCheck, exclToCheck) {
			return true
		}
	}
	return false
}

func checkforGetContextBefore(lines []string, currentIdx, count int) []string {
	start := currentIdx - count
	if start < 0 {
		start = 0
	}
	return lines[start:currentIdx]
}

func checkforGetContextAfter(lines []string, currentIdx, count int) []string {
	end := currentIdx + count + 1
	if end > len(lines) {
		end = len(lines)
	}
	return lines[currentIdx+1 : end]
}
