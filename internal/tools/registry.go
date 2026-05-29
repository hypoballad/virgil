package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/hypoballad/virgil/internal/repository"
	"github.com/hypoballad/virgil/internal/shadow"
)

// Registry はツールの登録と取得を管理する
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry は新しいレジストリを作成
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register はツールを登録
func (r *Registry) Register(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %q already registered", name)
	}

	r.tools[name] = tool
	return nil
}

// Get は名前でツールを取得
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, exists := r.tools[name]
	return tool, exists
}

// Definitions は全ツールの定義をLLM用に取得
func (r *Registry) Definitions() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		defs = append(defs, tool.Definition())
	}
	return defs
}

// Execute は指定された名前のツールを実行
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (*Result, error) {
	tool, exists := r.Get(name)
	if !exists {
		return nil, fmt.Errorf("tool %q not found", name)
	}

	return tool.Execute(ctx, args)
}

// RegisterRunCommand は run_command ツールを登録する
func (r *Registry) RegisterRunCommand(config RunCommandConfig) {
	r.Register(NewRunCommandTool(config))
}

// RegisterGetFileOutline は get_file_outline ツールを登録する
func (r *Registry) RegisterGetFileOutline(workspaceRoot string) {
	r.Register(NewGetFileOutlineTool(workspaceRoot))
}

// RegisterReadSymbol は read_symbol ツールを登録する
func (r *Registry) RegisterReadSymbol(workspaceRoot string) {
	r.Register(NewReadSymbolTool(workspaceRoot))
}

// RegisterGetSymbolOutline は get_symbol_outline ツールを登録する
func (r *Registry) RegisterGetSymbolOutline(workspaceRoot string) {
	r.Register(NewGetSymbolOutlineTool(workspaceRoot))
}

// RegisterEditWithPattern は edit_with_pattern ツールを登録する
func (r *Registry) RegisterEditWithPattern(workspaceRoot string) {
	r.Register(NewEditWithPatternTool(workspaceRoot))
}

// RegisterFetchDocs は fetch_docs ツールを登録する
func (r *Registry) RegisterFetchDocs() {
	r.Register(NewFetchDocsTool())
}

// RegisterRunTests は run_tests ツールを登録する
func (r *Registry) RegisterRunTests(workspaceRoot string) {
	r.Register(NewRunTestsTool(workspaceRoot))
}

// RegisterGetCallers は get_callers ツールを登録する
func (r *Registry) RegisterGetCallers(calls *repository.CallRepository) {
	r.Register(NewGetCallersTool(calls))
}

// RegisterGetCallGraph は get_call_graph ツールを登録する
func (r *Registry) RegisterGetCallGraph(calls *repository.CallRepository) {
	r.Register(NewGetCallGraphTool(calls))
}

// RegisterGetFileImports は get_file_imports ツールを登録する
func (r *Registry) RegisterGetFileImports(workspaceRoot string, imports *repository.ImportRepository, symbols *repository.SymbolRepository) {
	r.Register(NewGetFileImportsTool(workspaceRoot, imports, symbols))
}

// RegisterGetJSONOutline は get_json_outline ツールを登録する
func (r *Registry) RegisterGetJSONOutline(workspaceRoot string) {
	r.Register(NewGetJSONOutlineTool(workspaceRoot))
}

// RegisterReadJSONPath は read_json_path ツールを登録する
func (r *Registry) RegisterReadJSONPath(workspaceRoot string) {
	r.Register(NewReadJSONPathTool(workspaceRoot))
}

func (r *Registry) RegisterGetMarkdownOutline(workspaceRoot string) {
	r.Register(NewGetMarkdownOutlineTool(workspaceRoot))
}

func (r *Registry) RegisterReadMarkdownSection(workspaceRoot string) {
	r.Register(NewReadMarkdownSectionTool(workspaceRoot))
}

// RegisterFindDependents は find_dependents ツールを登録する
func (r *Registry) RegisterFindDependents(imports *repository.ImportRepository) {
	r.Register(NewFindDependentsTool(imports))
}

// RegisterGetDiffSummary は get_diff_summary ツールを登録する。
func (r *Registry) RegisterGetDiffSummary(shadowRepo *shadow.ShadowRepo) {
	r.Register(NewGetDiffSummaryTool(shadowRepo))
}
