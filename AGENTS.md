# Repository Guidelines

## Project Structure & Module Organization

Virgil is a Go module (`github.com/hypoballad/virgil`) for a coding-agent CLI and related inspection tools. Main entry points live in `cmd/virgil` for the terminal agent and `cmd/inspect` for the browser-based LLM context inspector. Core packages are under `internal/`: `agent` coordinates agent behavior, `tools` implements file/search/edit commands, `llm` contains OpenAI and Ollama clients, `db` and `repository` manage SQLite persistence, `symbols` handles code indexing, `shadow` manages backup state, and `tui` renders the Bubble Tea UI. SQL migrations are in `internal/db/migrations`, docs are in `docs`, and setup scripts are in `scripts`.

## Build, Test, and Development Commands

- `go test ./cmd/... ./internal/...`: run the standard package tests. Use this instead of `go test ./...` because `scratch/` contains local experiment programs with duplicate `main` functions.
- `go test ./internal/tools`: run tests for a focused package while iterating.
- `go run ./cmd/virgil`: start the CLI agent locally.
- `go run ./cmd/inspect --db /home/agent/data/virgil.db --port 8080`: launch the inspector web UI.
- `go build ./cmd/virgil ./cmd/inspect`: compile both binaries.
- `gofmt -w <files>`: format changed Go files before review.

## Coding Style & Naming Conventions

Use standard Go formatting and idioms. Keep package names short, lowercase, and aligned with directory names. Exported identifiers require clear comments when they are part of a package API. Prefer small, focused functions in `internal/tools` and keep command-facing result types explicit. Test files should sit next to the package code and use `_test.go` suffixes.

## Testing Guidelines

The repository uses Go’s built-in `testing` package. Add table-driven tests for parser, editor, repository, and agent behavior where practical. Follow existing names such as `TestEditFile...` or `TestExtract...`, and keep fixtures in package-local `testdata` directories, as in `internal/symbols/testdata`. Run `go test ./cmd/... ./internal/...` before submitting changes.

## Commit & Pull Request Guidelines

This checkout does not include normal Git history, so no project-specific commit convention is available. Use concise imperative commit subjects, for example `Add symbol lookup tests` or `Fix inspector timeline rendering`. Pull requests should describe the behavioral change, list validation commands run, link related issues, and include screenshots or terminal output when changing `cmd/inspect` or TUI behavior.

## Security & Configuration Tips

Do not commit `.env`, local `.virgil` databases, debug logs, or generated binaries. Use `.env.example` for required configuration keys. Treat database paths and provider API keys as local-only settings.
