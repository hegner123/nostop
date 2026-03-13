package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// Split builtin tests
// =============================================================================

func TestSplit_BasicSplit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\nline4\nline5\nline6\n"), 0644)

	result := builtinSplit(context.Background(), map[string]any{
		"file":  path,
		"lines": []any{float64(2), float64(4)},
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out splitResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.TotalLines != 6 {
		t.Errorf("expected 6 total_lines, got %d", out.TotalLines)
	}
	if len(out.Parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(out.Parts))
	}

	// Part 1: lines 1-2
	if out.Parts[0].StartLine != 1 || out.Parts[0].EndLine != 2 {
		t.Errorf("part 1: expected lines 1-2, got %d-%d", out.Parts[0].StartLine, out.Parts[0].EndLine)
	}
	// Part 2: lines 3-4
	if out.Parts[1].StartLine != 3 || out.Parts[1].EndLine != 4 {
		t.Errorf("part 2: expected lines 3-4, got %d-%d", out.Parts[1].StartLine, out.Parts[1].EndLine)
	}
	// Part 3: lines 5-6
	if out.Parts[2].StartLine != 5 || out.Parts[2].EndLine != 6 {
		t.Errorf("part 3: expected lines 5-6, got %d-%d", out.Parts[2].StartLine, out.Parts[2].EndLine)
	}

	// Verify file content
	data1, _ := os.ReadFile(out.Parts[0].Path)
	if strings.TrimSpace(string(data1)) != "line1\nline2" {
		t.Errorf("part 1 content: %q", string(data1))
	}
}

func TestSplit_OutputNaming(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	os.WriteFile(path, []byte("a\nb\nc\nd\n"), 0644)

	result := builtinSplit(context.Background(), map[string]any{
		"file":  path,
		"lines": []any{float64(2)},
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out splitResult
	json.Unmarshal([]byte(result.Output), &out)

	// Should produce code_001.go and code_002.go
	if !strings.HasSuffix(out.Parts[0].Path, "code_001.go") {
		t.Errorf("expected code_001.go, got %s", filepath.Base(out.Parts[0].Path))
	}
	if !strings.HasSuffix(out.Parts[1].Path, "code_002.go") {
		t.Errorf("expected code_002.go, got %s", filepath.Base(out.Parts[1].Path))
	}
}

func TestSplit_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	os.WriteFile(path, []byte{}, 0644)

	result := builtinSplit(context.Background(), map[string]any{
		"file":  path,
		"lines": []any{float64(5)},
	}, "")

	if !result.IsError {
		t.Fatal("expected error for empty file")
	}
}

func TestSplit_InvalidSplitPoints(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("a\nb\nc\n"), 0644)

	// Split point beyond file length
	result := builtinSplit(context.Background(), map[string]any{
		"file":  path,
		"lines": []any{float64(100)},
	}, "")

	if !result.IsError {
		t.Fatal("expected error: split point beyond file length should yield no valid points")
	}
}

func TestSplit_DuplicatePoints(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("a\nb\nc\nd\n"), 0644)

	result := builtinSplit(context.Background(), map[string]any{
		"file":  path,
		"lines": []any{float64(2), float64(2), float64(2)},
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out splitResult
	json.Unmarshal([]byte(result.Output), &out)

	// Duplicates should be deduped — only 2 parts
	if len(out.Parts) != 2 {
		t.Errorf("expected 2 parts (deduped), got %d", len(out.Parts))
	}
}

func TestSplit_MissingParams(t *testing.T) {
	result := builtinSplit(context.Background(), map[string]any{}, "")
	if !result.IsError {
		t.Fatal("expected error for missing params")
	}

	result = builtinSplit(context.Background(), map[string]any{
		"file": "/tmp/test.txt",
	}, "")
	if !result.IsError {
		t.Fatal("expected error for missing lines")
	}
}

func TestSplit_RelativePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("a\nb\nc\nd\n"), 0644)

	result := builtinSplit(context.Background(), map[string]any{
		"file":  "test.txt",
		"lines": []any{float64(2)},
	}, dir)

	if result.IsError {
		t.Fatalf("relative path should work: %s", result.Error)
	}
}

// =============================================================================
// Splice builtin tests
// =============================================================================

