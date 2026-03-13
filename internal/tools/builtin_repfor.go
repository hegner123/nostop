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

// repforFileModification describes changes to a single file.
type repforFileModification struct {
	Path         string `json:"path"`
	LinesChanged int    `json:"lines_changed"`
	Replacements int    `json:"replacements"`
}

// repforDirResult groups modifications within a directory.
type repforDirResult struct {
	Dir               string                   `json:"dir"`
	FilesModified     int                      `json:"files_modified"`
	LinesChanged      int                      `json:"lines_changed"`
	TotalReplacements int                      `json:"total_replacements"`
	Files             []repforFileModification `json:"files"`
}

// repforResult is the top-level output.
type repforResult struct {
	Summary     string            `json:"summary"`
	Directories []repforDirResult `json:"directories"`
	DryRun      bool              `json:"dry_run,omitempty"`
}

// repforConfig holds replacement parameters.
type repforConfig struct {
	dirs            []string
	files           []string
	search          string
	replace         string
	ext             string
	exclude         []string
	caseInsensitive bool
	wholeWord       bool
	dryRun          bool
	recursive       bool
}

// RepforDef performs search-and-replace across files.
var RepforDef = ToolDef{
	Name:        "repfor",
	Description: "Search and replace strings in files across directories. Supports recursive scanning, extension filtering, case-insensitive search, whole-word matching, dry-run preview, and exclude filters.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"search": map[string]any{
				"type":        "string",
				"description": "String to search for. Use \\n to match literal newlines for multi-line patterns.",
			},
			"replace": map[string]any{
				"type":        "string",
				"description": "String to replace matches with. Use \\n to insert literal newlines.",
			},
			"dir": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Array of directory paths to search. Defaults to current directory.",
			},
			"file": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Array of file paths to process. Takes precedence over dir.",
			},
			"ext": map[string]any{
				"type":        "string",
				"description": "File extension to filter (e.g., '.go').",
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
			"dry_run": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Preview changes without modifying files.",
			},
			"recursive": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Recursively search subdirectories.",
			},
			"exclude": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Array of strings to exclude from replacement.",
			},
		},
		"required": []string{"search", "replace"},
	},
	Builtin: builtinRepfor,
	Timeout: 60 * time.Second,
}

func builtinRepfor(ctx context.Context, input map[string]any, workDir string) Result {
	search, ok := input["search"].(string)
	if !ok {
		return Result{IsError: true, Error: "search is required"}
	}
	replace, ok := input["replace"].(string)
	if !ok {
		return Result{IsError: true, Error: "replace is required"}
	}

	// Unescape literal \n, \r, \t from JSON
	search = repforUnescape(search)
	replace = repforUnescape(replace)

	cfg := repforConfig{
		search:  search,
		replace: replace,
	}

	// Parse dir/file params
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
	if v, exists := input["file"]; exists {
		switch f := v.(type) {
		case string:
			if f != "" {
				cfg.files = []string{f}
			}
		case []any:
			for _, item := range f {
				if s, ok := item.(string); ok {
					cfg.files = append(cfg.files, s)
				}
			}
		}
	}

	if len(cfg.dirs) == 0 && len(cfg.files) == 0 {
		if workDir != "" {
			cfg.dirs = []string{workDir}
		} else {
			cfg.dirs = []string{"."}
		}
	}

	// Resolve paths
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
	if v, ok := input["dry_run"].(bool); ok {
		cfg.dryRun = v
	}
	if v, ok := input["recursive"].(bool); ok {
		cfg.recursive = v
	}
	if v, ok := input["exclude"].([]any); ok {
		for _, item := range v {
			if s, ok := item.(string); ok {
				cfg.exclude = append(cfg.exclude, s)
			}
		}
	}

	r, err := doRepfor(cfg)
	if err != nil {
		return Result{IsError: true, Error: err.Error()}
	}

	return resultJSON(r)
}

func repforUnescape(s string) string {
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\r", "\r")
	s = strings.ReplaceAll(s, "\\t", "\t")
	return s
}

