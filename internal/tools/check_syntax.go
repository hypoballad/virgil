package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	checkSyntaxTimeout        = 20 * time.Second
	checkSyntaxMaxOutputBytes = 64 * 1024
)

type checkerTool struct {
	name          string
	description   string
	workspaceRoot string
	language      string
}

type checkSyntaxArgs struct {
	Path    string `json:"path,omitempty"`
	Project string `json:"project,omitempty"`
}

type checkerCommand struct {
	name    string
	args    []string
	workDir string
	source  string
}

func NewCheckPythonSyntaxTool(workspaceRoot string) Tool {
	return &checkerTool{
		name:          "check_python_syntax",
		description:   "Check Python syntax for one .py file using py_compile. Use after editing Python before run_tests.",
		workspaceRoot: filepath.Clean(workspaceRoot),
		language:      "python",
	}
}

func NewCheckGoPackageTool(workspaceRoot string) Tool {
	return &checkerTool{
		name:          "check_go_package",
		description:   "Check a Go package quickly with `go test -run ^$`. Use after editing Go before run_tests.",
		workspaceRoot: filepath.Clean(workspaceRoot),
		language:      "go",
	}
}

func NewCheckJavaScriptSyntaxTool(workspaceRoot string) Tool {
	return &checkerTool{
		name:          "check_javascript_syntax",
		description:   "Check JavaScript syntax for one .js/.mjs/.cjs file using `node --check`. Use after editing JavaScript before run_tests.",
		workspaceRoot: filepath.Clean(workspaceRoot),
		language:      "javascript",
	}
}

func NewCheckTypeScriptTool(workspaceRoot string) Tool {
	return &checkerTool{
		name:          "check_typescript",
		description:   "Check TypeScript with `tsc --noEmit --pretty false`. Prefer project mode for tsconfig projects. Use after editing TypeScript before run_tests.",
		workspaceRoot: filepath.Clean(workspaceRoot),
		language:      "typescript",
	}
}

func (t *checkerTool) Name() string {
	return t.name
}

func (t *checkerTool) IsMutating() bool {
	return false
}

func (t *checkerTool) Definition() ToolDefinition {
	properties := map[string]interface{}{
		"path": map[string]interface{}{
			"type":        "string",
			"description": "Target file, package directory, or project-relative path. Defaults depend on the checker.",
		},
	}
	required := []string{}
	if t.language == "python" || t.language == "javascript" {
		required = append(required, "path")
	}
	if t.language == "typescript" {
		properties["project"] = map[string]interface{}{
			"type":        "string",
			"description": "Optional tsconfig.json path. When omitted, the nearest tsconfig.json is used if found.",
		}
	}

	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        t.name,
			Description: t.description,
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": properties,
				"required":   required,
			},
		},
	}
}

func (t *checkerTool) Execute(ctx context.Context, rawArgs json.RawMessage) (*Result, error) {
	var args checkSyntaxArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
	}

	spec, unavailable, err := t.command(args)
	if unavailable != "" {
		return t.unavailableResult(unavailable), nil
	}
	if err != nil {
		return ErrorResult(err.Error()), nil
	}
	return t.execute(ctx, spec)
}

