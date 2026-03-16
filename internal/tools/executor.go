package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/hegner123/nostop/internal/mcp"
)

const (
	// DefaultTimeout is the per-tool execution timeout when none is specified.
	DefaultTimeout = 30 * time.Second

	// MaxOutputBytes caps tool stdout to prevent context explosion (50KB).
	MaxOutputBytes = 50 * 1024
)

// Result holds the output of a tool execution.
type Result struct {
	// Output is the tool's stdout content (JSON).
	Output string

	// IsError is true when the tool exited non-zero or timed out.
	IsError bool

	// Error contains stderr content on failure, or a timeout message.
	Error string
}

// Executor runs tool binaries as subprocesses, mapping Claude's JSON input
// parameters to CLI flags via the tool's FlagMap. MCP tools are dispatched
// via the ServerManager instead.
type Executor struct {
	registry       *Registry
	defaultTimeout time.Duration
	workDir        string
	readTracker    *ReadTracker
	mcpManager     *mcp.ServerManager
}

// NewExecutor creates an executor bound to a registry and working directory.
// The executor owns a ReadTracker that enforces read-before-write safety.
func NewExecutor(registry *Registry, workDir string) *Executor {
	return &Executor{
		registry:       registry,
		defaultTimeout: DefaultTimeout,
		workDir:        workDir,
		readTracker:    NewReadTracker(),
	}
}

// SetMCPManager sets the MCP server manager for dispatching MCP tool calls.
func (e *Executor) SetMCPManager(mgr *mcp.ServerManager) {
	e.mcpManager = mgr
}

// ReadTracker returns the executor's read tracker for inspection/testing.
func (e *Executor) ReadTracker() *ReadTracker {
	return e.readTracker
}

// Execute runs a tool by name with the given input parameters.
// Builtin tools execute in-process; subprocess tools are invoked via exec.Command.
// All executions are logged to the agent log.
func (e *Executor) Execute(ctx context.Context, name string, input map[string]any) Result {
	def, ok := e.registry.Get(name)
	if !ok {
		log := GetAgentLogger()
		log.LogToolResult(name, "", "unknown tool", true, 0)
		return Result{
			IsError: true,
			Error:   fmt.Sprintf("unknown tool: %s", name),
		}
	}

	start := time.Now()

	// Dispatch based on tool type
	var result Result
	if def.IsBuiltinTool() {
		result = e.executeBuiltin(ctx, def, input)
	} else if def.IsMCPTool() {
		result = e.executeMCP(ctx, def, input)
	} else {
		result = e.executeSubprocess(ctx, def, input)
	}

	// Log the execution
	elapsed := time.Since(start)
	log := GetAgentLogger()
	output := result.Output
	if result.IsError {
		output = result.Error
	}
	log.LogToolResult(name, "", output, result.IsError, elapsed)

	return result
}

// executeSubprocess runs a tool via exec.Command.
func (e *Executor) executeSubprocess(ctx context.Context, def ToolDef, input map[string]any) Result {
	// Resolve binary path
	binPath, err := exec.LookPath(def.Binary)
	if err != nil {
		return Result{
			IsError: true,
			Error:   fmt.Sprintf("binary %q not found on PATH", def.Binary),
		}
	}

	// Build argument list
	args := buildArgs(def, input)

	// Determine timeout
	timeout := def.Timeout
	if timeout == 0 {
		timeout = e.defaultTimeout
	}

	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, binPath, args...)
	if e.workDir != "" {
		cmd.Dir = e.workDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Pipe stdin if the tool expects it
	if def.StdinParam != "" {
		if val, ok := input[def.StdinParam]; ok {
			stdinStr, isStr := val.(string)
			if !isStr {
				// Marshal non-string values to JSON for stdin
				data, marshalErr := json.Marshal(val)
				if marshalErr != nil {
					return Result{
						IsError: true,
						Error:   fmt.Sprintf("failed to serialize stdin param %q: %s", def.StdinParam, marshalErr),
					}
				}
				stdinStr = string(data)
			}
			cmd.Stdin = strings.NewReader(stdinStr)
		}
	}

	// Run the command
	runErr := cmd.Run()

	// Check for timeout
	if execCtx.Err() == context.DeadlineExceeded {
		return Result{
			IsError: true,
			Error:   fmt.Sprintf("tool %q timed out after %s", def.Name, timeout),
		}
	}

	// Check for cancellation
	if execCtx.Err() == context.Canceled {
		return Result{
			IsError: true,
			Error:   fmt.Sprintf("tool %q was cancelled", def.Name),
		}
	}

	if runErr != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = runErr.Error()
		}
		return Result{
			IsError: true,
			Error:   errMsg,
		}
	}

	// Truncate output if too large
	output := stdout.String()
	if len(output) > MaxOutputBytes {
		output = output[:MaxOutputBytes] + "\n... (output truncated at 50KB)"
	}

	// Validate JSON output
	output = strings.TrimSpace(output)
	if output != "" && !json.Valid([]byte(output)) {
		// Return as-is but wrap in a JSON-safe structure
		wrapped, wrapErr := json.Marshal(map[string]string{"raw_output": output})
		if wrapErr == nil {
			output = string(wrapped)
		}
	}

	return Result{
		Output: output,
	}
}

