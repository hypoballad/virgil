package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hypoballad/virgil/internal/tools/jsonutil"
)

const (
	maxJSONFileSize         = 50 * 1024 * 1024
	readJSONPathMaxBytes    = 40 * 1024
	defaultJSONOutlineDepth = 2
)

type GetJSONOutlineTool struct {
	AllowedRoot string
}

type ReadJSONPathTool struct {
	AllowedRoot string
}

type getJSONOutlineArgs struct {
	Path     string `json:"path"`
	MaxDepth int    `json:"max_depth,omitempty"`
}

type readJSONPathArgs struct {
	Path     string `json:"path"`
	JSONPath string `json:"jsonpath"`
}

func NewGetJSONOutlineTool(allowedRoot string) *GetJSONOutlineTool {
	return &GetJSONOutlineTool{AllowedRoot: allowedRoot}
}

func NewReadJSONPathTool(allowedRoot string) *ReadJSONPathTool {
	return &ReadJSONPathTool{AllowedRoot: allowedRoot}
}

func (t *GetJSONOutlineTool) Name() string { return "get_json_outline" }
func (t *ReadJSONPathTool) Name() string   { return "read_json_path" }

func (t *GetJSONOutlineTool) IsMutating() bool { return false }
func (t *ReadJSONPathTool) IsMutating() bool   { return false }

func (t *GetJSONOutlineTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "get_json_outline",
			Description: "Inspect a .json file without loading it into context. Returns file size, estimated tokens, and key/type structure up to max_depth. Use this before read_file for JSON, especially large JSON files.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to a .json file, relative to workspace root or absolute under the workspace.",
					},
					"max_depth": map[string]interface{}{
						"type":        "integer",
						"description": "Optional. Structure depth to show. Default 2.",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (t *ReadJSONPathTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "read_json_path",
			Description: "Read a focused part of a .json file using JSONPath. Supports $, .key, [index], [*], and [start:end]. Use this instead of read_file when you only need part of a JSON document.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to a .json file, relative to workspace root or absolute under the workspace.",
					},
					"jsonpath": map[string]interface{}{
						"type":        "string",
						"description": "JSONPath expression. Examples: $.users[0], $.users[0:10], $.metadata.version",
					},
				},
				"required": []string{"path", "jsonpath"},
			},
		},
	}
}

func (t *GetJSONOutlineTool) Execute(ctx context.Context, rawArgs json.RawMessage) (*Result, error) {
	var args getJSONOutlineArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}
	cleanPath, data, errMsg := readJSONDocument(t.AllowedRoot, args.Path)
	if errMsg != "" {
		return ErrorResult(errMsg), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	maxDepth := args.MaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultJSONOutlineDepth
	}
	if maxDepth > 8 {
		maxDepth = 8
	}

	var parsed interface{}
	if err := decodeJSON(data, &parsed); err != nil {
		return ErrorResult("invalid JSON: " + formatJSONError(data, err)), nil
	}

	entries := jsonutil.AnalyzeStructure(parsed, maxDepth)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("File: %s\n", displayPath(t.AllowedRoot, cleanPath)))
	sb.WriteString(fmt.Sprintf("Size: %s (%d bytes, estimated %d tokens)\n\n", humanBytes(len(data)), len(data), jsonutil.EstimateTokens(len(data))))
	sb.WriteString("Top-level structure:\n")
	for _, entry := range entries {
		if outlineDepth(entry.Path) == 1 {
			sb.WriteString(fmt.Sprintf("%s: %s\n", entry.Path, entry.Description))
		}
	}
	sb.WriteString(fmt.Sprintf("\nDetailed structure (depth=%d):\n", maxDepth))
	for _, entry := range entries {
		sb.WriteString(fmt.Sprintf("%s: %s\n", entry.Path, entry.Description))
	}
	if len(entries) == 0 {
		sb.WriteString("$: " + describeScalar(parsed) + "\n")
	}

	res := SuccessResult(sb.String())
	res.Metadata = map[string]interface{}{
		"path":       args.Path,
		"size_bytes": len(data),
		"max_depth":  maxDepth,
		"entries":    len(entries),
	}
	return res, nil
}

