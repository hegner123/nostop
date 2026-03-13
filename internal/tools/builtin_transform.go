package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// transformOp represents a single pipeline operation.
type transformOp struct {
	Op       string  `json:"op"`
	Key      string  `json:"key,omitempty"`
	Desc     bool    `json:"desc,omitempty"`
	Eq       any     `json:"eq,omitempty"`
	Neq      any     `json:"neq,omitempty"`
	Contains *string `json:"contains,omitempty"`
	Exists   *bool   `json:"exists,omitempty"`
	Template string  `json:"template,omitempty"`
}

// TransformDef runs composable operations on JSON arrays.
var TransformDef = ToolDef{
	Name:        "transform",
	Description: "Composable JSON data pipeline. Takes a JSON array (from exec command or file) and runs it through a sequence of operations: group_by, sort_by, filter, count, flatten, format.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"exec": map[string]any{
				"type":        "string",
				"description": "Shell command whose stdout is a JSON array. Use exec OR file, not both.",
			},
			"file": map[string]any{
				"type":        "string",
				"description": "Absolute path to a JSON file containing an array. Use file OR exec, not both.",
			},
			"pipeline": map[string]any{
				"type":        "array",
				"description": "Ordered sequence of operations. Each element: {\"op\": \"<name>\", ...args}.",
			},
		},
		"required": []string{"pipeline"},
	},
	Builtin: builtinTransform,
	Timeout: 30 * time.Second,
}

func builtinTransform(ctx context.Context, input map[string]any, workDir string) Result {
	// Parse pipeline
	pipelineRaw, ok := input["pipeline"].([]any)
	if !ok || len(pipelineRaw) == 0 {
		return Result{IsError: true, Error: "pipeline is required (non-empty array of operations)"}
	}

	var ops []transformOp
	for _, rawOp := range pipelineRaw {
		opData, marshalErr := json.Marshal(rawOp)
		if marshalErr != nil {
			return Result{IsError: true, Error: fmt.Sprintf("invalid pipeline operation: %s", marshalErr)}
		}
		var op transformOp
		if unmarshalErr := json.Unmarshal(opData, &op); unmarshalErr != nil {
			return Result{IsError: true, Error: fmt.Sprintf("invalid pipeline operation: %s", unmarshalErr)}
		}
		ops = append(ops, op)
	}

	// Get input data
	var jsonInput string
	if execCmd, ok := input["exec"].(string); ok && execCmd != "" {
		shell := "/bin/zsh"
		if _, err := os.Stat(shell); err != nil {
			shell = "/bin/bash"
		}
		cmd := exec.CommandContext(ctx, shell, "-c", execCmd)
		if workDir != "" {
			cmd.Dir = workDir
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if runErr := cmd.Run(); runErr != nil {
			return Result{IsError: true, Error: fmt.Sprintf("exec failed: %s\n%s", runErr, stderr.String())}
		}
		jsonInput = stdout.String()
	} else if filePath, ok := input["file"].(string); ok && filePath != "" {
		filePath = resolvePath(filePath, workDir)
		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return Result{IsError: true, Error: fmt.Sprintf("failed to read file: %s", readErr)}
		}
		jsonInput = string(data)
	} else {
		return Result{IsError: true, Error: "either exec or file is required"}
	}

	// Execute pipeline
	output, err := transformExecute(jsonInput, ops)
	if err != nil {
		return Result{IsError: true, Error: err.Error()}
	}

	return Result{Output: string(output)}
}

func transformExecute(input string, ops []transformOp) (json.RawMessage, error) {
	var data any
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		return nil, fmt.Errorf("invalid input JSON: %w", err)
	}

	current := data
	for i, op := range ops {
		var err error
		current, err = transformApplyOp(current, op)
		if err != nil {
			return nil, fmt.Errorf("pipeline step %d (%s): %w", i, op.Op, err)
		}
	}

	transformStripGroupTags(current)
	return json.MarshalIndent(current, "", "  ")
}

func transformStripGroupTags(data any) {
	arr, ok := data.([]any)
	if !ok {
		return
	}
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			delete(m, "__group_by")
			if items, ok := m["items"]; ok {
				transformStripGroupTags(items)
			}
		}
	}
}

func transformApplyOp(data any, op transformOp) (any, error) {
	switch op.Op {
	case "group_by":
		return transformGroupBy(data, op)
	case "sort_by":
		return transformSortBy(data, op)
	case "filter":
		return transformFilter(data, op)
	case "count":
		return transformCount(data, op)
	case "flatten":
		return transformFlatten(data)
	case "format":
		return transformFormat(data, op)
	default:
		return nil, fmt.Errorf("unknown op: %q", op.Op)
	}
}

func transformGroupBy(data any, op transformOp) (any, error) {
	if op.Key == "" {
		return nil, fmt.Errorf("group_by requires 'key'")
	}
	arr, err := transformToArray(data)
	if err != nil {
		return nil, err
	}

	groups := make(map[string][]any)
	var order []string
	for _, item := range arr {
		val := transformResolve(item, op.Key)
		key := transformToString(val)
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], item)
	}

	result := make([]any, 0, len(order))
	for _, k := range order {
		result = append(result, map[string]any{
			"__group_by": true,
			"key":        k,
			"items":      groups[k],
		})
	}
	return result, nil
}

