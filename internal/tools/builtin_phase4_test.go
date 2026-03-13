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
// Repfor builtin tests
// =============================================================================

func TestRepfor_BasicReplace(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello world\nhello again\n"), 0644)

	result := builtinRepfor(context.Background(), map[string]any{
		"search":  "hello",
		"replace": "goodbye",
		"dir":     []any{dir},
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out repforResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Directories[0].TotalReplacements != 2 {
		t.Errorf("expected 2 replacements, got %d", out.Directories[0].TotalReplacements)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	if !strings.Contains(string(data), "goodbye world") {
		t.Errorf("expected 'goodbye world', got %q", string(data))
	}
}

func TestRepfor_DryRun(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("original\n"), 0644)

	result := builtinRepfor(context.Background(), map[string]any{
		"search":  "original",
		"replace": "modified",
		"dir":     []any{dir},
		"dry_run": true,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out repforResult
	json.Unmarshal([]byte(result.Output), &out)

	if !out.DryRun {
		t.Error("expected dry_run=true in output")
	}
	if out.Directories[0].TotalReplacements != 1 {
		t.Errorf("expected 1 replacement counted, got %d", out.Directories[0].TotalReplacements)
	}

	// File should be unchanged
	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	if string(data) != "original\n" {
		t.Errorf("dry_run should not modify file, got %q", string(data))
	}
}

func TestRepfor_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("Hello HELLO hello\n"), 0644)

	result := builtinRepfor(context.Background(), map[string]any{
		"search":           "hello",
		"replace":          "hi",
		"dir":              []any{dir},
		"case_insensitive": true,
	}, "")

	var out repforResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Directories[0].TotalReplacements != 3 {
		t.Errorf("expected 3 case-insensitive replacements, got %d", out.Directories[0].TotalReplacements)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	if string(data) != "hi hi hi\n" {
		t.Errorf("expected all replaced, got %q", string(data))
	}
}

func TestRepfor_WholeWord(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("foo foobar barfoo\n"), 0644)

	result := builtinRepfor(context.Background(), map[string]any{
		"search":     "foo",
		"replace":    "baz",
		"dir":        []any{dir},
		"whole_word": true,
	}, "")

	var out repforResult
	json.Unmarshal([]byte(result.Output), &out)

	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	if string(data) != "baz foobar barfoo\n" {
		t.Errorf("expected only whole-word 'foo' replaced, got %q", string(data))
	}
}

func TestRepfor_Exclude(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("func main() {}\nfunc test_helper() {}\nfunc run() {}\n"), 0644)

	result := builtinRepfor(context.Background(), map[string]any{
		"search":  "func",
		"replace": "fn",
		"dir":     []any{dir},
		"exclude": []any{"test_"},
	}, "")

	var out repforResult
	json.Unmarshal([]byte(result.Output), &out)

	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	content := string(data)
	if !strings.Contains(content, "fn main()") {
		t.Error("expected 'func main' replaced with 'fn main'")
	}
	if !strings.Contains(content, "func test_helper") {
		t.Error("expected 'func test_helper' excluded from replacement")
	}
	if !strings.Contains(content, "fn run()") {
		t.Error("expected 'func run' replaced with 'fn run'")
	}
}

func TestRepfor_ExtFilter(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("target\n"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("target\n"), 0644)

	builtinRepfor(context.Background(), map[string]any{
		"search":  "target",
		"replace": "replaced",
		"dir":     []any{dir},
		"ext":     ".go",
	}, "")

	goData, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	txtData, _ := os.ReadFile(filepath.Join(dir, "b.txt"))

	if !strings.Contains(string(goData), "replaced") {
		t.Error(".go file should be modified")
	}
	if strings.Contains(string(txtData), "replaced") {
		t.Error(".txt file should NOT be modified")
	}
}

func TestRepfor_DirectFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "specific.txt")
	os.WriteFile(path, []byte("old value\n"), 0644)

	result := builtinRepfor(context.Background(), map[string]any{
		"search":  "old",
		"replace": "new",
		"file":    []any{path},
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "new value\n" {
		t.Errorf("expected 'new value', got %q", string(data))
	}
}

func TestRepfor_NoMatchNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("no match here\n"), 0644)

	result := builtinRepfor(context.Background(), map[string]any{
		"search":  "nonexistent",
		"replace": "something",
		"dir":     []any{dir},
	}, "")

	var out repforResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Directories[0].TotalReplacements != 0 {
		t.Errorf("expected 0 replacements, got %d", out.Directories[0].TotalReplacements)
	}
}

