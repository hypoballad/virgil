package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hypoballad/virgil/internal/agent"
	"github.com/hypoballad/virgil/internal/db"
	"github.com/hypoballad/virgil/internal/llm"
	"github.com/hypoballad/virgil/internal/repository"
	"github.com/hypoballad/virgil/internal/shadow"
	"github.com/hypoballad/virgil/internal/symbols"
	"github.com/hypoballad/virgil/internal/tools"
	"github.com/hypoballad/virgil/internal/tui"
	"github.com/joho/godotenv"
)

var version string

func main() {
	if len(os.Args) > 1 && os.Args[1] == "tsdiag" {
		os.Exit(runTSDiagCommand(os.Args[2:]))
	}
	startupArgs, dangerousVMax, fullPowerCommands, resumeTarget, forceNewSession := parseStartupArgs(os.Args[1:])

	if err := godotenv.Overload(); err != nil {
		// Ignore if no .env found
	}
	toolProfile := resolveToolProfile(startupArgs)

	// 1. Workspace Root resolution (Determined first to set default paths)
	workspaceRoot := os.Getenv("VIRGIL_WORKSPACE")
	if workspaceRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Printf("Fatal: failed to get current directory: %v\n", err)
			os.Exit(1)
		}
		workspaceRoot = cwd
	}
	absWorkspace, err := filepath.Abs(workspaceRoot)
	if err == nil {
		workspaceRoot = absWorkspace
	}

	// 2. Default paths based on workspaceRoot
	virgilDir := filepath.Join(workspaceRoot, ".virgil")
	defaultDBPath := filepath.Join(virgilDir, "virgil.db")
	defaultLogPath := filepath.Join(virgilDir, "debug.log")

	// Debug logging
	if os.Getenv("DEBUG") != "" {
		_ = os.MkdirAll(virgilDir, 0755)
		f, err := tea.LogToFile(defaultLogPath, "debug")
		if err != nil {
			fmt.Println("fatal:", err)
			os.Exit(1)
		}
		defer f.Close()
		log.SetOutput(f)
		log.Printf("Virgil debug logging initialized in %s", defaultLogPath)
	}

	// Database initialization
	dbPath := os.Getenv("VIRGIL_DB_PATH")
	if dbPath == "" {
		dbPath = defaultDBPath
		_ = os.MkdirAll(virgilDir, 0755)
	}
	database, err := db.New(dbPath)
	if err != nil {
		fmt.Printf("Fatal: failed to initialize database at %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	defer database.Close()

	// Repository initialization
	repo := repository.New(database)

	// LLM Client
	var client agent.LLMClient
	var modelName string

	provider := os.Getenv("LLM_PROVIDER")
	if provider == "" {
		provider = "ollama"
	}

	if provider == "ollama" {
		modelName = os.Getenv("OLLAMA_MODEL")
		if modelName == "" {
			modelName = "qwen2.5-coder:7b"
		}
		ollamaHost := os.Getenv("OLLAMA_HOST")
		if ollamaHost == "" {
			ollamaHost = "http://127.0.0.1:11434"
		}

		client = &llm.OllamaClient{
			BaseURL: ollamaHost,
			Model:   modelName,
		}
	} else if provider == "openai" {
		modelName = os.Getenv("OPENAI_MODEL")
		if modelName == "" {
			modelName = "qwen"
		}
		apiBase := os.Getenv("OPENAI_API_BASE")
		if apiBase == "" {
			apiBase = "http://127.0.0.1:8081/v1"
		}
		apiKey := os.Getenv("OPENAI_API_KEY")
		openAIParams, err := loadOpenAIParametersFromEnv()
		if err != nil {
			fmt.Printf("Fatal: invalid OpenAI-compatible generation setting: %v\n", err)
			os.Exit(1)
		}

		client = &llm.OpenAIClient{
			BaseURL:          apiBase,
			Model:            modelName,
			APIKey:           apiKey,
			Temperature:      openAIParams.Temperature,
			TopP:             openAIParams.TopP,
			MaxTokens:        openAIParams.MaxTokens,
			PresencePenalty:  openAIParams.PresencePenalty,
			FrequencyPenalty: openAIParams.FrequencyPenalty,
			DisableStream:    openAIParams.DisableStream,
		}
	} else {
		fmt.Printf("Fatal: unknown LLM_PROVIDER: %s\n", provider)
		os.Exit(1)
	}

	// Tool Registry
	registry := tools.NewRegistry()

	// Register read_file tool
	readFileTool := tools.NewReadFileTool(workspaceRoot)
	if err := registry.Register(readFileTool); err != nil {
		log.Fatalf("failed to register read_file: %v", err)
	}

	// Register search_text tool
	searchTextTool := tools.NewSearchTextTool(workspaceRoot)
	if err := registry.Register(searchTextTool); err != nil {
		log.Fatalf("failed to register search_text: %v", err)
	}

	// Register list_files tool
	listFilesTool := tools.NewListFilesTool(workspaceRoot)
	if err := registry.Register(listFilesTool); err != nil {
		log.Fatalf("failed to register list_files: %v", err)
	}

	// Register write_file tool
	writeFileTool := tools.NewWriteFileTool(workspaceRoot)
	if err := registry.Register(writeFileTool); err != nil {
		log.Fatalf("failed to register write_file: %v", err)
	}

	// Register edit_file tool
	editFileTool := tools.NewEditFileTool(workspaceRoot)
	if err := registry.Register(editFileTool); err != nil {
		log.Fatalf("failed to register edit_file: %v", err)
	}

	// Register edit_with_pattern tool
	editWithPatternTool := tools.NewEditWithPatternTool(workspaceRoot)
	if err := registry.Register(editWithPatternTool); err != nil {
		log.Fatalf("failed to register edit_with_pattern: %v", err)
	}

	// Register run_command tool
	runConfig := loadRunCommandConfig()
	runConfig.WorkspaceRoot = workspaceRoot
	if err := registry.Register(tools.NewRunCommandTool(runConfig)); err != nil {
		log.Fatalf("failed to register run_command: %v", err)
	}

	// Register run_tests tool
	if err := registry.Register(tools.NewRunTestsTool(workspaceRoot)); err != nil {
		log.Fatalf("failed to register run_tests: %v", err)
	}
	for _, tool := range []tools.Tool{
		tools.NewCheckPythonSyntaxTool(workspaceRoot),
		tools.NewCheckGoPackageTool(workspaceRoot),
		tools.NewCheckJavaScriptSyntaxTool(workspaceRoot),
		tools.NewCheckTypeScriptTool(workspaceRoot),
	} {
		if err := registry.Register(tool); err != nil {
			log.Fatalf("failed to register %s: %v", tool.Name(), err)
		}
	}

	// Register get_file_outline tool
	if err := registry.Register(tools.NewGetFileOutlineTool(workspaceRoot)); err != nil {
		log.Fatalf("failed to register get_file_outline: %v", err)
	}

	// Register read_symbol tool
	if err := registry.Register(tools.NewReadSymbolTool(workspaceRoot)); err != nil {
		log.Fatalf("failed to register read_symbol: %v", err)
	}

	if err := registry.Register(tools.NewGetSymbolOutlineTool(workspaceRoot)); err != nil {
		log.Fatalf("failed to register get_symbol_outline: %v", err)
	}

	// Register find_symbol tool
	if err := registry.Register(tools.NewFindSymbolTool(repo.Symbols)); err != nil {
		log.Fatalf("failed to register find_symbol: %v", err)
	}

	if err := registry.Register(tools.NewGetCallersTool(repo.Calls)); err != nil {
		log.Fatalf("failed to register get_callers: %v", err)
	}

	if err := registry.Register(tools.NewGetCallGraphTool(repo.Calls)); err != nil {
		log.Fatalf("failed to register get_call_graph: %v", err)
	}

	if err := registry.Register(tools.NewGetFileImportsTool(workspaceRoot, repo.Imports, repo.Symbols)); err != nil {
		log.Fatalf("failed to register get_file_imports: %v", err)
	}

	if err := registry.Register(tools.NewGetJSONOutlineTool(workspaceRoot)); err != nil {
		log.Fatalf("failed to register get_json_outline: %v", err)
	}

	if err := registry.Register(tools.NewReadJSONPathTool(workspaceRoot)); err != nil {
		log.Fatalf("failed to register read_json_path: %v", err)
	}

	if err := registry.Register(tools.NewGetMarkdownOutlineTool(workspaceRoot)); err != nil {
		log.Fatalf("failed to register get_markdown_outline: %v", err)
	}

	if err := registry.Register(tools.NewReadMarkdownSectionTool(workspaceRoot)); err != nil {
		log.Fatalf("failed to register read_markdown_section: %v", err)
	}

	if err := registry.Register(tools.NewFindDependentsTool(repo.Imports)); err != nil {
		log.Fatalf("failed to register find_dependents: %v", err)
	}

	// Register fetch_docs tool
	if err := registry.Register(tools.NewFetchDocsTool()); err != nil {
		log.Fatalf("failed to register fetch_docs: %v", err)
	}

	// Initialize Indexer
	indexer := symbols.NewIndexer(workspaceRoot, repo.Symbols, repo.Calls)
	indexer.SetImportStore(repo.Imports)

	// Pass indexer to write_file and edit_file
	if t, ok := registry.Get("write_file"); ok {
		if wf, ok := t.(*tools.WriteFileTool); ok {
			wf.SetIndexer(indexer)
		}
	}
	if t, ok := registry.Get("edit_file"); ok {
		if ef, ok := t.(*tools.EditFileTool); ok {
			ef.SetIndexer(indexer)
		}
	}

	// Start background full scan
	indexerCtx, indexerCancel := context.WithCancel(context.Background())
	defer indexerCancel()
	indexer.StartFullScan(indexerCtx)

	// Agent initialization
	agentInst := agent.New(client, registry)
	agentInst.SetSystemPrompt(agent.SystemPromptWithAppendFromEnv(agent.SystemPromptDefault))
	agentInst.SetRepository(repo)
	agentInst.SetWorkspaceRoot(workspaceRoot)
	agentInst.SetToolProfile(toolProfile)
	agentInst.SetResponseLanguage(os.Getenv("VIRGIL_RESPONSE_LANGUAGE"))

	// Watchdog configuration
	watchdogConfig := agent.DefaultWatchdogConfig()

	if v := os.Getenv("VIRGIL_WATCHDOG_CONTEXT_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			watchdogConfig.ContextTokenLimit = n
			log.Printf("watchdog context limit set to %d", n)
		}
	}
	if v := os.Getenv("VIRGIL_WATCHDOG_MAX_REPEAT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			watchdogConfig.MaxRepeatCalls = n
		}
	}
	if v := os.Getenv("VIRGIL_WATCHDOG_MAX_EMPTY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			watchdogConfig.MaxEmptyResponses = n
		}
	}
	agentInst.SetWatchdogConfig(watchdogConfig)

	// ShadowRepo initialization
	shadowRepo, err := shadow.New(workspaceRoot)
	if err != nil {
		log.Fatalf("failed to create shadow repo: %v", err)
	}
	if err := shadowRepo.Init(context.Background()); err != nil {
		log.Fatalf("failed to init shadow repo: %v", err)
	}
	agentInst.SetShadowRepo(shadowRepo)

	if err := registry.Register(tools.NewGetDiffSummaryTool(shadowRepo)); err != nil {
		log.Fatalf("failed to register get_diff_summary: %v", err)
	}

	// Pass shadow repo to edit_with_pattern tool
	if t, ok := registry.Get("edit_with_pattern"); ok {
		if ep, ok := t.(*tools.EditWithPatternTool); ok {
			ep.SetIndexer(indexer)
			ep.SetShadowRepo(shadowRepo)
		}
	}

	// Session initialization
	session, resumedHistory, resumedTurnNumber, resumed, err := initializeSession(repo, modelName, workspaceRoot, resumeTarget, forceNewSession)
	if err != nil {
		fmt.Printf("Fatal: failed to initialize session: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if err := repo.Sessions.End(session.ID, "completed"); err != nil {
			log.Printf("Error ending session: %v", err)
		}
	}()

	// Timeout configuration
	agentTimeoutMinutes := 5
	if timeoutStr := os.Getenv("VIRGIL_AGENT_TIMEOUT_MINUTES"); timeoutStr != "" {
		if timeout, err := strconv.Atoi(timeoutStr); err == nil {
			agentTimeoutMinutes = timeout
		}
	}
	runTimeoutMinutes := 30
	if timeoutStr := os.Getenv("VIRGIL_RUN_TIMEOUT_MINUTES"); timeoutStr != "" {
		if timeout, err := strconv.Atoi(timeoutStr); err == nil {
			runTimeoutMinutes = timeout
		}
	}

	// TUI
	model := tui.NewModel(agentInst, repo, shadowRepo, indexer, session.ID, workspaceRoot, modelName, version, watchdogConfig.ContextTokenLimit, agentTimeoutMinutes, runTimeoutMinutes, repo.Calls)
	if resumed {
		model.SetInitialHistory(resumedHistory, resumedTurnNumber, fmt.Sprintf("↩ Resumed session %s with %d saved messages.", shortSessionID(session.ID), len(resumedHistory)))
	}
	model.SetVMaxAvailable(dangerousVMax)
	model.SetFullPowerCommands(fullPowerCommands)
	p := tea.NewProgram(
		model,
		// tea.WithAltScreen(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		os.Exit(1)
	}
}

