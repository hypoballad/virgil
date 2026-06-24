# Virgil

Virgil is a coding-agent CLI focused on reading, understanding, and changing code in a local workspace. It combines an interactive terminal UI, tool-calling agents, symbol indexing, file editing tools, command execution, and a local SQLite-backed conversation store.

Virgil can run with local models through Ollama, local OpenAI-compatible servers such as llama.cpp, or the OpenAI API.

## Features

- Interactive terminal agent for code navigation and edits
- File tools for reading, searching, writing, and targeted edits
- Symbol and call-graph tools backed by Tree-sitter-style indexing
- Markdown outline and section-reading tools for large documents
- Task breakdown workflow for large, resumable coding work
- Shadow snapshots and `/rewind` support for recovering edits
- Optional browser inspector for reviewing LLM exchanges and tool calls
- Optional VS Code debug-context export extension

## Installation

Install the CLI from the public module path:

```bash
go install github.com/hypoballad/virgil/cmd/virgil@latest
```

You can also build from a checkout:

```bash
git clone https://github.com/hypoballad/virgil.git
cd virgil
go build ./cmd/virgil ./cmd/inspect
```

Virgil uses `ripgrep` when available and falls back to an internal search implementation when it is not installed.

## Configuration

Copy the example environment file and select one backend profile:

```bash
cp .env.example .env
```

Default local Ollama profile:

```bash
LLM_PROVIDER=ollama
OLLAMA_HOST=http://127.0.0.1:11434
OLLAMA_MODEL=qwen2.5-coder:7b
```

OpenAI-compatible local server:

```bash
LLM_PROVIDER=openai
OPENAI_API_BASE=http://127.0.0.1:8081/v1
OPENAI_API_KEY=
OPENAI_MODEL=qwen
OPENAI_TEMPERATURE=0.3
OPENAI_TOP_P=0.9
OPENAI_STREAM=false
```

OpenAI API:

```bash
LLM_PROVIDER=openai
OPENAI_API_BASE=https://api.openai.com/v1
OPENAI_API_KEY=sk-...
OPENAI_MODEL=gpt-5-mini
```

Useful optional settings:

- `VIRGIL_WORKSPACE`: workspace root, defaults to the current directory
- `VIRGIL_DB_PATH`: SQLite database path, defaults under `.virgil/`
- `DEBUG=1`: enable debug logging
- `VIRGIL_AGENT_TIMEOUT_MINUTES`: timeout for normal requests
- `VIRGIL_RUN_TIMEOUT_MINUTES`: timeout for generated task execution
- `OPENAI_TEMPERATURE`, `OPENAI_TOP_P`, `OPENAI_MAX_TOKENS`: optional OpenAI-compatible generation parameters
- `OPENAI_STREAM=false`: disable streaming for OpenAI-compatible servers that close streaming connections early
- `VIRGIL_RUN_AUTO_ALLOW`: comma-separated command prefixes to auto-allow
- `VIRGIL_RUN_DENY`: comma-separated command patterns to deny

See [.env.example](.env.example) and [docs/openai_api_guide.md](docs/openai_api_guide.md) for the full backend configuration reference.

## Usage

Start Virgil in the repository you want it to work on:

```bash
virgil
```

For local development from this checkout:

```bash
go run ./cmd/virgil
```

Then ask natural coding questions or request edits:

```text
Find where sessions are stored.
Add tests for the tokenizer counter.
/task fix the inspector timeline rendering bug
```

## Slash Commands

Common commands:

- `/task <request>`: generate and confirm a TODO plan before execution
- `/breakdown <source> [--output <path>]`: create a task breakdown document
- `/tasks <path>`: list tasks in a breakdown document
- `/do <task-id> <path>`: execute one task from a breakdown document
- `/task-status <task-id> <status> <path>`: update a task status
- `/reindex`: rebuild the symbol and call index in the background
- `/callers <name>`: show incoming calls for a function or method
- `/callgraph <name> [depth]`: show a call graph from a function or method
- `/debug-context [question]`: attach exported VS Code debugger context
- `/debug-context clear`: clear the active debug context
- `/rewind`: list recent shadow snapshots
- `/rewind <N|hash>` then `/confirm`: restore a previous snapshot
- `/clear`: start a fresh session

The full command reference is in [docs/commands_reference.md](docs/commands_reference.md).

## Large Task Workflow

For broad migrations or multi-step fixes, use the task breakdown workflow instead of one large prompt:

```text
/breakdown docs/source_plan.md --output docs/source_plan_tasks.md
/tasks docs/source_plan_tasks.md
/do AREA-001 docs/source_plan_tasks.md
/task-status AREA-001 done-pending-user-test docs/source_plan_tasks.md
```

See [docs/task_breakdown_workflow.md](docs/task_breakdown_workflow.md) for the task schema and recommended task boundaries.

## Workspace Specs

Virgil can reference Markdown specs placed under `.virgil/SPECS/` when you ask whether behavior matches a specification:

```text
Check the specs for this behavior.
Is this expected behavior?
```

See [docs/specs_reference_workflow.md](docs/specs_reference_workflow.md).

## Inspector

`cmd/inspect` starts a local browser UI for reviewing Virgil's stored sessions, LLM requests, responses, tool calls, token usage, and latency.

```bash
go run ./cmd/inspect --db .virgil/virgil.db --port 8080
```

Then open:

```text
http://localhost:8080
```

## VS Code Debug Context

The optional VS Code extension exports the current debugger stop location, locals, stack, and surrounding source context to `.vscode/debug-context.json`.

Build the extension:

```bash
cd vscode-extension
npm run compile
```

Usage:

1. Start a VS Code debug session.
2. Stop at a breakpoint, step, or exception.
3. Run `Virgil: Export Debug Context` from the command palette.
4. In Virgil, run `/debug-context` or `/debug-context <question>`.

See [vscode-extension/README.md](vscode-extension/README.md).

## Development

Run the standard test set:

```bash
go test ./cmd/... ./internal/...
```

Build both binaries:

```bash
go build ./cmd/virgil ./cmd/inspect
```

Compile the VS Code extension:

```bash
npm --prefix vscode-extension run compile
```

Format changed Go files with `gofmt`.

## Documentation

- [docs/commands_reference.md](docs/commands_reference.md): slash command details
- [docs/openai_api_guide.md](docs/openai_api_guide.md): local and OpenAI-compatible backend setup
- [docs/task_breakdown_workflow.md](docs/task_breakdown_workflow.md): large-task workflow
- [docs/specs_reference_workflow.md](docs/specs_reference_workflow.md): workspace spec convention
- [docs/tree_sitter_strategy.md](docs/tree_sitter_strategy.md): symbol indexing and context-saving strategy

## License

MIT License. See [LICENSE](LICENSE).