func TestRepfor_SearchEqualsReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("same same\n"), 0644)

	result := builtinRepfor(context.Background(), map[string]any{
		"search":  "same",
		"replace": "same",
		"dir":     []any{dir},
	}, "")

	var out repforResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Directories[0].TotalReplacements != 0 {
		t.Errorf("search == replace should be no-op, got %d replacements", out.Directories[0].TotalReplacements)
	}
}

func TestRepfor_DeleteMode(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("remove THIS please\n"), 0644)

	builtinRepfor(context.Background(), map[string]any{
		"search":  "THIS ",
		"replace": "",
		"dir":     []any{dir},
	}, "")

	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	if string(data) != "remove please\n" {
		t.Errorf("expected 'remove please', got %q", string(data))
	}
}

func TestRepfor_MultilineReplace(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("first\nsecond\nthird\n"), 0644)

	builtinRepfor(context.Background(), map[string]any{
		"search":  "first\nsecond",
		"replace": "combined",
		"dir":     []any{dir},
	}, "")

	data, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	if string(data) != "combined\nthird\n" {
		t.Errorf("expected 'combined\\nthird\\n', got %q", string(data))
	}
}

func TestRepfor_MissingParams(t *testing.T) {
	result := builtinRepfor(context.Background(), map[string]any{
		"search": "x",
	}, "")
	if !result.IsError {
		t.Fatal("expected error for missing replace")
	}
}

func TestRepfor_Summary(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x\nx\n"), 0644)

	result := builtinRepfor(context.Background(), map[string]any{
		"search":  "x",
		"replace": "y",
		"dir":     []any{dir},
	}, "")

	var out repforResult
	json.Unmarshal([]byte(result.Output), &out)

	if !strings.Contains(out.Summary, "Modified") {
		t.Errorf("expected 'Modified' in summary, got %q", out.Summary)
	}
	if !strings.Contains(out.Summary, "2 files") {
		t.Errorf("expected '2 files' in summary, got %q", out.Summary)
	}
}

func TestRepfor_MatchesCLI(t *testing.T) {
	if _, err := exec.LookPath("repfor"); err != nil {
		t.Skip("repfor binary not found on PATH")
	}

	// Create two identical directories
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	content := []byte("func hello() {}\nfunc world() {}\n")
	os.WriteFile(filepath.Join(dir1, "test.go"), content, 0644)
	os.WriteFile(filepath.Join(dir2, "test.go"), content, 0644)

	// Run builtin
	builtinRepfor(context.Background(), map[string]any{
		"search":  "func",
		"replace": "fn",
		"dir":     []any{dir1},
	}, "")

	// Run CLI
	cmd := exec.Command("repfor", "--cli", "--search", "func", "--replace", "fn", "--dir", dir2)
	cmd.Run()

	// Compare file contents
	data1, _ := os.ReadFile(filepath.Join(dir1, "test.go"))
	data2, _ := os.ReadFile(filepath.Join(dir2, "test.go"))

	if string(data1) != string(data2) {
		t.Error("builtin and CLI produced different results")
		t.Logf("builtin: %q", string(data1))
		t.Logf("cli:     %q", string(data2))
	}
}

// =============================================================================
// CleanDiff builtin tests
// =============================================================================

