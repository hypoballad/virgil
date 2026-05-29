package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hypoballad/virgil/internal/shadow"
)

const defaultDiffSummaryMaxLines = 160

type GetDiffSummaryTool struct {
	shadow *shadow.ShadowRepo
}

func NewGetDiffSummaryTool(repo *shadow.ShadowRepo) *GetDiffSummaryTool {
	return &GetDiffSummaryTool{shadow: repo}
}

func (t *GetDiffSummaryTool) Name() string {
	return "get_diff_summary"
}

func (t *GetDiffSummaryTool) IsMutating() bool {
	return false
}

func (t *GetDiffSummaryTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        t.Name(),
			Description: "Summarize recent file edits from Virgil shadow git. Use after edits and before final reporting to verify what changed without reading full files. By default compares the latest pre-tool snapshot to current HEAD.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from_commit": map[string]interface{}{
						"type":        "string",
						"description": "Optional shadow commit hash to diff from. Defaults to the latest pre-tool commit.",
					},
					"to_commit": map[string]interface{}{
						"type":        "string",
						"description": "Optional shadow commit hash to diff to. Defaults to current HEAD.",
					},
					"max_lines": map[string]interface{}{
						"type":        "integer",
						"description": "Optional maximum diff lines. Default 160.",
					},
				},
			},
		},
	}
}

type getDiffSummaryArgs struct {
	FromCommit string `json:"from_commit,omitempty"`
	ToCommit   string `json:"to_commit,omitempty"`
	MaxLines   int    `json:"max_lines,omitempty"`
}

func (t *GetDiffSummaryTool) Execute(ctx context.Context, rawArgs json.RawMessage) (*Result, error) {
	if t.shadow == nil {
		return ErrorResult("shadow repository is not available"), nil
	}

	var args getDiffSummaryArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
	}

	maxLines := args.MaxLines
	if maxLines <= 0 {
		maxLines = defaultDiffSummaryMaxLines
	}

	toCommit := strings.TrimSpace(args.ToCommit)
	if toCommit == "" {
		head, err := t.shadow.HeadCommit(ctx)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to get shadow HEAD: %v", err)), nil
		}
		toCommit = head
	}

	fromCommit := strings.TrimSpace(args.FromCommit)
	if fromCommit == "" {
		commit, err := latestPreToolCommit(ctx, t.shadow)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to find latest pre-tool commit: %v", err)), nil
		}
		fromCommit = commit
	}
	if fromCommit == "" || toCommit == "" {
		return SuccessResult("No shadow commits available for diff summary."), nil
	}

	diff, err := t.shadow.Diff(ctx, fromCommit, toCommit, maxLines)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get diff: %v", err)), nil
	}
	if strings.TrimSpace(diff) == "" {
		return SuccessResult(fmt.Sprintf("No file changes between %s and %s.", shortCommit(fromCommit), shortCommit(toCommit))), nil
	}

	content := formatDiffSummary(fromCommit, toCommit, diff)
	return SuccessResult(content), nil
}

func latestPreToolCommit(ctx context.Context, repo *shadow.ShadowRepo) (string, error) {
	commits, err := repo.LogRecent(ctx, 50)
	if err != nil {
		return "", err
	}
	for _, commit := range commits {
		if strings.HasPrefix(commit.Message, shadow.PreCommitPrefix+":") {
			return commit.Hash, nil
		}
	}
	return "", nil
}

func formatDiffSummary(fromCommit, toCommit, diff string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Diff: %s..%s\n\n", shortCommit(fromCommit), shortCommit(toCommit)))
	sb.WriteString(summarizeDiffFiles(diff))
	sb.WriteString("\n```diff\n")
	sb.WriteString(diff)
	if !strings.HasSuffix(diff, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("```")
	return sb.String()
}

func summarizeDiffFiles(diff string) string {
	files := make([]string, 0)
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				files = append(files, strings.TrimPrefix(parts[3], "b/"))
			}
		}
	}
	if len(files) == 0 {
		return "Changed files: (unknown)\n"
	}
	var sb strings.Builder
	sb.WriteString("Changed files:\n")
	for _, file := range files {
		sb.WriteString("- ")
		sb.WriteString(file)
		sb.WriteString("\n")
	}
	return sb.String()
}

func shortCommit(hash string) string {
	if len(hash) <= 8 {
		return hash
	}
	return hash[:8]
}
