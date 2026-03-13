package tools

import (
	"context"
	"testing"
)

func TestBuildArgs_StringFlag(t *testing.T) {
	def := ToolDef{
		NeedsCLI: true,
		FlagMap: map[string]FlagSpec{
			"file": {Flag: "--file", Type: "string"},
		},
	}

	args := buildArgs(def, map[string]any{
		"file": "/tmp/test.go",
	})

	expected := []string{"--cli", "--file", "/tmp/test.go"}
	assertArgsContain(t, args, expected)
}

func TestBuildArgs_BoolFlag(t *testing.T) {
	def := ToolDef{
		NeedsCLI: true,
		FlagMap: map[string]FlagSpec{
			"recursive": {Flag: "--recursive", Type: "bool"},
			"verbose":   {Flag: "--verbose", Type: "bool"},
		},
	}

	args := buildArgs(def, map[string]any{
		"recursive": true,
		"verbose":   false,
	})

	assertContains(t, args, "--recursive")
	assertNotContains(t, args, "--verbose")
}

func TestBuildArgs_IntFlag(t *testing.T) {
	def := ToolDef{
		NeedsCLI: false,
		FlagMap: map[string]FlagSpec{
			"context": {Flag: "--context", Type: "int"},
		},
	}

	args := buildArgs(def, map[string]any{
		"context": float64(3), // JSON numbers are float64
	})

	assertArgsContain(t, args, []string{"--context", "3"})
}

func TestBuildArgs_IntFlagZeroSkipped(t *testing.T) {
	def := ToolDef{
		NeedsCLI: false,
		FlagMap: map[string]FlagSpec{
			"context": {Flag: "--context", Type: "int"},
		},
	}

	args := buildArgs(def, map[string]any{
		"context": float64(0),
	})

	assertNotContains(t, args, "--context")
}

func TestBuildArgs_ArrayFlag(t *testing.T) {
	def := ToolDef{
		NeedsCLI: true,
		FlagMap: map[string]FlagSpec{
			"dir": {Flag: "--dir", Type: "array"},
		},
	}

	args := buildArgs(def, map[string]any{
		"dir": []any{"/tmp/a", "/tmp/b"},
	})

	assertArgsContain(t, args, []string{"--dir", "/tmp/a,/tmp/b"})
}

func TestBuildArgs_PositionalArg(t *testing.T) {
	def := ToolDef{
		NeedsCLI: true,
		FlagMap: map[string]FlagSpec{
			"file": {Type: "string", Positional: true},
			"all":  {Flag: "--all", Type: "bool"},
		},
	}

	args := buildArgs(def, map[string]any{
		"file": "/tmp/test.go",
		"all":  true,
	})

	// Positional arg should be at the end
	if len(args) == 0 {
		t.Fatal("expected args, got none")
	}
	last := args[len(args)-1]
	if last != "/tmp/test.go" {
		t.Errorf("expected positional arg at end, got %q", last)
	}
	assertContains(t, args, "--all")
}

func TestBuildArgs_NoCLIFlag(t *testing.T) {
	def := ToolDef{
		NeedsCLI: false,
		FlagMap: map[string]FlagSpec{
			"dir": {Type: "string", Positional: true},
		},
	}

	args := buildArgs(def, map[string]any{
		"dir": "/tmp",
	})

	assertNotContains(t, args, "--cli")
	assertContains(t, args, "/tmp")
}

func TestBuildArgs_StdinParamSkipped(t *testing.T) {
	def := ToolDef{
		NeedsCLI:   true,
		StdinParam: "input",
		FlagMap: map[string]FlagSpec{
			"input":  {Flag: "--input", Type: "string"},
			"format": {Flag: "--format", Type: "string"},
		},
	}

	args := buildArgs(def, map[string]any{
		"input":  "some error text",
		"format": "go",
	})

	// stdin param should NOT appear as a flag
	assertNotContains(t, args, "--input")
	assertNotContains(t, args, "some error text")
	// regular param should appear
	assertArgsContain(t, args, []string{"--format", "go"})
}

func TestBuildArgs_UnknownParamIgnored(t *testing.T) {
	def := ToolDef{
		NeedsCLI: true,
		FlagMap: map[string]FlagSpec{
			"file": {Flag: "--file", Type: "string"},
		},
	}

	args := buildArgs(def, map[string]any{
		"file":    "/tmp/test.go",
		"unknown": "should be ignored",
	})

	assertNotContains(t, args, "unknown")
	assertNotContains(t, args, "should be ignored")
}