func (t *checkerTool) command(args checkSyntaxArgs) (checkerCommand, string, error) {
	switch t.language {
	case "python":
		target, err := t.resolvePath(args.Path)
		if err != nil {
			return checkerCommand{}, "", err
		}
		if filepath.Ext(target) != ".py" {
			return checkerCommand{}, "", fmt.Errorf("check_python_syntax requires a .py file, got %s", args.Path)
		}
		bin, tried := findBinary("VIRGIL_PYTHON_BIN", []string{"python3", "python"})
		if bin == "" {
			return checkerCommand{}, unavailableMessage("python checker not found", tried), nil
		}
		rel, _ := filepath.Rel(t.workspaceRoot, target)
		return checkerCommand{name: bin, args: []string{"-m", "py_compile", filepath.ToSlash(rel)}, workDir: t.workspaceRoot, source: "python"}, "", nil

	case "go":
		target := args.Path
		if strings.TrimSpace(target) == "" {
			target = "."
		}
		targetPath, err := t.resolvePath(target)
		if err != nil {
			return checkerCommand{}, "", err
		}
		targetDir := targetPath
		if info, err := os.Stat(targetPath); err == nil && !info.IsDir() {
			targetDir = filepath.Dir(targetPath)
		}
		moduleRoot, ok := findUpward(targetDir, t.workspaceRoot, "go.mod")
		if !ok {
			return checkerCommand{}, "", fmt.Errorf("go.mod not found for path %s", target)
		}
		bin, tried := findBinary("VIRGIL_GO_BIN", []string{"go"})
		if bin == "" {
			return checkerCommand{}, unavailableMessage("go checker not found", tried), nil
		}
		rel, err := filepath.Rel(moduleRoot, targetDir)
		if err != nil {
			return checkerCommand{}, "", fmt.Errorf("failed to resolve Go package path: %v", err)
		}
		pkg := "."
		if rel != "." {
			pkg = "./" + filepath.ToSlash(rel)
		}
		return checkerCommand{name: bin, args: []string{"test", "-run", "^$", pkg}, workDir: moduleRoot, source: "go"}, "", nil

	case "javascript":
		target, err := t.resolvePath(args.Path)
		if err != nil {
			return checkerCommand{}, "", err
		}
		ext := filepath.Ext(target)
		if ext != ".js" && ext != ".mjs" && ext != ".cjs" {
			return checkerCommand{}, "", fmt.Errorf("check_javascript_syntax requires a .js, .mjs, or .cjs file, got %s", args.Path)
		}
		bin, tried := findBinary("VIRGIL_NODE_BIN", []string{"node"})
		if bin == "" {
			return checkerCommand{}, unavailableMessage("javascript checker not found", tried), nil
		}
		rel, _ := filepath.Rel(t.workspaceRoot, target)
		return checkerCommand{name: bin, args: []string{"--check", filepath.ToSlash(rel)}, workDir: t.workspaceRoot, source: "node"}, "", nil

	case "typescript":
		target := args.Path
		if strings.TrimSpace(target) == "" {
			target = "."
		}
		targetPath, err := t.resolvePath(target)
		if err != nil {
			return checkerCommand{}, "", err
		}
		targetDir := targetPath
		if info, err := os.Stat(targetPath); err == nil && !info.IsDir() {
			targetDir = filepath.Dir(targetPath)
		}
		project := strings.TrimSpace(args.Project)
		if project == "" {
			if root, ok := findUpward(targetDir, t.workspaceRoot, "tsconfig.json"); ok {
				project = filepath.Join(root, "tsconfig.json")
			}
		} else {
			project, err = t.resolvePath(project)
			if err != nil {
				return checkerCommand{}, "", err
			}
		}
		spec, tried := t.findTSC(targetDir)
		if spec.name == "" {
			return checkerCommand{}, unavailableMessage("typescript checker not found", tried), nil
		}
		if project != "" {
			spec.args = append(spec.args, "--noEmit", "--pretty", "false", "--project", project)
		} else {
			rel, _ := filepath.Rel(t.workspaceRoot, targetPath)
			spec.args = append(spec.args, "--noEmit", "--pretty", "false", filepath.ToSlash(rel))
			spec.workDir = t.workspaceRoot
		}
		return spec, "", nil
	default:
		return checkerCommand{}, "", fmt.Errorf("unsupported checker language %s", t.language)
	}
}

func (t *checkerTool) findTSC(startDir string) (checkerCommand, []string) {
	tried := []string{}
	if env := strings.TrimSpace(os.Getenv("VIRGIL_TSC_BIN")); env != "" {
		tried = append(tried, "VIRGIL_TSC_BIN="+env)
		if isExecutable(env) {
			return checkerCommand{name: env, source: "VIRGIL_TSC_BIN", workDir: t.workspaceRoot}, tried
		}
	}
	if packageRoot, ok := findUpward(startDir, t.workspaceRoot, "package.json"); ok {
		local := filepath.Join(packageRoot, "node_modules", ".bin", "tsc")
		tried = append(tried, local)
		if isExecutable(local) {
			return checkerCommand{name: local, source: "node_modules/.bin/tsc", workDir: packageRoot}, tried
		}
	}
	tried = append(tried, "PATH: tsc")
	if bin, err := exec.LookPath("tsc"); err == nil {
		return checkerCommand{name: bin, source: "PATH: tsc", workDir: t.workspaceRoot}, tried
	}
	tried = append(tried, "PATH: npx --no-install tsc")
	if bin, err := exec.LookPath("npx"); err == nil {
		return checkerCommand{name: bin, args: []string{"--no-install", "tsc"}, source: "PATH: npx", workDir: t.workspaceRoot}, tried
	}
	return checkerCommand{}, tried
}

