package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSig_TypeScript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.ts")
	os.WriteFile(path, []byte(`export interface User {
  id: number;
  name: string;
}

export function greet(user: User): string {
  return "Hello " + user.name;
}

export const MAX_USERS = 100;
`), 0644)

	result := builtinSig(context.Background(), map[string]any{
		"file": path,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out FileShape
	json.Unmarshal([]byte(result.Output), &out)

	if len(out.Types) == 0 {
		t.Error("expected types from TS file")
	}
	if len(out.Functions) == 0 {
		t.Error("expected functions from TS file")
	}
}

func TestSig_CSharp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Test.cs")
	os.WriteFile(path, []byte(`using System;

namespace MyApp
{
    public class User
    {
        public int Id { get; set; }
        public string Name { get; set; }
    }

    public interface IService
    {
        void Process();
    }
}
`), 0644)

	result := builtinSig(context.Background(), map[string]any{
		"file": path,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out FileShape
	json.Unmarshal([]byte(result.Output), &out)

	if len(out.Types) == 0 {
		t.Error("expected types from C# file")
	}
}

func TestSig_UnsupportedExt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.rb")
	os.WriteFile(path, []byte("class Foo; end"), 0644)

	result := builtinSig(context.Background(), map[string]any{
		"file": path,
	}, "")

	if !result.IsError {
		t.Fatal("expected error for unsupported extension")
	}
}

func TestSig_MissingFile(t *testing.T) {
	result := builtinSig(context.Background(), map[string]any{
		"file": "/nonexistent/file.go",
	}, "")
	if !result.IsError {
		t.Fatal("expected error for missing file")
	}
}

func TestSig_MissingParam(t *testing.T) {
	result := builtinSig(context.Background(), map[string]any{}, "")
	if !result.IsError {
		t.Fatal("expected error for missing file param")
	}
}

// =============================================================================
// Transform builtin tests
// =============================================================================

func TestTransform_Count(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	os.WriteFile(path, []byte(`[{"a":1},{"a":2},{"a":3}]`), 0644)

	result := builtinTransform(context.Background(), map[string]any{
		"file": path,
		"pipeline": []any{
			map[string]any{"op": "count"},
		},
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	if !strings.Contains(result.Output, "3") {
		t.Errorf("expected count of 3, got %s", result.Output)
	}
}

func TestTransform_Filter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	os.WriteFile(path, []byte(`[{"name":"a","val":1},{"name":"b","val":2},{"name":"c","val":3}]`), 0644)

	result := builtinTransform(context.Background(), map[string]any{
		"file": path,
		"pipeline": []any{
			map[string]any{"op": "filter", "key": "name", "eq": "b"},
		},
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out []map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	if len(out) != 1 || out[0]["name"] != "b" {
		t.Errorf("expected filtered to 'b', got %s", result.Output)
	}
}

func TestTransform_SortBy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	os.WriteFile(path, []byte(`[{"n":3},{"n":1},{"n":2}]`), 0644)

	result := builtinTransform(context.Background(), map[string]any{
		"file": path,
		"pipeline": []any{
			map[string]any{"op": "sort_by", "key": "n"},
		},
	}, "")

	var out []map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	if len(out) != 3 || out[0]["n"] != float64(1) {
		t.Errorf("expected sorted [1,2,3], got %s", result.Output)
	}
}

func TestTransform_GroupBy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	os.WriteFile(path, []byte(`[{"t":"a","v":1},{"t":"b","v":2},{"t":"a","v":3}]`), 0644)

	result := builtinTransform(context.Background(), map[string]any{
		"file": path,
		"pipeline": []any{
			map[string]any{"op": "group_by", "key": "t"},
		},
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out []map[string]any
	json.Unmarshal([]byte(result.Output), &out)
	if len(out) != 2 {
		t.Errorf("expected 2 groups, got %d", len(out))
	}
}

func TestTransform_Format(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	os.WriteFile(path, []byte(`[{"name":"Alice","age":30},{"name":"Bob","age":25}]`), 0644)

	result := builtinTransform(context.Background(), map[string]any{
		"file": path,
		"pipeline": []any{
			map[string]any{"op": "format", "template": "{name} is {age}"},
		},
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	if !strings.Contains(result.Output, "Alice is 30") {
		t.Errorf("expected formatted output, got %s", result.Output)
	}
}

func TestTransform_ExecMode(t *testing.T) {
	result := builtinTransform(context.Background(), map[string]any{
		"exec": `echo '[{"x":1},{"x":2}]'`,
		"pipeline": []any{
			map[string]any{"op": "count"},
		},
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	if !strings.Contains(result.Output, "2") {
		t.Errorf("expected count 2, got %s", result.Output)
	}
}

func TestTransform_DotPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	os.WriteFile(path, []byte(`[{"user":{"name":"A"}},{"user":{"name":"B"}}]`), 0644)

	result := builtinTransform(context.Background(), map[string]any{
		"file": path,
		"pipeline": []any{
			map[string]any{"op": "format", "template": "{user.name}"},
		},
	}, "")

	if !strings.Contains(result.Output, "A") || !strings.Contains(result.Output, "B") {
		t.Errorf("expected dot-path resolution, got %s", result.Output)
	}
}

func TestTransform_MissingPipeline(t *testing.T) {
	result := builtinTransform(context.Background(), map[string]any{
		"file": "/tmp/test.json",
	}, "")
	if !result.IsError {
		t.Fatal("expected error for missing pipeline")
	}
}

func TestTransform_MissingInput(t *testing.T) {
	result := builtinTransform(context.Background(), map[string]any{
		"pipeline": []any{map[string]any{"op": "count"}},
	}, "")
	if !result.IsError {
		t.Fatal("expected error for missing exec/file")
	}
}

// =============================================================================
// Errs builtin tests
// =============================================================================

func TestErrs_GoErrors(t *testing.T) {
	input := `./main.go:10:5: undefined: foo
./main.go:15:2: too many arguments
./util.go:3:1: imported and not used: "fmt"
`
	result := builtinErrs(context.Background(), map[string]any{
		"input": input,
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out errsResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Count != 3 {
		t.Errorf("expected 3 errors, got %d", out.Count)
	}
	if out.Format != "colon" {
		t.Errorf("expected format colon, got %q", out.Format)
	}
	if out.Files != 2 {
		t.Errorf("expected 2 files, got %d", out.Files)
	}
}

func TestErrs_RustErrors(t *testing.T) {
	input := `error[E0425]: cannot find value ` + "`foo`" + ` in this scope
 --> src/main.rs:10:5
warning: unused variable
 --> src/main.rs:3:9
`
	result := builtinErrs(context.Background(), map[string]any{
		"input": input,
	}, "")

	var out errsResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Format != "rust" {
		t.Errorf("expected format rust, got %q", out.Format)
	}
	if out.Count != 2 {
		t.Errorf("expected 2 errors, got %d", out.Count)
	}
	if out.Errors[0].Code != "E0425" {
		t.Errorf("expected code E0425, got %q", out.Errors[0].Code)
	}
}

func TestErrs_TypeScriptErrors(t *testing.T) {
	input := `src/app.ts(10,5): error TS2304: Cannot find name 'foo'.
src/app.ts(15,1): error TS2322: Type mismatch.
`
	result := builtinErrs(context.Background(), map[string]any{
		"input": input,
	}, "")

	var out errsResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Format != "tsc" {
		t.Errorf("expected format tsc, got %q", out.Format)
	}
	if out.Count != 2 {
		t.Errorf("expected 2 errors, got %d", out.Count)
	}
}

func TestErrs_ANSIStripping(t *testing.T) {
	input := "\x1b[31m./main.go:10:5: error message\x1b[0m\n"
	result := builtinErrs(context.Background(), map[string]any{
		"input": input,
	}, "")

	var out errsResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Count != 1 {
		t.Errorf("expected 1 error after ANSI stripping, got %d", out.Count)
	}
}

func TestErrs_FormatHint(t *testing.T) {
	input := `src/main.rs:10:5: some error`
	result := builtinErrs(context.Background(), map[string]any{
		"input":  input,
		"format": "colon",
	}, "")

	var out errsResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Format != "colon" {
		t.Errorf("expected format colon (from hint), got %q", out.Format)
	}
}

func TestErrs_Deduplication(t *testing.T) {
	input := `./main.go:10:5: duplicate error
./main.go:10:5: duplicate error
./main.go:10:5: duplicate error
`
	result := builtinErrs(context.Background(), map[string]any{
		"input": input,
	}, "")

	var out errsResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Count != 1 {
		t.Errorf("expected 1 deduplicated error, got %d", out.Count)
	}
}

func TestErrs_EmptyInput(t *testing.T) {
	result := builtinErrs(context.Background(), map[string]any{
		"input": "",
	}, "")
	if !result.IsError {
		t.Fatal("expected error for empty input")
	}
}

func TestErrs_NoErrors(t *testing.T) {
	result := builtinErrs(context.Background(), map[string]any{
		"input": "Build complete.\nNo errors.\n",
	}, "")

	var out errsResult
	json.Unmarshal([]byte(result.Output), &out)

	if out.Count != 0 {
		t.Errorf("expected 0 errors, got %d", out.Count)
	}
	if out.Summary != "no errors found" {
		t.Errorf("unexpected summary: %q", out.Summary)
	}
}

func TestErrs_MissingParam(t *testing.T) {
	result := builtinErrs(context.Background(), map[string]any{}, "")
	if !result.IsError {
		t.Fatal("expected error for missing input")
	}
}

// =============================================================================
// Registry integration
// =============================================================================

func TestPhase6_BuiltinsInRegistry(t *testing.T) {
	reg := DefaultRegistry()

	for _, name := range []string{"sig", "transform", "errs"} {
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

func TestPhase6_AllToolsAreBuiltins(t *testing.T) {
	reg := DefaultRegistry()

	// All 18 tools should be builtins now (15 migrated + 3 original)
	if reg.Len() != 18 {
		t.Errorf("expected 18 tools, got %d", reg.Len())
		t.Logf("tools: %v", reg.Names())
	}

	// AllTools() should be empty
	subprocess := AllTools()
	if len(subprocess) != 0 {
		t.Errorf("expected 0 subprocess tools, got %d", len(subprocess))
	}

	// Every tool should be a builtin
	for _, name := range reg.Names() {
		def, _ := reg.Get(name)
		if !def.IsBuiltinTool() {
			t.Errorf("tool %q is not a builtin", name)
		}
	}
}

func TestPhase6_NoBinaryDependencies(t *testing.T) {
	reg := DefaultRegistry()
	missing := reg.CheckBinaries()

	// Should be empty — all builtins, no binaries needed
	if len(missing) != 0 {
		t.Errorf("expected no missing binaries, got %v", missing)
	}
}