type openAIParameters struct {
	Temperature      *float64
	TopP             *float64
	MaxTokens        *int
	PresencePenalty  *float64
	FrequencyPenalty *float64
	DisableStream    bool
}

func loadOpenAIParametersFromEnv() (openAIParameters, error) {
	var params openAIParameters
	var err error

	if params.Temperature, err = optionalFloatEnv("OPENAI_TEMPERATURE"); err != nil {
		return openAIParameters{}, err
	}
	if params.TopP, err = optionalFloatEnv("OPENAI_TOP_P"); err != nil {
		return openAIParameters{}, err
	}
	if params.MaxTokens, err = optionalIntEnv("OPENAI_MAX_TOKENS"); err != nil {
		return openAIParameters{}, err
	}
	if params.PresencePenalty, err = optionalFloatEnv("OPENAI_PRESENCE_PENALTY"); err != nil {
		return openAIParameters{}, err
	}
	if params.FrequencyPenalty, err = optionalFloatEnv("OPENAI_FREQUENCY_PENALTY"); err != nil {
		return openAIParameters{}, err
	}
	if params.DisableStream, err = optionalDisableStreamEnv("OPENAI_STREAM"); err != nil {
		return openAIParameters{}, err
	}

	return params, nil
}

func optionalDisableStreamEnv(name string) (bool, error) {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if raw == "" {
		return false, nil
	}
	switch raw {
	case "false", "0", "no", "off":
		return true, nil
	case "true", "1", "yes", "on":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}

func optionalFloatEnv(name string) (*float64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, fmt.Errorf("%s must be a number: %w", name, err)
	}
	return &value, nil
}