func (t *ReadJSONPathTool) Execute(ctx context.Context, rawArgs json.RawMessage) (*Result, error) {
	var args readJSONPathArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}
	if strings.TrimSpace(args.JSONPath) == "" {
		return ErrorResult("jsonpath is required"), nil
	}
	_, data, errMsg := readJSONDocument(t.AllowedRoot, args.Path)
	if errMsg != "" {
		return ErrorResult(errMsg), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var parsed interface{}
	if err := decodeJSON(data, &parsed); err != nil {
		return ErrorResult("invalid JSON: " + formatJSONError(data, err)), nil
	}
	value, err := jsonutil.QueryJSONPath(parsed, args.JSONPath)
	if err != nil {
		if errors.Is(err, jsonutil.ErrPathNotFound) {
			return ErrorResult(fmt.Sprintf("path not found: %s", args.JSONPath)), nil
		}
		return ErrorResult(fmt.Sprintf("invalid JSONPath: %v", err)), nil
	}
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to format JSON result: %v", err)), nil
	}
	totalSize := len(out)
	truncated := false
	if len(out) > readJSONPathMaxBytes {
		truncated = true
		out = out[:readJSONPathMaxBytes]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Path: %s\n\nResult:\n", args.JSONPath))
	sb.Write(out)
	if truncated {
		sb.WriteString(fmt.Sprintf("\n\n[truncated, total size: %d bytes, showing first %d bytes]\n", totalSize, readJSONPathMaxBytes))
		sb.WriteString("Use a more specific JSONPath like $.users[0:10] or $.users[0].profile.")
	}
	res := SuccessResult(sb.String())
	res.Metadata = map[string]interface{}{
		"path":        args.Path,
		"jsonpath":    args.JSONPath,
		"size_bytes":  totalSize,
		"truncated":   truncated,
		"limit_bytes": readJSONPathMaxBytes,
	}
	return res, nil
}

func readJSONDocument(root, requestedPath string) (string, []byte, string) {
	if strings.TrimSpace(requestedPath) == "" {
		return "", nil, "path is required"
	}
	cleanPath, err := resolveWorkspacePath(root, requestedPath)
	if err != nil {
		return "", nil, err.Error()
	}
	if strings.ToLower(filepath.Ext(cleanPath)) != ".json" {
		return "", nil, fmt.Sprintf("not a JSON file: %s. Use read_file for non-JSON files.", requestedPath)
	}
	info, err := os.Stat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, formatPathError(root, requestedPath, cleanPath)
		}
		return "", nil, fmt.Sprintf("stat error: %v", err)
	}
	if info.IsDir() {
		return "", nil, fmt.Sprintf("path is a directory, not a file: %s", requestedPath)
	}
	if info.Size() == 0 {
		return "", nil, fmt.Sprintf("empty file: %s", requestedPath)
	}
	if info.Size() > maxJSONFileSize {
		return "", nil, fmt.Sprintf("JSON file too large: %d bytes (max %d)", info.Size(), maxJSONFileSize)
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return "", nil, fmt.Sprintf("read error: %v", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return "", nil, fmt.Sprintf("empty file: %s", requestedPath)
	}
	return cleanPath, data, ""
}

func resolveWorkspacePath(root, requestedPath string) (string, error) {
	var abs string
	var err error
	if filepath.IsAbs(requestedPath) {
		abs = filepath.Clean(requestedPath)
	} else {
		abs, err = filepath.Abs(filepath.Join(root, requestedPath))
		if err != nil {
			return "", fmt.Errorf("failed to resolve path: %v", err)
		}
	}
	if root != "" {
		relPath, err := filepath.Rel(root, abs)
		if err != nil || strings.HasPrefix(relPath, "..") {
			return "", fmt.Errorf("path outside allowed root: %s", requestedPath)
		}
	}
	return abs, nil
}

func decodeJSON(data []byte, out *interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(out)
}

func formatJSONError(data []byte, err error) string {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		line, col := lineColumnForOffset(data, syntaxErr.Offset)
		return fmt.Sprintf("%v at line %d, column %d", err, line, col)
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		line, col := lineColumnForOffset(data, int64(len(data)+1))
		return fmt.Sprintf("%v at line %d, column %d", err, line, col)
	}
	return err.Error()
}

func lineColumnForOffset(data []byte, offset int64) (int, int) {
	if offset < 1 {
		return 1, 1
	}
	line, col := 1, 1
	for i := int64(0); i < offset-1 && i < int64(len(data)); i++ {
		if data[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func humanBytes(size int) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := float64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/div, "KMGTPE"[exp])
}

func displayPath(root, path string) string {
	if root == "" {
		return path
	}
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func outlineDepth(path string) int {
	if path == "$" {
		return 0
	}
	depth := 0
	for i := 1; i < len(path); i++ {
		if path[i] == '.' || path[i] == '[' {
			depth++
		}
	}
	return depth
}

func describeScalar(value interface{}) string {
	switch value.(type) {
	case string:
		return "string"
	case json.Number, float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return "unknown"
	}
}
