package nostop

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Prompt loading follows the same hierarchy as Claude Code:
//
//   1. ~/.claude/CLAUDE.md (user global instructions)
//   2. ~/.claude/rules/*.md (user global rules)
//   3. <project>/CLAUDE.md (project instructions)
//   4. <project>/.claude/rules/*.md (project rules - not yet used by this loader)
//
// All files are optional. Content is concatenated in order.

// LoadUserPrompt reads Claude Code prompt files from the standard locations
// and returns the concatenated content for use as a system prompt.
func LoadUserPrompt() string {
	var parts []string

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// 1. ~/.claude/CLAUDE.md
	if content := readFileIfExists(filepath.Join(home, ".claude", "CLAUDE.md")); content != "" {
		parts = append(parts, content)
	}

	// 2. ~/.claude/rules/*.md
	parts = append(parts, loadRulesDir(filepath.Join(home, ".claude", "rules"))...)

	// 3. <cwd>/CLAUDE.md (project-level)
	if content := readFileIfExists("CLAUDE.md"); content != "" {
		parts = append(parts, content)
	}

	return strings.Join(parts, "\n\n")
}

// LoadUserPromptFrom reads prompt files relative to a specific project directory.
func LoadUserPromptFrom(projectDir string) string {
	var parts []string

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// 1. ~/.claude/CLAUDE.md
	if content := readFileIfExists(filepath.Join(home, ".claude", "CLAUDE.md")); content != "" {
		parts = append(parts, content)
	}

	// 2. ~/.claude/rules/*.md
	parts = append(parts, loadRulesDir(filepath.Join(home, ".claude", "rules"))...)

	// 3. <projectDir>/CLAUDE.md
	if content := readFileIfExists(filepath.Join(projectDir, "CLAUDE.md")); content != "" {
		parts = append(parts, content)
	}

	return strings.Join(parts, "\n\n")
}

// loadRulesDir reads all .md files from a directory, sorted by name.
func loadRulesDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	// Sort for deterministic ordering
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var parts []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if content := readFileIfExists(filepath.Join(dir, entry.Name())); content != "" {
			parts = append(parts, content)
		}
	}
	return parts
}

// readFileIfExists reads a file and returns its trimmed content.
// Returns "" if the file doesn't exist or can't be read.
func readFileIfExists(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
