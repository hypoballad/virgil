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
	runTestsTimeout        = 60 * time.Second
	runTestsMaxOutputBytes = 100 * 1024
	runTestsFailurePrompt  = "Tests failed. Please analyze the error output above, identify the root cause, and use edit_with_pattern to fix the code. DO NOT stop until tests pass."
)

type RunTestsTool struct {
	workspaceRoot string
	timeout       time.Duration
	maxOutput     int
}

type runTestsArgs struct {
	Path     string `json:"path,omitempty"`
	Language string `json:"language,omitempty"`
}

type testCommand struct {
	language string
	name     string
	args     []string
	workDir  string
}

func NewRunTestsTool(workspaceRoot string) *RunTestsTool {
	return &RunTestsTool{
		workspaceRoot: filepath.Clean(workspaceRoot),
		timeout:       runTestsTimeout,
		maxOutput:     runTestsMaxOutputBytes,
	}
}

func (t *RunTestsTool) Name() string {
	return "run_tests"
}

func (t *RunTestsTool) IsMutating() bool {
	return false
}

func (t *RunTestsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        "run_tests",
			Description: "Run unit tests for Go, Python, JS/TS, or Rust. ALWAYS use this tool after modifying code to verify changes. Supports automatic language detection. If tests fail, analyze the tail of the output to find the cause.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Optional path to test. Defaults to '.'.",
					},
					"language": map[string]interface{}{
						"type":        "string",
						"description": "Optional language override: go, python, node, or rust.",
						"enum":        []string{"go", "python", "node", "rust"},
					},
				},
			},
		},
	}
}

func (t *RunTestsTool) Execute(ctx context.Context, rawArgs json.RawMessage) (*Result, error) {
	var args runTestsArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
	}
	if strings.TrimSpace(args.Path) == "" {
		args.Path = "."
	}

	targetPath, err := t.resolvePath(args.Path)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}

	cmdSpec, err := t.selectCommand(targetPath, strings.TrimSpace(strings.ToLower(args.Language)))
	if err != nil {
		return ErrorResult(err.Error()), nil
	}

	return t.execute(ctx, cmdSpec)
}