func TestSplice_Append(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.txt")
	target := filepath.Join(dir, "target.txt")
	os.WriteFile(source, []byte("appended\n"), 0644)
	os.WriteFile(target, []byte("original\n"), 0644)

	result := builtinSplice(context.Background(), map[string]any{
		"source": source,
		"target": target,
		"mode":   "append",
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, _ := os.ReadFile(target)
	if string(data) != "original\nappended\n" {
		t.Errorf("expected original+appended, got %q", string(data))
	}
}

func TestSplice_Prepend(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.txt")
	target := filepath.Join(dir, "target.txt")
	os.WriteFile(source, []byte("first\n"), 0644)
	os.WriteFile(target, []byte("second\n"), 0644)

	result := builtinSplice(context.Background(), map[string]any{
		"source": source,
		"target": target,
		"mode":   "prepend",
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, _ := os.ReadFile(target)
	if string(data) != "first\nsecond\n" {
		t.Errorf("expected first+second, got %q", string(data))
	}
}

func TestSplice_Replace(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.txt")
	target := filepath.Join(dir, "target.txt")
	os.WriteFile(source, []byte("replacement\n"), 0644)
	os.WriteFile(target, []byte("original\n"), 0644)

	result := builtinSplice(context.Background(), map[string]any{
		"source": source,
		"target": target,
		"mode":   "replace",
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, _ := os.ReadFile(target)
	if string(data) != "replacement\n" {
		t.Errorf("expected replacement content, got %q", string(data))
	}
}

func TestSplice_Insert(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.txt")
	target := filepath.Join(dir, "target.txt")
	os.WriteFile(source, []byte("inserted\n"), 0644)
	os.WriteFile(target, []byte("line1\nline2\nline3\n"), 0644)

	result := builtinSplice(context.Background(), map[string]any{
		"source": source,
		"target": target,
		"mode":   "insert",
		"line":   float64(2),
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, _ := os.ReadFile(target)
	expected := "line1\nline2\ninserted\nline3\n"
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}
}

func TestSplice_InsertMissingLine(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.txt")
	target := filepath.Join(dir, "target.txt")
	os.WriteFile(source, []byte("x\n"), 0644)
	os.WriteFile(target, []byte("y\n"), 0644)

	result := builtinSplice(context.Background(), map[string]any{
		"source": source,
		"target": target,
		"mode":   "insert",
		// No line param
	}, "")

	if !result.IsError {
		t.Fatal("expected error: insert mode requires line >= 1")
	}
}

func TestSplice_AppendNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.txt")
	target := filepath.Join(dir, "target.txt")
	os.WriteFile(source, []byte("appended"), 0644)
	os.WriteFile(target, []byte("original"), 0644) // no trailing newline

	result := builtinSplice(context.Background(), map[string]any{
		"source": source,
		"target": target,
		"mode":   "append",
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, _ := os.ReadFile(target)
	// Should insert a newline between them
	if string(data) != "original\nappended" {
		t.Errorf("expected newline-joined, got %q", string(data))
	}
}

func TestSplice_InvalidMode(t *testing.T) {
	result := builtinSplice(context.Background(), map[string]any{
		"source": "/tmp/a",
		"target": "/tmp/b",
		"mode":   "destroy",
	}, "")
	if !result.IsError {
		t.Fatal("expected error for invalid mode")
	}
}

func TestSplice_MissingParams(t *testing.T) {
	result := builtinSplice(context.Background(), map[string]any{}, "")
	if !result.IsError {
		t.Fatal("expected error for missing params")
	}
}

func TestSplice_ResultFormat(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "s.txt")
	target := filepath.Join(dir, "t.txt")
	os.WriteFile(source, []byte("a\nb\nc\n"), 0644)
	os.WriteFile(target, []byte("x\n"), 0644)

	result := builtinSplice(context.Background(), map[string]any{
		"source": source,
		"target": target,
		"mode":   "append",
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out spliceResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Mode != "append" {
		t.Errorf("expected mode append, got %q", out.Mode)
	}
	if out.LinesAdded != 3 {
		t.Errorf("expected 3 lines_added, got %d", out.LinesAdded)
	}
	if out.Summary == "" {
		t.Error("expected non-empty summary")
	}
}

// =============================================================================
// Stump builtin tests
// =============================================================================

func TestStump_BasicDirectory(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b"), 0644)

	result := builtinStump(context.Background(), map[string]any{
		"dir": dir,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out stumpResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Root != dir {
		t.Errorf("expected root %q, got %q", dir, out.Root)
	}
	if out.Stats.Dirs < 1 {
		t.Error("expected at least 1 directory")
	}
	if out.Stats.Files < 2 {
		t.Errorf("expected at least 2 files, got %d", out.Stats.Files)
	}

	// Verify tree entries exist
	paths := make(map[string]bool)
	for _, e := range out.Tree {
		paths[e.Path] = true
	}
	if !paths["a.txt"] {
		t.Error("expected a.txt in tree")
	}
	if !paths["sub"] {
		t.Error("expected sub directory in tree")
	}
}

func TestStump_DepthLimit(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b", "c"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "b", "c", "deep.txt"), []byte("deep"), 0644)

	result := builtinStump(context.Background(), map[string]any{
		"dir":   dir,
		"depth": float64(1),
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out stumpResult
	json.Unmarshal([]byte(result.Output), &out)

	// At depth 1, should see "a" dir and its immediate children, but not c/deep.txt
	for _, e := range out.Tree {
		if strings.Count(e.Path, string(filepath.Separator)) > 1 {
			t.Errorf("depth=1 should not include deeply nested paths, got %q", e.Path)
		}
	}
}

func TestStump_ExtensionFilter(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("go"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("txt"), 0644)
	os.WriteFile(filepath.Join(dir, "c.go"), []byte("go"), 0644)

	result := builtinStump(context.Background(), map[string]any{
		"dir":         dir,
		"include_ext": []any{".go"},
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out stumpResult
	json.Unmarshal([]byte(result.Output), &out)

	for _, e := range out.Tree {
		if e.Type == "f" && !strings.HasSuffix(e.Path, ".go") {
			t.Errorf("expected only .go files, got %q", e.Path)
		}
	}
	if out.Stats.Files != 2 {
		t.Errorf("expected 2 .go files, got %d", out.Stats.Files)
	}
}

func TestStump_ExcludePatterns(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0755)
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "index.js"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("x"), 0644)

	result := builtinStump(context.Background(), map[string]any{
		"dir":              dir,
		"exclude_patterns": []any{"node_modules"},
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out stumpResult
	json.Unmarshal([]byte(result.Output), &out)

	for _, e := range out.Tree {
		if strings.Contains(e.Path, "node_modules") {
			t.Errorf("node_modules should be excluded, got %q", e.Path)
		}
	}
}

func TestStump_ShowSize(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello world"), 0644)

	result := builtinStump(context.Background(), map[string]any{
		"dir":       dir,
		"show_size": true,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out stumpResult
	json.Unmarshal([]byte(result.Output), &out)

	for _, e := range out.Tree {
		if e.Type == "f" && e.Size == 0 {
			t.Errorf("expected non-zero size with show_size=true for %q", e.Path)
		}
	}
}

func TestStump_HiddenFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "visible"), []byte("x"), 0644)

	// Without show_hidden: should skip .hidden
	result := builtinStump(context.Background(), map[string]any{
		"dir": dir,
	}, "")

	var out stumpResult
	json.Unmarshal([]byte(result.Output), &out)
	for _, e := range out.Tree {
		if strings.HasPrefix(e.Path, ".") {
			t.Errorf("hidden files should be excluded by default, got %q", e.Path)
		}
	}

	// With show_hidden: should include .hidden
	result2 := builtinStump(context.Background(), map[string]any{
		"dir":         dir,
		"show_hidden": true,
	}, "")

	var out2 stumpResult
	json.Unmarshal([]byte(result2.Output), &out2)

	found := false
	for _, e := range out2.Tree {
		if e.Path == ".hidden" {
			found = true
		}
	}
	if !found {
		t.Error("expected .hidden with show_hidden=true")
	}
}

func TestStump_NonexistentDir(t *testing.T) {
	result := builtinStump(context.Background(), map[string]any{
		"dir": "/nonexistent/dir",
	}, "")
	if !result.IsError {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestStump_FileNotDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("x"), 0644)

	result := builtinStump(context.Background(), map[string]any{
		"dir": path,
	}, "")
	if !result.IsError {
		t.Fatal("expected error for file-as-dir")
	}
}

func TestStump_MissingDir(t *testing.T) {
	result := builtinStump(context.Background(), map[string]any{}, "")
	if !result.IsError {
		t.Fatal("expected error for missing dir param")
	}
}

// =============================================================================
// Registry integration
// =============================================================================

func TestPhase2_BuiltinsInRegistry(t *testing.T) {
	reg := DefaultRegistry()

	for _, name := range []string{"split", "splice", "stump"} {
		def, ok := reg.Get(name)
		if !ok {
			t.Errorf("%q not found in default registry", name)
			continue
		}
		if !def.IsBuiltinTool() {
			t.Errorf("%q should be a builtin", name)
		}
	}
}

func TestPhase2_TotalToolCount(t *testing.T) {
	reg := DefaultRegistry()
	// 9 subprocess + 9 builtins (read, write, bash, tabcount, notab, delete, split, splice, stump)
	if reg.Len() != 18 {
		t.Errorf("expected 18 tools, got %d", reg.Len())
		t.Logf("tools: %v", reg.Names())
	}
}