func optionalIntEnv(name string) (*int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return &value, nil
}

func initializeSession(repo *repository.Repository, modelName, workspaceRoot, resumeTarget string, forceNewSession bool) (*repository.Session, []llm.Message, int, bool, error) {
	if forceNewSession || strings.TrimSpace(resumeTarget) == "" {
		session, err := repo.Sessions.Create(modelName, workspaceRoot, "General Coding Task")
		return session, nil, 0, false, err
	}

	session, err := resolveResumeSession(repo, workspaceRoot, resumeTarget)
	if err != nil {
		return nil, nil, 0, false, err
	}
	history, err := repo.Turns.RebuildHistory(session.ID, 8)
	if err != nil {
		return nil, nil, 0, false, fmt.Errorf("rebuild history for session %s: %w", session.ID, err)
	}
	turns, err := repo.Turns.ListBySession(session.ID)
	if err != nil {
		return nil, nil, 0, false, fmt.Errorf("list turns for session %s: %w", session.ID, err)
	}
	turnNumber := 0
	if len(turns) > 0 {
		turnNumber = turns[len(turns)-1].TurnNumber
	}
	return session, history, turnNumber, true, nil
}

func resolveResumeSession(repo *repository.Repository, workspaceRoot string, target string) (*repository.Session, error) {
	target = strings.TrimSpace(target)
	if target == "" || strings.EqualFold(target, "latest") {
		sessions, err := repo.Sessions.ListRecent(50)
		if err != nil {
			return nil, fmt.Errorf("list recent sessions: %w", err)
		}
		for _, session := range sessions {
			if sameWorkspace(session.ProjectPath, workspaceRoot) {
				return session, nil
			}
		}
		return nil, fmt.Errorf("no previous session found for workspace %s", workspaceRoot)
	}

	session, err := repo.Sessions.Get(target)
	if err != nil {
		return nil, fmt.Errorf("get session %q: %w", target, err)
	}
	if !sameWorkspace(session.ProjectPath, workspaceRoot) {
		return nil, fmt.Errorf("session %s belongs to workspace %s, not %s", shortSessionID(session.ID), session.ProjectPath, workspaceRoot)
	}
	return session, nil
}