func doRepfor(cfg repforConfig) (*repforResult, error) {
	result := &repforResult{
		Directories: make([]repforDirResult, 0),
		DryRun:      cfg.dryRun,
	}

	if len(cfg.files) > 0 {
		dirResult, err := repforProcessFiles(cfg.files, cfg)
		if err != nil {
			return nil, err
		}
		result.Directories = append(result.Directories, *dirResult)
	} else {
		dirsToProcess := cfg.dirs
		if cfg.recursive {
			dirsToProcess = repforCollectDirsRecursive(cfg.dirs)
		}

		for _, dir := range dirsToProcess {
			dirResult, err := repforProcessDir(dir, cfg)
			if err != nil {
				return nil, err
			}
			result.Directories = append(result.Directories, *dirResult)
		}
	}

	// Build summary
	totalFiles := 0
	totalLines := 0
	totalReplacements := 0
	for _, dr := range result.Directories {
		totalFiles += dr.FilesModified
		totalLines += dr.LinesChanged
		totalReplacements += dr.TotalReplacements
	}

	action := "Modified"
	if cfg.dryRun {
		action = "Would modify"
	}

	fileWord := "files"
	if totalFiles == 1 {
		fileWord = "file"
	}
	lineWord := "lines"
	if totalLines == 1 {
		lineWord = "line"
	}
	replacementWord := "replacements"
	if totalReplacements == 1 {
		replacementWord = "replacement"
	}

	result.Summary = fmt.Sprintf("%s %d %s: %d %s in %d %s",
		action, totalFiles, fileWord, totalReplacements, replacementWord, totalLines, lineWord)

	return result, nil
}

func repforCollectDirsRecursive(dirs []string) []string {
	var allDirs []string
	seen := make(map[string]bool)

	for _, dir := range dirs {
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				cleanPath := filepath.Clean(path)
				if !seen[cleanPath] {
					seen[cleanPath] = true
					allDirs = append(allDirs, cleanPath)
				}
			}
			return nil
		})
	}
	return allDirs
}

func repforProcessDir(dir string, cfg repforConfig) (*repforDirResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	dirResult := &repforDirResult{
		Dir:   dir,
		Files: make([]repforFileModification, 0),
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}

		name := entry.Name()
		if cfg.ext != "" && !strings.HasSuffix(name, cfg.ext) {
			continue
		}

		fullPath := filepath.Join(dir, name)
		linesChanged, replacements, replErr := repforReplaceInFile(fullPath, cfg)
		if replErr != nil {
			continue
		}

		if linesChanged > 0 {
			dirResult.Files = append(dirResult.Files, repforFileModification{
				Path:         name,
				LinesChanged: linesChanged,
				Replacements: replacements,
			})
			dirResult.FilesModified++
			dirResult.LinesChanged += linesChanged
			dirResult.TotalReplacements += replacements
		}
	}

	return dirResult, nil
}

func repforProcessFiles(filePaths []string, cfg repforConfig) (*repforDirResult, error) {
	dirResult := &repforDirResult{
		Dir:   "(files)",
		Files: make([]repforFileModification, 0, len(filePaths)),
	}

	for _, filePath := range filePaths {
		info, err := os.Stat(filePath)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if cfg.ext != "" && !strings.HasSuffix(filePath, cfg.ext) {
			continue
		}

		linesChanged, replacements, replErr := repforReplaceInFile(filePath, cfg)
		if replErr != nil {
			continue
		}

		if linesChanged > 0 {
			dirResult.Files = append(dirResult.Files, repforFileModification{
				Path:         filePath,
				LinesChanged: linesChanged,
				Replacements: replacements,
			})
			dirResult.FilesModified++
			dirResult.LinesChanged += linesChanged
			dirResult.TotalReplacements += replacements
		}
	}

	return dirResult, nil
}

func repforReplaceInFile(path string, cfg repforConfig) (int, int, error) {
	if cfg.search == cfg.replace {
		return 0, 0, nil
	}

	if repforIsMultiline(cfg.search, cfg.replace) {
		return repforReplaceMultiline(path, cfg)
	}

	return repforReplaceSingleLine(path, cfg)
}