func transformSortBy(data any, op transformOp) (any, error) {
	if op.Key == "" {
		return nil, fmt.Errorf("sort_by requires 'key'")
	}
	arr, err := transformToArray(data)
	if err != nil {
		return nil, err
	}

	sorted := make([]any, len(arr))
	copy(sorted, arr)

	sort.SliceStable(sorted, func(i, j int) bool {
		vi := transformResolve(sorted[i], op.Key)
		vj := transformResolve(sorted[j], op.Key)
		cmp := transformCompare(vi, vj)
		if op.Desc {
			return cmp > 0
		}
		return cmp < 0
	})

	return sorted, nil
}

func transformFilter(data any, op transformOp) (any, error) {
	if op.Key == "" {
		return nil, fmt.Errorf("filter requires 'key'")
	}
	// Validate exactly one condition
	count := 0
	if op.Exists != nil {
		count++
	}
	if op.Eq != nil {
		count++
	}
	if op.Neq != nil {
		count++
	}
	if op.Contains != nil {
		count++
	}
	if count == 0 {
		return nil, fmt.Errorf("filter requires a condition (eq, neq, contains, or exists)")
	}
	if count > 1 {
		return nil, fmt.Errorf("filter requires exactly one condition, got %d", count)
	}

	arr, err := transformToArray(data)
	if err != nil {
		return nil, err
	}

	var result []any
	for _, item := range arr {
		val := transformResolve(item, op.Key)
		if transformMatchFilter(val, op) {
			result = append(result, item)
		}
	}
	if result == nil {
		result = []any{}
	}
	return result, nil
}

func transformMatchFilter(val any, op transformOp) bool {
	if op.Exists != nil {
		return (val != nil) == *op.Exists
	}
	if op.Eq != nil {
		return transformToString(val) == transformToString(op.Eq)
	}
	if op.Neq != nil {
		return transformToString(val) != transformToString(op.Neq)
	}
	return strings.Contains(transformToString(val), *op.Contains)
}

func transformCount(data any, op transformOp) (any, error) {
	if op.Key == "" {
		switch v := data.(type) {
		case []any:
			return float64(len(v)), nil
		case map[string]any:
			return float64(len(v)), nil
		default:
			return float64(1), nil
		}
	}

	arr, err := transformToArray(data)
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int)
	var order []string
	for _, item := range arr {
		val := transformResolve(item, op.Key)
		key := transformToString(val)
		if _, seen := counts[key]; !seen {
			order = append(order, key)
		}
		counts[key]++
	}

	result := make([]any, 0, len(order))
	for _, k := range order {
		result = append(result, map[string]any{
			"key":   k,
			"count": float64(counts[k]),
		})
	}
	return result, nil
}

func transformFlatten(data any) (any, error) {
	arr, err := transformToArray(data)
	if err != nil {
		return nil, err
	}

	var result []any
	for _, item := range arr {
		switch v := item.(type) {
		case []any:
			result = append(result, v...)
		case map[string]any:
			if _, tagged := v["__group_by"]; tagged {
				if items, ok := v["items"].([]any); ok {
					result = append(result, items...)
				}
			} else {
				result = append(result, v)
			}
		default:
			result = append(result, item)
		}
	}
	if result == nil {
		result = []any{}
	}
	return result, nil
}

func transformFormat(data any, op transformOp) (any, error) {
	if op.Template == "" {
		return nil, fmt.Errorf("format requires 'template'")
	}
	arr, err := transformToArray(data)
	if err != nil {
		return nil, err
	}

	result := make([]any, 0, len(arr))
	for _, item := range arr {
		line := transformRenderTemplate(op.Template, item)
		result = append(result, line)
	}
	return result, nil
}

func transformRenderTemplate(tmpl string, item any) string {
	var sb strings.Builder
	i := 0
	for i < len(tmpl) {
		switch {
		case tmpl[i] == '{' && i+1 < len(tmpl) && tmpl[i+1] == '{':
			sb.WriteByte('{')
			i += 2
		case tmpl[i] == '}' && i+1 < len(tmpl) && tmpl[i+1] == '}':
			sb.WriteByte('}')
			i += 2
		case tmpl[i] == '{':
			end := strings.IndexByte(tmpl[i+1:], '}')
			if end == -1 {
				sb.WriteByte('{')
				i++
				continue
			}
			field := tmpl[i+1 : i+1+end]
			val := transformResolve(item, field)
			sb.WriteString(transformToString(val))
			i = i + 1 + end + 1
		default:
			sb.WriteByte(tmpl[i])
			i++
		}
	}
	return sb.String()
}

// --- Helpers ---

func transformResolve(data any, path string) any {
	path = strings.TrimPrefix(path, ".")
	parts := strings.Split(path, ".")
	current := data
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = m[part]
		if !ok {
			return nil
		}
	}
	return current
}

func transformToArray(data any) ([]any, error) {
	arr, ok := data.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array, got %T", data)
	}
	return arr, nil
}

func transformToString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("%v", val)
		}
		return string(b)
	}
}

func transformCompare(a, b any) int {
	af, aOk := a.(float64)
	bf, bOk := b.(float64)
	if aOk && bOk {
		if af < bf {
			return -1
		}
		if af > bf {
			return 1
		}
		return 0
	}
	return strings.Compare(transformToString(a), transformToString(b))
}