func sameWorkspace(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil {
		a = absA
	}
	if errB == nil {
		b = absB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func shortSessionID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func parseStartupArgs(args []string) ([]string, bool, bool, string, bool) {
	filtered := make([]string, 0, len(args))
	dangerousVMax := false
	fullPowerCommands := false
	resumeTarget := ""
	forceNewSession := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "--dangerous-vmax":
			dangerousVMax = true
		case "fullpower":
			fullPowerCommands = true
		case "--new-session":
			forceNewSession = true
		case "--resume":
			resumeTarget = "latest"
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && !isStartupModeArg(args[i+1]) {
				i++
				resumeTarget = strings.TrimSpace(args[i])
			}
		default:
			if value, ok := strings.CutPrefix(arg, "--resume="); ok {
				resumeTarget = strings.TrimSpace(value)
				if resumeTarget == "" {
					resumeTarget = "latest"
				}
				continue
			}
			filtered = append(filtered, arg)
		}
	}
	if forceNewSession {
		resumeTarget = ""
	}
	return filtered, dangerousVMax, fullPowerCommands, resumeTarget, forceNewSession
}

func isStartupModeArg(arg string) bool {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "small", "default", "full", "fullpower":
		return true
	default:
		return false
	}
}

func resolveToolProfile(args []string) string {
	profile := os.Getenv("VIRGIL_TOOL_PROFILE")
	if len(args) > 0 {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "small":
			profile = agent.ToolProfileSmall
		case "default", "full":
			profile = agent.ToolProfileDefault
		default:
			fmt.Printf("Fatal: unknown command/profile: %s\n", args[0])
			fmt.Println("Usage: virgil [small|default] [fullpower] or virgil tsdiag --file <path>")
			os.Exit(1)
		}
	}
	return agent.NormalizeToolProfile(profile)
}