func repforReplaceSingleLine(path string, cfg repforConfig) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	// Detect line ending
	lineEnding := "\n"
	detectBuf := make([]byte, 8192)
	n, _ := f.Read(detectBuf)
	if n > 0 {
		for i := 0; i < n-1; i++ {
			if detectBuf[i] == '\r' && detectBuf[i+1] == '\n' {
				lineEnding = "\r\n"
				break
			}
			if detectBuf[i] == '\n' {
				break
			}
		}
	}
	f.Seek(0, 0)

	var lines []string
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return 0, 0, scanErr
	}

	linesChanged := 0
	totalReplacements := 0
	modifiedLines := make([]string, len(lines))
	copy(modifiedLines, lines)

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

		if repforIsExcluded(line, lineToCheck, cfg) {
			continue
		}

		newLine := repforReplaceLine(line, cfg.search, cfg.replace, cfg.caseInsensitive, cfg.wholeWord)
		if newLine != line {
			modifiedLines[i] = newLine
			linesChanged++
			totalReplacements += repforCountReplacements(line, cfg.search, cfg.caseInsensitive, cfg.wholeWord)
		}
	}

	if linesChanged > 0 && !cfg.dryRun {
		content := strings.Join(modifiedLines, lineEnding)
		if len(modifiedLines) > 0 {
			content += lineEnding
		}
		if writeErr := AtomicWrite(path, []byte(content)); writeErr != nil {
			return 0, 0, fmt.Errorf("failed to write file: %w", writeErr)
		}
	}

	return linesChanged, totalReplacements, nil
}

func repforReplaceMultiline(path string, cfg repforConfig) (int, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}

	content := string(data)

	// Normalize line endings in search/replace
	lineEnding := "\n"
	if strings.Contains(content, "\r\n") {
		lineEnding = "\r\n"
	}

	search := cfg.search
	replace := cfg.replace
	if lineEnding == "\r\n" {
		search = strings.ReplaceAll(strings.ReplaceAll(search, "\r\n", "\n"), "\n", "\r\n")
		replace = strings.ReplaceAll(strings.ReplaceAll(replace, "\r\n", "\n"), "\n", "\r\n")
	}

	modified, replacements, linesChanged := repforReplaceContent(
		content, search, replace, cfg.caseInsensitive, cfg.wholeWord, cfg.exclude,
	)

	if replacements == 0 {
		return 0, 0, nil
	}

	if !cfg.dryRun {
		if writeErr := AtomicWrite(path, []byte(modified)); writeErr != nil {
			return 0, 0, fmt.Errorf("failed to write file: %w", writeErr)
		}
	}

	return linesChanged, replacements, nil
}

// repforReplaceContent performs whole-content replacement with all four modes.
func repforReplaceContent(content, search, replace string, caseInsensitive, wholeWord bool, exclude []string) (string, int, int) {
	if search == "" {
		return content, 0, 0
	}

	searchTerm := search
	contentToSearch := content
	if caseInsensitive {
		searchTerm = strings.ToLower(search)
		contentToSearch = strings.ToLower(content)
	}

	var result strings.Builder
	result.Grow(len(content))
	replacements := 0
	affectedLines := make(map[int]bool)
	pos := 0

	for {
		idx := strings.Index(contentToSearch[pos:], searchTerm)
		if idx == -1 {
			result.WriteString(content[pos:])
			break
		}

		matchStart := pos + idx
		matchEnd := matchStart + len(search)

		if wholeWord {
			beforeOk := matchStart == 0 || !checkforIsWordChar(rune(content[matchStart-1]))
			afterOk := matchEnd >= len(content) || !checkforIsWordChar(rune(content[matchEnd]))
			if !beforeOk || !afterOk {
				result.WriteString(content[pos : matchStart+1])
				pos = matchStart + 1
				continue
			}
		}

		if len(exclude) > 0 {
			lineStart := matchStart
			for lineStart > 0 && content[lineStart-1] != '\n' {
				lineStart--
			}
			lineEnd := matchEnd
			for lineEnd < len(content) && content[lineEnd] != '\n' {
				lineEnd++
			}
			spanningText := content[lineStart:lineEnd]

			excluded := false
			for _, excl := range exclude {
				exclCheck := excl
				textCheck := spanningText
				if caseInsensitive {
					exclCheck = strings.ToLower(excl)
					textCheck = strings.ToLower(spanningText)
				}
				if strings.Contains(textCheck, exclCheck) {
					excluded = true
					break
				}
			}
			if excluded {
				result.WriteString(content[pos:matchEnd])
				pos = matchEnd
				continue
			}
		}

		startLine := strings.Count(content[:matchStart], "\n")
		matchNewlines := strings.Count(content[matchStart:matchEnd], "\n")
		for l := startLine; l <= startLine+matchNewlines; l++ {
			affectedLines[l] = true
		}

		result.WriteString(content[pos:matchStart])
		result.WriteString(replace)
		pos = matchEnd
		replacements++
	}

	return result.String(), replacements, len(affectedLines)
}