func (t *RunTestsTool) resolvePath(path string) (string, error) {
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

func (t *RunTestsTool) selectCommand(targetPath string, language string) (testCommand, error) {
	targetDir := targetPath
	if info, err := os.Stat(targetPath); err == nil && !info.IsDir() {
		targetDir = filepath.Dir(targetPath)
	}

	if language != "" {
		switch language {
		case "go":
			return t.goTestCommand(targetPath, targetDir)
		case "node":
			return t.nodeTestCommand(targetDir)
		case "rust":
			return t.rustTestCommand(targetDir)
		case "python":
			return t.pythonTestCommand(targetPath)
		default:
			return testCommand{}, fmt.Errorf("unsupported language %q (expected go, python, node, or rust)", language)
		}
	}

	if moduleRoot, ok := findUpward(targetDir, t.workspaceRoot, "go.mod"); ok {
		return t.goTestCommandAt(targetPath, moduleRoot)
	}
	if packageRoot, ok := findUpward(targetDir, t.workspaceRoot, "package.json"); ok {
		return testCommand{language: "node", name: "npm", args: []string{"test"}, workDir: packageRoot}, nil
	}
	if cargoRoot, ok := findUpward(targetDir, t.workspaceRoot, "Cargo.toml"); ok {
		return testCommand{language: "rust", name: "cargo", args: []string{"test"}, workDir: cargoRoot}, nil
	}
	if hasPythonFiles(targetPath) {
		return t.pythonTestCommand(targetPath)
	}

	return testCommand{}, fmt.Errorf("could not detect test framework for %s; pass language as go, python, node, or rust", targetPath)
}

func (t *RunTestsTool) goTestCommand(targetPath, targetDir string) (testCommand, error) {
	moduleRoot, ok := findUpward(targetDir, t.workspaceRoot, "go.mod")
	if !ok {
		return testCommand{}, fmt.Errorf("go.mod not found for path %s", targetPath)
	}
	return t.goTestCommandAt(targetPath, moduleRoot)
}

func (t *RunTestsTool) goTestCommandAt(targetPath, moduleRoot string) (testCommand, error) {
	testPath := targetPath
	if info, err := os.Stat(testPath); err == nil && !info.IsDir() {
		testPath = filepath.Dir(testPath)
	}
	rel, err := filepath.Rel(moduleRoot, testPath)
	if err != nil {
		return testCommand{}, fmt.Errorf("failed to resolve Go test path: %v", err)
	}
	arg := "."
	if rel != "." {
		arg = "./" + filepath.ToSlash(rel)
	}
	return testCommand{language: "go", name: "go", args: []string{"test", "-v", arg}, workDir: moduleRoot}, nil
}

func (t *RunTestsTool) nodeTestCommand(targetDir string) (testCommand, error) {
	packageRoot, ok := findUpward(targetDir, t.workspaceRoot, "package.json")
	if !ok {
		return testCommand{}, fmt.Errorf("package.json not found for path %s", targetDir)
	}
	return testCommand{language: "node", name: "npm", args: []string{"test"}, workDir: packageRoot}, nil
}

func (t *RunTestsTool) rustTestCommand(targetDir string) (testCommand, error) {
	cargoRoot, ok := findUpward(targetDir, t.workspaceRoot, "Cargo.toml")
	if !ok {
		return testCommand{}, fmt.Errorf("Cargo.toml not found for path %s", targetDir)
	}
	return testCommand{language: "rust", name: "cargo", args: []string{"test"}, workDir: cargoRoot}, nil
}

func (t *RunTestsTool) pythonTestCommand(targetPath string) (testCommand, error) {
	testPath := targetPath
	if rel, err := filepath.Rel(t.workspaceRoot, targetPath); err == nil && rel != "." {
		testPath = filepath.ToSlash(rel)
	}
	if _, err := exec.LookPath("pytest"); err == nil {
		return testCommand{language: "python", name: "pytest", args: []string{"-v", testPath}, workDir: t.workspaceRoot}, nil
	}
	return testCommand{language: "python", name: "python", args: []string{"-m", "unittest", "discover", testPath}, workDir: t.workspaceRoot}, nil
}

func (t *RunTestsTool) execute(ctx context.Context, spec testCommand) (*Result, error) {
	execCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, spec.name, spec.args...)
	cmd.Dir = spec.workDir

	output, err := cmd.CombinedOutput()
	timedOut := execCtx.Err() == context.DeadlineExceeded
	if timedOut {
		err = execCtx.Err()
	}

	limitedOutput, truncated := tailOutput(output, t.maxOutput)
	exitCode := 0
	if err != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("$ %s\n", shellQuoteCommand(spec.name, spec.args)))
	sb.WriteString(fmt.Sprintf("(language: %s, in %s)\n\n", spec.language, spec.workDir))
	sb.WriteString(limitedOutput)
	if timedOut {
		sb.WriteString(fmt.Sprintf("\n[timeout after %v]", t.timeout))
	}
	sb.WriteString(fmt.Sprintf("\n[exit code: %d]", exitCode))

	if err != nil {
		if summary := summarizeTestFailure(limitedOutput, spec.language, 8); summary != "" {
			sb.WriteString("\n\n")
			sb.WriteString(summary)
		}
		sb.WriteString("\n\n")
		sb.WriteString(runTestsFailurePrompt)
		return &Result{
			IsError: true,
			Content: sb.String(),
			Metadata: map[string]interface{}{
				"language":  spec.language,
				"command":   shellQuoteCommand(spec.name, spec.args),
				"work_dir":  spec.workDir,
				"exit_code": exitCode,
				"truncated": truncated,
				"timeout":   timedOut,
			},
		}, nil
	}

	return &Result{
		IsError: false,
		Content: sb.String(),
		Metadata: map[string]interface{}{
			"language":  spec.language,
			"command":   shellQuoteCommand(spec.name, spec.args),
			"work_dir":  spec.workDir,
			"exit_code": exitCode,
			"truncated": truncated,
			"timeout":   false,
		},
	}, nil
}

func findUpward(startDir, stopDir, filename string) (string, bool) {
	dir := filepath.Clean(startDir)
	stopDir = filepath.Clean(stopDir)
	for {
		if _, err := os.Stat(filepath.Join(dir, filename)); err == nil {
			return dir, true
		}
		if dir == stopDir || dir == filepath.Dir(dir) {
			return "", false
		}
		dir = filepath.Dir(dir)
	}
}

func hasPythonFiles(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return filepath.Ext(path) == ".py"
	}

	found := false
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "__pycache__" || name == "venv" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) == ".py" {
			found = true
		}
		return nil
	})
	return found
}

func tailOutput(output []byte, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(output) <= maxBytes {
		return string(output), false
	}
	tail := output[len(output)-maxBytes:]
	return fmt.Sprintf("... [output truncated, showing last %d bytes for error analysis] ...\n%s", maxBytes, string(tail)), true
}

func shellQuoteCommand(name string, args []string) string {
	parts := append([]string{name}, args...)
	for i, part := range parts {
		if strings.ContainsAny(part, " \t\n\"'\\$`") {
			parts[i] = "'" + strings.ReplaceAll(part, "'", "'\\''") + "'"
		}
	}
	return strings.Join(parts, " ")
}