func runTSDiagCommand(args []string) int {
	fs := flag.NewFlagSet("tsdiag", flag.ContinueOnError)
	filePath := fs.String("file", "", "Python file to diagnose")
	redactNames := fs.Bool("redact-names", false, "Redact symbol/class/function names in output")
	maxList := fs.Int("max-list", 120, "Maximum entries to print per section")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *filePath == "" {
		fmt.Println("usage: virgil tsdiag --file /path/to/file.py [--redact-names] [--max-list 120]")
		return 2
	}

	opts := symbols.PythonDiagnosticsOptions{
		RedactNames: *redactNames,
		MaxList:     *maxList,
	}
	diag, err := symbols.DiagnosePythonSymbols(*filePath, opts)
	if err != nil {
		fmt.Printf("tsdiag error: %v\n", err)
		return 1
	}

	fmt.Print(symbols.FormatPythonSymbolDiagnostics(diag, opts))
	return 0
}

func loadRunCommandConfig() tools.RunCommandConfig {
	config := tools.DefaultRunCommandConfig()

	// VIRGIL_RUN_AUTO_ALLOW (カンマ区切り)
	if v := os.Getenv("VIRGIL_RUN_AUTO_ALLOW"); v != "" {
		config.AutoAllow = parseCSV(v)
	}

	// VIRGIL_RUN_DENY (カンマ区切り)
	if v := os.Getenv("VIRGIL_RUN_DENY"); v != "" {
		config.Deny = parseCSV(v)
	}

	// VIRGIL_RUN_DEFAULT (auto / confirm / deny)
	if v := os.Getenv("VIRGIL_RUN_DEFAULT"); v != "" {
		config.DefaultAction = v
	}

	// VIRGIL_RUN_ALLOW_OUTSIDE_WORKSPACE
	if os.Getenv("VIRGIL_RUN_ALLOW_OUTSIDE_WORKSPACE") == "true" {
		config.AllowOutsideWorkspace = true
	}

	// VIRGIL_RUN_TIMEOUT_SECONDS
	if v := os.Getenv("VIRGIL_RUN_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			config.Timeout = time.Duration(n) * time.Second
		}
	}

	return config
}

func parseCSV(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
