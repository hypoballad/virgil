package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hypoballad/virgil/internal/repository"
)

const getCallersDefaultLimit = 30
const getCallersMaxLimit = 100

// GetCallersTool は逆引きで関数の呼び出し元を返す。
type GetCallersTool struct {
	calls *repository.CallRepository
}

func NewGetCallersTool(calls *repository.CallRepository) *GetCallersTool {
	return &GetCallersTool{calls: calls}
}

func (t *GetCallersTool) Name() string {
	return "get_callers"
}

func (t *GetCallersTool) Description() string {
	return "Find all places that call a given function or method (reverse lookup). " +
		"Use this BEFORE modifying a function to understand its impact scope. " +
		"Returns file paths, line numbers, and the calling function/method. " +
		"Currently indexed: Go (.go) and Python (.py) files."
}

func (t *GetCallersTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		},
	}
}

func (t *GetCallersTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "The function or method name to find callers for (exact match)",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of results (default 30, max 100)",
			},
		},
		"required": []string{"name"},
	}
}

func (t *GetCallersTool) IsMutating() bool {
	return false
}

type getCallersArgs struct {
	Name  string `json:"name"`
	Limit int    `json:"limit,omitempty"`
}

func (t *GetCallersTool) Execute(ctx context.Context, argsJSON json.RawMessage) (*Result, error) {
	var args getCallersArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	name := strings.TrimSpace(args.Name)
	if name == "" {
		return ErrorResult("name is required"), nil
	}
	if t.calls == nil {
		return ErrorResult("call graph repository is not available"), nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = getCallersDefaultLimit
	}
	if limit > getCallersMaxLimit {
		limit = getCallersMaxLimit
	}

	records, err := t.calls.FindIncoming(name, limit)
	if err != nil {
		return ErrorResult(fmt.Sprintf("query failed: %v", err)), nil
	}

	return &Result{
		IsError: false,
		Content: FormatCallersResult(name, records, limit),
	}, nil
}

// FormatCallersResult は呼び出し元一覧を Markdown で整形する。
func FormatCallersResult(name string, records []repository.CallRecord, limit int) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Callers of `%s`\n\n", name))

	if len(records) == 0 {
		sb.WriteString("No callers found.\n\n")
		sb.WriteString("Possible reasons:\n")
		sb.WriteString("- The function is not called anywhere\n")
		sb.WriteString("- The function name is misspelled\n")
		sb.WriteString("- The function is called dynamically (e.g., reflection, interface methods in non-indexed languages)\n")
		sb.WriteString("- Indexed languages are limited to Go and Python\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d caller(s)", len(records)))
	if len(records) == limit {
		sb.WriteString(fmt.Sprintf(" (limited to %d, may have more)", limit))
	}
	sb.WriteString("\n\n")

	sb.WriteString("| File | Line | Caller | Receiver |\n")
	sb.WriteString("|------|------|--------|----------|\n")

	for _, r := range records {
		receiver := r.CallerReceiver
		if receiver == "" {
			receiver = "-"
		}
		caller := r.CallerName
		if caller == "<global>" {
			caller = "_(global init)_"
		}
		sb.WriteString(fmt.Sprintf("| `%s` | %d | `%s` | %s |\n",
			r.CallerFile, r.CallLine, caller, receiver))
	}

	sb.WriteString("\n")
	sb.WriteString("**Next steps:**\n")
	sb.WriteString("- To see how a caller uses this function: `read_file(path=\"FILE\", start_line=START_LINE, end_line=END_LINE)`\n")
	sb.WriteString("- To see the call graph from a caller: `get_call_graph(name=\"CALLER_NAME\")`\n")

	return sb.String()
}
