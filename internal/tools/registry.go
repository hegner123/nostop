// Package tools provides a tool registry and subprocess executor for agentic tool use.
// Each tool is either a standalone CLI binary invoked via exec.Command, or a builtin
// that executes directly in-process (Read, Write, Bash).
package tools

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"time"

	"github.com/hegner123/nostop/internal/api"
)

// BuiltinFunc is the signature for tools that execute in-process rather than
// spawning a subprocess. The workDir parameter is the executor's working directory.
type BuiltinFunc func(ctx context.Context, input map[string]any, workDir string) Result

// ToolDef defines a tool that Claude can invoke during agentic conversations.
// A tool is either a subprocess tool (Binary set) or a builtin (Builtin set).
type ToolDef struct {
	// Name is the tool name sent to the API (e.g. "checkfor").
	Name string

	// Description is shown to Claude to help it decide when to use the tool.
	Description string

	// InputSchema is the JSON Schema for the tool's parameters, passed directly
	// to api.Tool.InputSchema in API requests.
	InputSchema map[string]any

	// Builtin, when non-nil, means this tool runs in-process instead of
	// spawning a subprocess. Binary/FlagMap/NeedsCLI/StdinParam are ignored.
	Builtin BuiltinFunc

	// Binary is the executable name resolved via PATH (e.g. "checkfor", "stump-core").
	// Empty for builtin tools.
	Binary string

	// NeedsCLI prepends --cli to the argument list when true.
	NeedsCLI bool

	// FlagMap maps Claude's input parameter names to CLI flag specifications.
	FlagMap map[string]FlagSpec

	// StdinParam names the input parameter whose value is piped to stdin
	// instead of passed as a flag. Empty means no stdin piping.
	StdinParam string

	// Timeout is the per-invocation timeout. Zero uses the executor's default.
	Timeout time.Duration

	// IsBuiltin returns true if this tool runs in-process.
	// Convenience method — equivalent to checking Builtin != nil.
}

// IsBuiltinTool returns true if this tool executes in-process.
func (d ToolDef) IsBuiltinTool() bool {
	return d.Builtin != nil
}

// FlagSpec describes how a single input parameter maps to a CLI flag.
type FlagSpec struct {
	// Flag is the CLI flag name (e.g. "--file", "--dir").
	Flag string

	// Type controls serialization: "string", "bool", "int", "array".
	Type string

	// Positional appends the value as a positional arg without a flag prefix.
	Positional bool
}

// Registry holds tool definitions and converts them to API-ready format.
type Registry struct {
	tools map[string]ToolDef
	order []string // insertion order for stable iteration
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]ToolDef),
	}
}

// Register adds a tool definition to the registry.
// If a tool with the same name exists, it is replaced.
func (r *Registry) Register(def ToolDef) {
	if _, exists := r.tools[def.Name]; !exists {
		r.order = append(r.order, def.Name)
	}
	r.tools[def.Name] = def
}

// Get returns a tool definition by name.
func (r *Registry) Get(name string) (ToolDef, bool) {
	def, ok := r.tools[name]
	return def, ok
}

// Remove removes a tool from the registry.
func (r *Registry) Remove(name string) {
	if _, exists := r.tools[name]; !exists {
		return
	}
	delete(r.tools, name)
	filtered := r.order[:0]
	for _, n := range r.order {
		if n != name {
			filtered = append(filtered, n)
		}
	}
	r.order = filtered
}

// Names returns all registered tool names in sorted order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// APITools converts all registered tool definitions to api.Tool slices
// suitable for inclusion in API requests.
func (r *Registry) APITools() []api.Tool {
	apiTools := make([]api.Tool, 0, len(r.tools))
	for _, name := range r.order {
		def := r.tools[name]
		apiTools = append(apiTools, api.Tool{
			Name:        def.Name,
			Description: def.Description,
			InputSchema: def.InputSchema,
		})
	}
	return apiTools
}

// Len returns the number of registered tools.
func (r *Registry) Len() int {
	return len(r.tools)
}

// CheckBinaries verifies that each registered subprocess tool's binary is on PATH.
// Builtin tools are skipped. Returns a map of tool name to error for any missing binaries.
func (r *Registry) CheckBinaries() map[string]error {
	missing := make(map[string]error)
	for name, def := range r.tools {
		if def.IsBuiltinTool() {
			continue
		}
		if _, err := exec.LookPath(def.Binary); err != nil {
			missing[name] = fmt.Errorf("binary %q not found on PATH: %w", def.Binary, err)
		}
	}
	return missing
}

// DefaultRegistry creates a registry with all tools registered:
// 15 terse-mcp subprocess tools + 3 builtins (read, write, bash).
func DefaultRegistry() *Registry {
	reg := NewRegistry()
	for _, def := range AllTools() {
		reg.Register(def)
	}
	for _, def := range BuiltinTools() {
		reg.Register(def)
	}
	return reg
}