func repforReplaceLine(line, search, replace string, caseInsensitive, wholeWord bool) string {
	if search == "" {
		return line
	}

	if !caseInsensitive && !wholeWord {
		return strings.ReplaceAll(line, search, replace)
	}

	if caseInsensitive && !wholeWord {
		return repforCaseInsensitiveReplace(line, search, replace)
	}

	if wholeWord && !caseInsensitive {
		return repforWholeWordReplace(line, search, replace)
	}

	return repforCaseInsensitiveWholeWordReplace(line, search, replace)
}

func repforCaseInsensitiveReplace(line, search, replace string) string {
	searchLower := strings.ToLower(search)
	var result strings.Builder
	result.Grow(len(line))
	remaining := line

	for {
		lineLower := strings.ToLower(remaining)
		idx := strings.Index(lineLower, searchLower)
		if idx == -1 {
			result.WriteString(remaining)
			break
		}
		result.WriteString(remaining[:idx])
		result.WriteString(replace)
		remaining = remaining[idx+len(search):]
	}

	return result.String()
}

func repforWholeWordReplace(line, search, replace string) string {
	var result strings.Builder
	result.Grow(len(line))
	remaining := line

	for {
		idx := strings.Index(remaining, search)
		if idx == -1 {
			result.WriteString(remaining)
			break
		}

		beforeOk := idx == 0 || !checkforIsWordChar(rune(remaining[idx-1]))
		afterIdx := idx + len(search)
		afterOk := afterIdx >= len(remaining) || !checkforIsWordChar(rune(remaining[afterIdx]))

		if beforeOk && afterOk {
			result.WriteString(remaining[:idx])
			result.WriteString(replace)
			remaining = remaining[afterIdx:]
		} else {
			result.WriteString(remaining[:idx+1])
			remaining = remaining[idx+1:]
		}
	}

	return result.String()
}

func repforCaseInsensitiveWholeWordReplace(line, search, replace string) string {
	var result strings.Builder
	result.Grow(len(line))
	remaining := line
	searchLower := strings.ToLower(search)

	for {
		lineLower := strings.ToLower(remaining)
		idx := strings.Index(lineLower, searchLower)
		if idx == -1 {
			result.WriteString(remaining)
			break
		}

		beforeOk := idx == 0 || !checkforIsWordChar(rune(remaining[idx-1]))
		afterIdx := idx + len(search)
		afterOk := afterIdx >= len(remaining) || !checkforIsWordChar(rune(remaining[afterIdx]))

		if beforeOk && afterOk {
			result.WriteString(remaining[:idx])
			result.WriteString(replace)
			remaining = remaining[afterIdx:]
		} else {
			result.WriteString(remaining[:idx+1])
			remaining = remaining[idx+1:]
		}
	}

	return result.String()
}

func repforCountReplacements(line, search string, caseInsensitive, wholeWord bool) int {
	if search == "" {
		return 0
	}

	lineToCheck := line
	searchTerm := search
	if caseInsensitive {
		lineToCheck = strings.ToLower(line)
		searchTerm = strings.ToLower(search)
	}

	if !wholeWord {
		return strings.Count(lineToCheck, searchTerm)
	}

	count := 0
	offset := 0
	for {
		idx := strings.Index(lineToCheck[offset:], searchTerm)
		if idx == -1 {
			break
		}
		actualIdx := offset + idx
		beforeOk := actualIdx == 0 || !checkforIsWordChar(rune(lineToCheck[actualIdx-1]))
		afterIdx := actualIdx + len(searchTerm)
		afterOk := afterIdx >= len(lineToCheck) || !checkforIsWordChar(rune(lineToCheck[afterIdx]))

		if beforeOk && afterOk {
			count++
		}
		offset = actualIdx + 1
	}
	return count
}

func repforIsExcluded(originalLine, loweredLine string, cfg repforConfig) bool {
	for _, excl := range cfg.exclude {
		exclCheck := excl
		lineCheck := originalLine
		if cfg.caseInsensitive {
			exclCheck = strings.ToLower(excl)
			lineCheck = loweredLine
		}
		if strings.Contains(lineCheck, exclCheck) {
			return true
		}
	}
	return false
}

func repforIsMultiline(search, replace string) bool {
	return strings.Contains(search, "\n") || strings.Contains(replace, "\n")
}