func TestCleanDiff_EmptyDiff(t *testing.T) {
	// Create a temp git repo with no changes
	dir := t.TempDir()
	exec.Command("git", "-C", dir, "init").Run()
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("content\n"), 0644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	result := builtinCleanDiff(context.Background(), map[string]any{
		"path": dir,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out cleanDiffResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Summary.FilesChanged != 0 {
		t.Errorf("expected 0 files changed, got %d", out.Summary.FilesChanged)
	}
}

func TestCleanDiff_UnstagedChanges(t *testing.T) {
	dir := t.TempDir()
	exec.Command("git", "-C", dir, "init").Run()
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("original\n"), 0644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	// Make unstaged change
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("modified\n"), 0644)

	result := builtinCleanDiff(context.Background(), map[string]any{
		"path": dir,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out cleanDiffResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Summary.FilesChanged != 1 {
		t.Errorf("expected 1 file changed, got %d", out.Summary.FilesChanged)
	}
	if out.Summary.Insertions != 1 {
		t.Errorf("expected 1 insertion, got %d", out.Summary.Insertions)
	}
	if out.Summary.Deletions != 1 {
		t.Errorf("expected 1 deletion, got %d", out.Summary.Deletions)
	}
}

func TestCleanDiff_StagedChanges(t *testing.T) {
	dir := t.TempDir()
	exec.Command("git", "-C", dir, "init").Run()
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("original\n"), 0644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()

	// Stage a change
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("staged\n"), 0644)
	exec.Command("git", "-C", dir, "add", "test.txt").Run()

	result := builtinCleanDiff(context.Background(), map[string]any{
		"path":   dir,
		"staged": true,
	}, "")

	var out cleanDiffResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Summary.FilesChanged != 1 {
		t.Errorf("expected 1 staged file changed, got %d", out.Summary.FilesChanged)
	}
}

func TestCleanDiff_StatOnly(t *testing.T) {
	dir := t.TempDir()
	exec.Command("git", "-C", dir, "init").Run()
	exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("original\n"), 0644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "init").Run()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("modified\n"), 0644)

	result := builtinCleanDiff(context.Background(), map[string]any{
		"path":      dir,
		"stat_only": true,
	}, "")

	var out cleanDiffResult
	json.Unmarshal([]byte(result.Output), &out)

	if len(out.Files) == 0 {
		t.Fatal("expected at least 1 file")
	}
	if out.Files[0].Hunks != nil {
		t.Error("stat_only should omit hunks")
	}
}

func TestCleanDiff_NotAGitRepo(t *testing.T) {
	dir := t.TempDir()

	result := builtinCleanDiff(context.Background(), map[string]any{
		"path": dir,
	}, "")

	if !result.IsError {
		t.Fatal("expected error for non-git directory")
	}
}

func TestCleanDiff_ParseDiffOutput(t *testing.T) {
	raw := `diff --git a/file.go b/file.go
index abc..def 100644
--- a/file.go
+++ b/file.go
@@ -1,3 +1,4 @@ package main
 func main() {
+	fmt.Println("hello")
 }
`

	files := parseUnifiedDiff(raw, false)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Path != "file.go" {
		t.Errorf("expected path file.go, got %q", files[0].Path)
	}
	if files[0].Insertions != 1 {
		t.Errorf("expected 1 insertion, got %d", files[0].Insertions)
	}
}

func TestCleanDiff_ParseRename(t *testing.T) {
	raw := `diff --git a/old.go b/new.go
similarity index 95%
rename from old.go
rename to new.go
`

	files := parseUnifiedDiff(raw, false)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Status != "renamed" {
		t.Errorf("expected status renamed, got %q", files[0].Status)
	}
	if files[0].OldPath != "old.go" {
		t.Errorf("expected old_path old.go, got %q", files[0].OldPath)
	}
	if files[0].Path != "new.go" {
		t.Errorf("expected path new.go, got %q", files[0].Path)
	}
}

// =============================================================================
// Registry integration
// =============================================================================

func TestPhase4_BuiltinsInRegistry(t *testing.T) {
	reg := DefaultRegistry()

	for _, name := range []string{"repfor", "cleanDiff"} {
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

func TestPhase4_TotalToolCount(t *testing.T) {
	reg := DefaultRegistry()
	// 5 subprocess + 13 builtins
	if reg.Len() != 18 {
		t.Errorf("expected 18 tools, got %d", reg.Len())
		t.Logf("tools: %v", reg.Names())
	}
}
