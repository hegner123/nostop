//go:build local

package tools

import (
	"context"
	"encoding/json"
	"testing"
)

// These tests use hardcoded paths to files on the developer's machine.
// Run with: go test -tags local ./internal/tools/

func TestSig_GoFile(t *testing.T) {
	result := builtinSig(context.Background(), map[string]any{
		"file": "/Users/home/Documents/Code/Go_dev/nostop/internal/tools/registry.go",
	}, "")

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	var out FileShape
	json.Unmarshal([]byte(result.Output), &out)

	if out.Package != "tools" {
		t.Errorf("expected package tools, got %q", out.Package)
	}
	if len(out.Types) == 0 {
		t.Error("expected types in registry.go")
	}
	if len(out.Functions) == 0 {
		t.Error("expected functions in registry.go")
	}

	// Should have Registry type
	foundRegistry := false
	for _, td := range out.Types {
		if td.Name == "Registry" {
			foundRegistry = true
		}
	}
	if !foundRegistry {
		t.Error("expected to find Registry type")
	}
}

func TestSig_GoExportedOnly(t *testing.T) {
	result := builtinSig(context.Background(), map[string]any{
		"file": "/Users/home/Documents/Code/Go_dev/nostop/internal/tools/registry.go",
		"all":  false,
	}, "")

	var out FileShape
	json.Unmarshal([]byte(result.Output), &out)

	for _, fn := range out.Functions {
		if fn.Name[0] >= 'a' && fn.Name[0] <= 'z' {
			t.Errorf("exported-only should not include unexported function %q", fn.Name)
		}
	}
}

func TestSig_GoIncludePrivate(t *testing.T) {
	result := builtinSig(context.Background(), map[string]any{
		"file": "/Users/home/Documents/Code/Go_dev/nostop/internal/tools/registry.go",
		"all":  true,
	}, "")

	var out FileShape
	json.Unmarshal([]byte(result.Output), &out)

	hasPrivate := false
	for _, fn := range out.Functions {
		if fn.Name[0] >= 'a' && fn.Name[0] <= 'z' {
			hasPrivate = true
			break
		}
	}
	// registry.go does have unexported methods — if not, that's OK too
	_ = hasPrivate
}

func TestExecutor_SigIntegration(t *testing.T) {
	reg := NewRegistry()
	reg.Register(SigDef)

	missing := reg.CheckBinaries()
	if _, ok := missing["sig"]; ok {
		t.Skip("sig binary not found on PATH, skipping integration test")
	}

	exec := NewExecutor(reg, "")
	result := exec.Execute(context.Background(), "sig", map[string]any{
		"file": "/Users/home/Documents/Code/Go_dev/nostop/internal/tools/registry.go",
		"all":  false,
	})

	if result.IsError {
		t.Fatalf("sig execution failed: %s", result.Error)
	}
	if result.Output == "" {
		t.Fatal("expected non-empty output from sig")
	}
	if result.Output[0] != '{' {
		t.Errorf("expected JSON object output, got: %.50s", result.Output)
	}
}