func TestBuildArgs_JSONComplexStringParam(t *testing.T) {
	def := ToolDef{
		NeedsCLI: true,
		FlagMap: map[string]FlagSpec{
			"pipeline": {Flag: "--pipeline", Type: "string"},
		},
	}

	// Simulates transform's pipeline parameter - array of objects from JSON
	args := buildArgs(def, map[string]any{
		"pipeline": []any{
			map[string]any{"op": "count"},
		},
	})

	assertContains(t, args, "--pipeline")
	// The value should be JSON-serialized
	found := false
	for _, arg := range args {
		if arg == `[{"op":"count"}]` {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected JSON-serialized pipeline arg, got args: %v", args)
	}
}

func TestToString(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want string
	}{
		{"string", "hello", "hello"},
		{"float64", float64(42), "42"},
		{"float64_decimal", float64(3.14), "3.14"},
		{"int", 7, "7"},
		{"bool_true", true, "true"},
		{"bool_false", false, "false"},
		{"slice", []any{"a", "b"}, `["a","b"]`},
		{"map", map[string]any{"k": "v"}, `{"k":"v"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toString(tt.val)
			if got != tt.want {
				t.Errorf("toString(%v) = %q, want %q", tt.val, got, tt.want)
			}
		})
	}
}

func TestToStringSlice(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want []string
	}{
		{"any_slice", []any{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"string_slice", []string{"x", "y"}, []string{"x", "y"}},
		{"nil", nil, nil},
		{"wrong_type", 42, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toStringSlice(tt.val)
			if len(got) != len(tt.want) {
				t.Errorf("toStringSlice(%v) len = %d, want %d", tt.val, len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("toStringSlice(%v)[%d] = %q, want %q", tt.val, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExecutor_UnknownTool(t *testing.T) {
	reg := NewRegistry()
	exec := NewExecutor(reg, "")

	result := exec.Execute(context.Background(), "nonexistent", nil)
	if !result.IsError {
		t.Fatal("expected error for unknown tool")
	}
	if result.Error != "unknown tool: nonexistent" {
		t.Errorf("unexpected error: %s", result.Error)
	}
}

func TestExecutor_MissingBinary(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ToolDef{
		Name:    "fake",
		Binary:  "nonexistent-binary-abc123",
		FlagMap: map[string]FlagSpec{},
	})
	exec := NewExecutor(reg, "")

	result := exec.Execute(context.Background(), "fake", nil)
	if !result.IsError {
		t.Fatal("expected error for missing binary")
	}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	def := ToolDef{Name: "test", Description: "test tool", Binary: "echo"}
	reg.Register(def)

	got, ok := reg.Get("test")
	if !ok {
		t.Fatal("expected to find registered tool")
	}
	if got.Name != "test" {
		t.Errorf("got name %q, want %q", got.Name, "test")
	}
}

func TestRegistry_Remove(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ToolDef{Name: "a", Binary: "echo"})
	reg.Register(ToolDef{Name: "b", Binary: "echo"})

	reg.Remove("a")

	if _, ok := reg.Get("a"); ok {
		t.Error("expected tool 'a' to be removed")
	}
	if _, ok := reg.Get("b"); !ok {
		t.Error("expected tool 'b' to still exist")
	}
	if reg.Len() != 1 {
		t.Errorf("expected len 1, got %d", reg.Len())
	}
}

func TestRegistry_APITools(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ToolDef{
		Name:        "test",
		Description: "a test tool",
		InputSchema: map[string]any{"type": "object"},
		Binary:      "echo",
	})

	apiTools := reg.APITools()
	if len(apiTools) != 1 {
		t.Fatalf("expected 1 api tool, got %d", len(apiTools))
	}
	if apiTools[0].Name != "test" {
		t.Errorf("got name %q, want %q", apiTools[0].Name, "test")
	}
	if apiTools[0].Description != "a test tool" {
		t.Errorf("got description %q", apiTools[0].Description)
	}
}

func TestRegistry_APIToolsPreservesOrder(t *testing.T) {
	reg := NewRegistry()
	reg.Register(ToolDef{Name: "second", Binary: "echo"})
	reg.Register(ToolDef{Name: "first", Binary: "echo"})
	reg.Register(ToolDef{Name: "third", Binary: "echo"})

	apiTools := reg.APITools()
	if len(apiTools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(apiTools))
	}
	// Should preserve insertion order, not alphabetical
	if apiTools[0].Name != "second" || apiTools[1].Name != "first" || apiTools[2].Name != "third" {
		t.Errorf("unexpected order: %s, %s, %s", apiTools[0].Name, apiTools[1].Name, apiTools[2].Name)
	}
}

func TestDefaultRegistry_Has18Tools(t *testing.T) {
	reg := DefaultRegistry()
	// 15 terse-mcp subprocess tools + 3 builtins (read, write, bash)
	if reg.Len() != 18 {
		t.Errorf("expected 18 tools in default registry, got %d", reg.Len())
	}
}

func TestDefaultRegistry_AllToolsHaveSchemas(t *testing.T) {
	reg := DefaultRegistry()
	for _, name := range reg.Names() {
		def, _ := reg.Get(name)
		if def.InputSchema == nil {
			t.Errorf("tool %q has nil InputSchema", name)
		}
		// Subprocess tools need a Binary; builtins don't
		if !def.IsBuiltinTool() && def.Binary == "" {
			t.Errorf("subprocess tool %q has empty Binary", name)
		}
		if def.IsBuiltinTool() && def.Builtin == nil {
			t.Errorf("builtin tool %q has nil Builtin func", name)
		}
		if def.Description == "" {
			t.Errorf("tool %q has empty Description", name)
		}
	}
}

// --- Test helpers ---

func assertContains(t *testing.T, args []string, want string) {
	t.Helper()
	for _, arg := range args {
		if arg == want {
			return
		}
	}
	t.Errorf("args %v does not contain %q", args, want)
}

func assertNotContains(t *testing.T, args []string, notwant string) {
	t.Helper()
	for _, arg := range args {
		if arg == notwant {
			t.Errorf("args %v should not contain %q", args, notwant)
			return
		}
	}
}

func assertArgsContain(t *testing.T, args, want []string) {
	t.Helper()
	for i := range args {
		if i+len(want) > len(args) {
			break
		}
		match := true
		for j, w := range want {
			if args[i+j] != w {
				match = false
				break
			}
		}
		if match {
			return
		}
	}
	t.Errorf("args %v does not contain subsequence %v", args, want)
}