// executeBuiltin runs an in-process tool with timeout enforcement.
// It injects the ReadTracker into context so builtins can enforce read-before-write.
func (e *Executor) executeBuiltin(ctx context.Context, def ToolDef, input map[string]any) Result {
	timeout := def.Timeout
	if timeout == 0 {
		timeout = e.defaultTimeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Inject read tracker so builtinRead/builtinWrite can access it
	execCtx = ContextWithReadTracker(execCtx, e.readTracker)

	result := def.Builtin(execCtx, input, e.workDir)

	if execCtx.Err() == context.DeadlineExceeded {
		return Result{
			IsError: true,
			Error:   fmt.Sprintf("builtin %q timed out after %s", def.Name, timeout),
		}
	}

	// Apply output truncation to builtins too
	if !result.IsError && len(result.Output) > MaxOutputBytes {
		result.Output = result.Output[:MaxOutputBytes] + "\n... (output truncated at 50KB)"
	}

	return result
}

// buildArgs constructs the CLI argument list from a ToolDef and input parameters.
func buildArgs(def ToolDef, input map[string]any) []string {
	var args []string
	var positionalArgs []string

	// Prepend --cli if required
	if def.NeedsCLI {
		args = append(args, "--cli")
	}

	for param, val := range input {
		// Skip stdin params — they're piped, not passed as flags
		if param == def.StdinParam {
			continue
		}

		spec, ok := def.FlagMap[param]
		if !ok {
			continue
		}

		switch spec.Type {
		case "string":
			s := toString(val)
			if s == "" {
				continue
			}
			if spec.Positional {
				positionalArgs = append(positionalArgs, s)
			} else {
				args = append(args, spec.Flag, s)
			}

		case "bool":
			b, isBool := val.(bool)
			if !isBool {
				continue
			}
			if b {
				args = append(args, spec.Flag)
			}

		case "int":
			n := toInt(val)
			if n == 0 {
				continue
			}
			args = append(args, spec.Flag, strconv.Itoa(n))

		case "array":
			items := toStringSlice(val)
			if len(items) == 0 {
				continue
			}
			// Join array items with comma for CLI flag
			joined := strings.Join(items, ",")
			if spec.Positional {
				positionalArgs = append(positionalArgs, joined)
			} else {
				args = append(args, spec.Flag, joined)
			}
		}
	}

	// Positional args go at the end
	args = append(args, positionalArgs...)

	return args
}

// toString converts an any value to string.
// For complex types (slices, maps), it JSON-serializes the value.
// This handles cases like transform's pipeline param where the API
// sends an array of objects but the CLI expects a JSON string flag.
func toString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case int:
		return strconv.Itoa(val)
	case bool:
		return strconv.FormatBool(val)
	case []any, map[string]any:
		data, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("%v", val)
		}
		return string(data)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// toInt converts an any value to int.
func toInt(v any) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case string:
		n, _ := strconv.Atoi(val)
		return n
	default:
		return 0
	}
}

// toStringSlice converts an any value to []string.
func toStringSlice(v any) []string {
	switch val := v.(type) {
	case []any:
		result := make([]string, 0, len(val))
		for _, item := range val {
			result = append(result, toString(item))
		}
		return result
	case []string:
		return val
	default:
		return nil
	}
}

// executeMCP dispatches a tool call to an MCP server via the ServerManager.
func (e *Executor) executeMCP(ctx context.Context, def ToolDef, input map[string]any) Result {
	if e.mcpManager == nil {
		return Result{
			IsError: true,
			Error:   fmt.Sprintf("MCP tool %q: no MCP manager configured", def.Name),
		}
	}

	// Strip namespace prefix to get the bare tool name the MCP server expects.
	// Registry name: "mcp__stump__stump" -> server expects: "stump"
	bareToolName := mcpBareToolName(def.Name)

	result, err := e.mcpManager.CallTool(ctx, def.MCPServer, bareToolName, input)
	if err != nil {
		return Result{
			IsError: true,
			Error:   err.Error(),
		}
	}

	return Result{
		Output:  extractMCPText(result),
		IsError: result.IsError,
	}
}

// mcpBareToolName strips the "mcp__<server>__" prefix from a namespaced tool name.
func mcpBareToolName(name string) string {
	parts := strings.SplitN(name, "__", 3)
	if len(parts) == 3 {
		return parts[2]
	}
	return name
}

// extractMCPText concatenates all text content blocks from an MCP tool result,
// separated by newlines. Non-text blocks (image, resource) are represented
// as "[<type> content]" placeholders.
func extractMCPText(result *mcp.ToolResult) string {
	var parts []string
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		default:
			parts = append(parts, fmt.Sprintf("[%s content]", block.Type))
		}
	}
	return strings.Join(parts, "\n")
}
