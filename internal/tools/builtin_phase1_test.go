package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// Tabcount builtin tests
// =============================================================================

func TestTabcount_BasicFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tfmt.Println()\n\t\tif true {\n\t\t\tx := 1\n\t\t}\n}\n"), 0644)

	result := builtinTabcount(context.Background(), map[string]any{
		"file": path,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out tabcountResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.File != path {
		t.Errorf("expected file %q, got %q", path, out.File)
	}
	if out.StartLine != 1 {
		t.Errorf("expected start_line 1, got %d", out.StartLine)
	}

	// Line 1: "package main" -> 0 tabs
	// Line 4: "\tfmt.Println()" -> 1 tab
	// Line 5: "\t\tif true {" -> 2 tabs
	// Line 6: "\t\t\tx := 1" -> 3 tabs
	found := map[int]int{} // line -> indentation
	for _, li := range out.Lines {
		found[li.Line] = li.Indentation
	}

	if found[1] != 0 {
		t.Errorf("line 1: expected 0 tabs, got %d", found[1])
	}
	if found[4] != 1 {
		t.Errorf("line 4: expected 1 tab, got %d", found[4])
	}
	if found[5] != 2 {
		t.Errorf("line 5: expected 2 tabs, got %d", found[5])
	}
	if found[6] != 3 {
		t.Errorf("line 6: expected 3 tabs, got %d", found[6])
	}
}

func TestTabcount_LineRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("a\n\tb\n\t\tc\n\t\t\td\ne\n"), 0644)

	result := builtinTabcount(context.Background(), map[string]any{
		"file":       path,
		"start_line": float64(2),
		"end_line":   float64(4),
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out tabcountResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.TotalLines != 3 {
		t.Errorf("expected 3 lines in range, got %d", out.TotalLines)
	}
	if out.StartLine != 2 {
		t.Errorf("expected start_line 2, got %d", out.StartLine)
	}
	if out.EndLine != 4 {
		t.Errorf("expected end_line 4, got %d", out.EndLine)
	}

	// Should only have lines 2, 3, 4
	for _, li := range out.Lines {
		if li.Line < 2 || li.Line > 4 {
			t.Errorf("unexpected line %d in range [2,4]", li.Line)
		}
	}
}

func TestTabcount_NonexistentFile(t *testing.T) {
	result := builtinTabcount(context.Background(), map[string]any{
		"file": "/nonexistent/file.txt",
	}, "")
	if !result.IsError {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestTabcount_MissingFile(t *testing.T) {
	result := builtinTabcount(context.Background(), map[string]any{}, "")
	if !result.IsError {
		t.Fatal("expected error for missing file param")
	}
}

func TestTabcount_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	os.WriteFile(path, []byte{}, 0644)

	result := builtinTabcount(context.Background(), map[string]any{
		"file": path,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out tabcountResult
	json.Unmarshal([]byte(result.Output), &out)
	if out.TotalLines != 0 {
		t.Errorf("expected 0 total_lines for empty file, got %d", out.TotalLines)
	}
}

func TestTabcount_SpacesNotTabs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spaces.py")
	os.WriteFile(path, []byte("def foo():\n    return 1\n        nested\n"), 0644)

	result := builtinTabcount(context.Background(), map[string]any{
		"file": path,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out tabcountResult
	json.Unmarshal([]byte(result.Output), &out)

	// All lines should have 0 indentation (spaces, not tabs)
	for _, li := range out.Lines {
		if li.Indentation != 0 {
			t.Errorf("line %d: expected 0 tabs (spaces only), got %d", li.Line, li.Indentation)
		}
	}
}

func TestTabcount_RelativePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("\tindented\n"), 0644)

	result := builtinTabcount(context.Background(), map[string]any{
		"file": "test.txt",
	}, dir)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}
}

// Dual execution: compare builtin output to CLI binary output.
func TestTabcount_MatchesCLI(t *testing.T) {
	if _, err := exec.LookPath("tabcount"); err != nil {
		t.Skip("tabcount binary not found on PATH")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tfmt.Println()\n\t\tx := 1\n}\n"), 0644)

	// Run builtin
	builtinOut := builtinTabcount(context.Background(), map[string]any{
		"file": path,
	}, "")
	if builtinOut.IsError {
		t.Fatalf("builtin error: %s", builtinOut.Error)
	}

	// Run CLI
	cmd := exec.Command("tabcount", "--cli", "--file", path)
	cliBytes, err := cmd.Output()
	if err != nil {
		t.Fatalf("CLI error: %s", err)
	}

	// Compare JSON structures
	var builtinJSON, cliJSON tabcountResult
	json.Unmarshal([]byte(builtinOut.Output), &builtinJSON)
	json.Unmarshal(cliBytes, &cliJSON)

	if builtinJSON.TotalLines != cliJSON.TotalLines {
		t.Errorf("total_lines mismatch: builtin=%d cli=%d", builtinJSON.TotalLines, cliJSON.TotalLines)
	}
	if len(builtinJSON.Lines) != len(cliJSON.Lines) {
		t.Fatalf("lines count mismatch: builtin=%d cli=%d", len(builtinJSON.Lines), len(cliJSON.Lines))
	}
	for i := range builtinJSON.Lines {
		if builtinJSON.Lines[i] != cliJSON.Lines[i] {
			t.Errorf("line %d mismatch: builtin=%+v cli=%+v", i, builtinJSON.Lines[i], cliJSON.Lines[i])
		}
	}
}

// =============================================================================
// Notab builtin tests
// =============================================================================

func TestNotab_TabsToSpaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte("\tfirst\n\t\tsecond\nthird\n"), 0644)

	result := builtinNotab(context.Background(), map[string]any{
		"file":   path,
		"spaces": float64(4),
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out notabResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Replacements != 3 { // 1 tab + 2 tabs = 3 replacements
		t.Errorf("expected 3 replacements, got %d", out.Replacements)
	}
	if out.LinesAffected != 2 {
		t.Errorf("expected 2 lines affected, got %d", out.LinesAffected)
	}
	if out.Direction != "tabs_to_spaces" {
		t.Errorf("expected direction tabs_to_spaces, got %q", out.Direction)
	}

	// Verify file content
	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "\t") {
		t.Error("file should not contain tabs after normalization")
	}
	if !strings.HasPrefix(content, "    first") {
		t.Errorf("expected 4-space indent, got: %q", content[:20])
	}
}

func TestNotab_SpacesToTabs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	os.WriteFile(path, []byte("target:\n    echo hello\n        echo nested\n"), 0644)

	result := builtinNotab(context.Background(), map[string]any{
		"file":   path,
		"spaces": float64(4),
		"tabs":   true,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out notabResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Direction != "spaces_to_tabs" {
		t.Errorf("expected direction spaces_to_tabs, got %q", out.Direction)
	}
	if out.LinesAffected != 2 {
		t.Errorf("expected 2 lines affected, got %d", out.LinesAffected)
	}

	data, _ := os.ReadFile(path)
	lines := strings.Split(string(data), "\n")
	if lines[1] != "\techo hello" {
		t.Errorf("expected tab indent on line 2, got %q", lines[1])
	}
	if lines[2] != "\t\techo nested" {
		t.Errorf("expected 2 tab indent on line 3, got %q", lines[2])
	}
}

func TestNotab_NoChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.txt")
	os.WriteFile(path, []byte("no tabs here\njust spaces\n"), 0644)

	result := builtinNotab(context.Background(), map[string]any{
		"file": path,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out notabResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Replacements != 0 {
		t.Errorf("expected 0 replacements, got %d", out.Replacements)
	}
}

func TestNotab_MissingFile(t *testing.T) {
	result := builtinNotab(context.Background(), map[string]any{}, "")
	if !result.IsError {
		t.Fatal("expected error for missing file param")
	}
}

func TestNotab_NonexistentFile(t *testing.T) {
	result := builtinNotab(context.Background(), map[string]any{
		"file": "/nonexistent/file.txt",
	}, "")
	if !result.IsError {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestNotab_CustomSpaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("\tindented\n"), 0644)

	result := builtinNotab(context.Background(), map[string]any{
		"file":   path,
		"spaces": float64(2),
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "  indented") {
		t.Errorf("expected 2-space indent, got: %q", string(data))
	}
}

func TestNotab_PreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("\ttab\n"), 0755)

	builtinNotab(context.Background(), map[string]any{
		"file": path,
	}, "")

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0755 {
		t.Errorf("expected 0755 permissions preserved, got %o", info.Mode().Perm())
	}
}

func TestNotab_MatchesCLI(t *testing.T) {
	if _, err := exec.LookPath("notab"); err != nil {
		t.Skip("notab binary not found on PATH")
	}

	// Create two identical files — one for builtin, one for CLI
	dir := t.TempDir()
	builtinPath := filepath.Join(dir, "builtin.go")
	cliPath := filepath.Join(dir, "cli.go")
	content := []byte("\tpackage main\n\n\tfunc main() {\n\t\tfmt.Println()\n\t}\n")
	os.WriteFile(builtinPath, content, 0644)
	os.WriteFile(cliPath, content, 0644)

	// Run builtin
	builtinOut := builtinNotab(context.Background(), map[string]any{
		"file":   builtinPath,
		"spaces": float64(4),
	}, "")
	if builtinOut.IsError {
		t.Fatalf("builtin error: %s", builtinOut.Error)
	}

	// Run CLI
	cmd := exec.Command("notab", "--cli", "--file", cliPath, "--spaces", "4")
	cmd.Run() // ignore exit code (2 = no changes, which won't happen here)

	// Compare file contents
	builtinData, _ := os.ReadFile(builtinPath)
	cliData, _ := os.ReadFile(cliPath)
	if string(builtinData) != string(cliData) {
		t.Error("builtin and CLI produced different file contents")
		t.Logf("builtin: %q", string(builtinData))
		t.Logf("cli:     %q", string(cliData))
	}
}

// =============================================================================
// Delete builtin tests
// =============================================================================

func TestDelete_File(t *testing.T) {
	// Use HOME-based temp dir to avoid /var/folders being blocked
	home := os.Getenv("HOME")
	dir, err := os.MkdirTemp(home, ".nostop-test-delete-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %s", err)
	}
	defer os.RemoveAll(dir)

	trashDir := filepath.Join(dir, "trash")
	filePath := filepath.Join(dir, "victim.txt")
	os.WriteFile(filePath, []byte("delete me"), 0644)

	result, err := trashPathTo(filePath, trashDir)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if result.Type != "file" {
		t.Errorf("expected type 'file', got %q", result.Type)
	}
	if result.OriginalPath != filePath {
		t.Errorf("expected original_path %q, got %q", filePath, result.OriginalPath)
	}

	// Original should be gone
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("original file should not exist after trashing")
	}

	// Should exist in trash
	if _, err := os.Stat(result.TrashPath); err != nil {
		t.Errorf("file should exist in trash at %q: %s", result.TrashPath, err)
	}
}