func (t *checkerTool) resolvePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs = filepath.Clean(filepath.Join(t.workspaceRoot, path))
	}
	rel, err := filepath.Rel(t.workspaceRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path outside workspace: %s", path)
	}
	return abs, nil
}

func (t *checkerTool) execute(ctx context.Context, spec checkerCommand) (*Result, error) {
	execCtx, cancel := context.WithTimeout(ctx, checkSyntaxTimeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, spec.name, spec.args...)
	cmd.Dir = spec.workDir
	output, err := cmd.CombinedOutput()
	timedOut := execCtx.Err() == context.DeadlineExceeded
	if timedOut {
		err = execCtx.Err()
	}

	limitedOutput, truncated := tailOutput(output, checkSyntaxMaxOutputBytes)
	exitCode := 0
	if err != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	if err != nil && spec.source == "PATH: npx" && looksLikeNpxUnavailable(limitedOutput) {
		return t.unavailableResult("typescript checker not found via npx --no-install tsc"), nil
	}

	content := t.formatResult(spec, limitedOutput, exitCode, timedOut)
	metadata := map[string]interface{}{
		"language":  t.language,
		"command":   shellQuoteCommand(spec.name, spec.args),
		"work_dir":  spec.workDir,
		"source":    spec.source,
		"exit_code": exitCode,
		"truncated": truncated,
		"timeout":   timedOut,
	}
	if err != nil {
		return &Result{IsError: true, Content: content, Metadata: metadata}, nil
	}
	return &Result{IsError: false, Content: content, Metadata: metadata}, nil
}

func (t *checkerTool) formatResult(spec checkerCommand, output string, exitCode int, timedOut bool) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("$ %s\n", shellQuoteCommand(spec.name, spec.args)))
	sb.WriteString(fmt.Sprintf("(language: %s, in %s, source: %s)\n\n", t.language, spec.workDir, spec.source))
	if strings.TrimSpace(output) != "" {
		sb.WriteString(output)
		if !strings.HasSuffix(output, "\n") {
			sb.WriteString("\n")
		}
	} else if exitCode == 0 {
		sb.WriteString("Syntax check OK\n")
	}
	if timedOut {
		sb.WriteString(fmt.Sprintf("[timeout after %v]\n", checkSyntaxTimeout))
	}
	sb.WriteString(fmt.Sprintf("[exit code: %d]", exitCode))
	return sb.String()
}

func (t *checkerTool) unavailableResult(message string) *Result {
	content := fmt.Sprintf("%s.\nThis checker will be hidden for the rest of this run.", message)
	return &Result{
		IsError: true,
		Content: content,
		Metadata: map[string]interface{}{
			"checker_unavailable": true,
			"tool":                t.name,
			"language":            t.language,
		},
	}
}

func findBinary(envName string, names []string) (string, []string) {
	tried := []string{}
	if env := strings.TrimSpace(os.Getenv(envName)); env != "" {
		tried = append(tried, envName+"="+env)
		if isExecutable(env) {
			return env, tried
		}
	}
	for _, name := range names {
		tried = append(tried, "PATH: "+name)
		if bin, err := exec.LookPath(name); err == nil {
			return bin, tried
		}
	}
	return "", tried
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0111 != 0
}

func unavailableMessage(prefix string, tried []string) string {
	var sb strings.Builder
	sb.WriteString(prefix)
	if len(tried) > 0 {
		sb.WriteString(". Tried:")
		for _, item := range tried {
			sb.WriteString("\n- ")
			sb.WriteString(item)
		}
	}
	return sb.String()
}

func looksLikeNpxUnavailable(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "could not determine executable") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "missing script") ||
		strings.Contains(lower, "not installed")
}