func TestDelete_Directory(t *testing.T) {
	home := os.Getenv("HOME")
	dir, _ := os.MkdirTemp(home, ".nostop-test-delete-*")
	defer os.RemoveAll(dir)

	trashDir := filepath.Join(dir, "trash")
	victim := filepath.Join(dir, "victim_dir")
	os.MkdirAll(filepath.Join(victim, "sub"), 0755)
	os.WriteFile(filepath.Join(victim, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(victim, "sub", "b.txt"), []byte("b"), 0644)

	result, err := trashPathTo(victim, trashDir)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if result.Type != "directory" {
		t.Errorf("expected type 'directory', got %q", result.Type)
	}
	if result.Items < 3 { // dir itself + sub + 2 files = at least 3
		t.Errorf("expected at least 3 items, got %d", result.Items)
	}
}

func TestDelete_NameCollision(t *testing.T) {
	home := os.Getenv("HOME")
	dir, _ := os.MkdirTemp(home, ".nostop-test-delete-*")
	defer os.RemoveAll(dir)

	trashDir := filepath.Join(dir, "trash")
	os.MkdirAll(trashDir, 0755)

	// Create file and a collision in trash
	filePath := filepath.Join(dir, "test.txt")
	os.WriteFile(filePath, []byte("original"), 0644)
	os.WriteFile(filepath.Join(trashDir, "test.txt"), []byte("existing"), 0644)

	result, err := trashPathTo(filePath, trashDir)
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	// Trash path should NOT be the plain name (collision handling)
	if filepath.Base(result.TrashPath) == "test.txt" {
		t.Error("expected collision-handled name, got plain 'test.txt'")
	}
}

func TestDelete_NonexistentPath(t *testing.T) {
	dir := t.TempDir()
	trashDir := filepath.Join(dir, "trash")

	_, err := trashPathTo("/nonexistent/path", trashDir)
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestDelete_BlockedSystemPath(t *testing.T) {
	dir := t.TempDir()
	trashDir := filepath.Join(dir, "trash")

	for _, blocked := range []string{"/System/Library", "/usr/bin/ls", "/bin/bash"} {
		_, err := trashPathTo(blocked, trashDir)
		if err == nil {
			t.Errorf("expected error for blocked path %q", blocked)
		}
		if !strings.Contains(err.Error(), "refusing") {
			t.Errorf("expected 'refusing' error for %q, got: %s", blocked, err)
		}
	}
}

func TestDelete_BlockTrashItself(t *testing.T) {
	dir := t.TempDir()
	trashDir := filepath.Join(dir, "trash")
	os.MkdirAll(trashDir, 0755)

	_, err := trashPathTo(trashDir, trashDir)
	if err == nil {
		t.Fatal("expected error for trashing Trash itself")
	}
}

func TestDelete_MissingPath(t *testing.T) {
	result := builtinDelete(context.Background(), map[string]any{}, "")
	if !result.IsError {
		t.Fatal("expected error for missing path param")
	}
}

func TestDelete_ViaExecutor(t *testing.T) {
	home := os.Getenv("HOME")
	dir, _ := os.MkdirTemp(home, ".nostop-test-delete-*")
	defer os.RemoveAll(dir)

	filePath := filepath.Join(dir, "exec_test.txt")
	os.WriteFile(filePath, []byte("executor delete"), 0644)

	reg := NewRegistry()
	reg.Register(DeleteDef)
	exec := NewExecutor(reg, "")

	result := exec.Execute(context.Background(), "delete", map[string]any{
		"path": filePath,
	})

	if result.IsError {
		t.Fatalf("executor delete failed: %s", result.Error)
	}

	// File should be gone
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

// =============================================================================
// Registry integration
// =============================================================================

func TestPhase1_BuiltinsInRegistry(t *testing.T) {
	reg := DefaultRegistry()

	for _, name := range []string{"tabcount", "notab", "delete"} {
		def, ok := reg.Get(name)
		if !ok {
			t.Errorf("%q not found in default registry", name)
			continue
		}
		if !def.IsBuiltinTool() {
			t.Errorf("%q should be a builtin, not subprocess", name)
		}
		if def.Binary != "" {
			t.Errorf("%q should have empty Binary field, got %q", name, def.Binary)
		}
	}
}

func TestPhase1_TotalToolCount(t *testing.T) {
	reg := DefaultRegistry()
	// 12 subprocess + 6 builtins (read, write, bash, tabcount, notab, delete)
	if reg.Len() != 18 {
		t.Errorf("expected 18 tools, got %d", reg.Len())
		t.Logf("tools: %v", reg.Names())
	}
}
