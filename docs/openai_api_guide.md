# OpenAI API Integration Guide

Date: 2026-05-16

This guide explains how to run Virgil with either local LLMs or an OpenAI-compatible API.

## Supported Backends

Virgil currently supports these backend profiles through environment variables:

| Backend | Provider | Typical Use |
|---|---|---|
| Ollama | `LLM_PROVIDER=ollama` | Default local operation |
| llama.cpp OpenAI-compatible server | `LLM_PROVIDER=openai` with local `OPENAI_API_BASE` | Local OpenAI-compatible operation |
| OpenAI API | `LLM_PROVIDER=openai` with `https://api.openai.com/v1` | Cloud API, paid |

The code path for llama.cpp and OpenAI API is the same `OpenAIClient`. Only `OPENAI_API_BASE`, `OPENAI_API_KEY`, and `OPENAI_MODEL` differ.

## Environment Variables

Virgil reads `.env` at startup via `godotenv.Overload()`, so shell environment variables can be overwritten by `.env`.

Relevant variables:

| Variable | Used By | Notes |
|---|---|---|
| `LLM_PROVIDER` | `cmd/virgil/main.go` | `ollama` or `openai`; defaults to `ollama` |
| `OLLAMA_HOST` | `cmd/virgil/main.go` | Defaults to `http://127.0.0.1:11434` |
| `OLLAMA_MODEL` | `cmd/virgil/main.go` | Defaults to `qwen2.5-coder:7b` |
| `OPENAI_API_BASE` | `cmd/virgil/main.go` | Defaults to `http://127.0.0.1:8081/v1` |
| `OPENAI_API_KEY` | `cmd/virgil/main.go` | Optional for local llama.cpp; required for OpenAI API |
| `OPENAI_MODEL` | `cmd/virgil/main.go` | Defaults to `qwen` for OpenAI-compatible mode |
| `VIRGIL_WORKSPACE` | `cmd/virgil/main.go` | Workspace root; defaults to current directory |
| `VIRGIL_DB_PATH` | `cmd/virgil/main.go` | Optional DB path override |
| `DEBUG` | `cmd/virgil/main.go` | Any non-empty value enables debug logging |
| `VIRGIL_WATCHDOG_CONTEXT_LIMIT` | `internal/agent` setup | Context budget guard |
| `VIRGIL_AGENT_TIMEOUT_MINUTES` | `cmd/virgil/main.go` | Normal request and task generation timeout |
| `VIRGIL_RUN_TIMEOUT_MINUTES` | `cmd/virgil/main.go` | `/task` TODO execution timeout |

Important: keep `.env` syntax strict. Do not put leading spaces before variable names.

## Profile A: Ollama

Use this for local default operation.

```bash
LLM_PROVIDER=ollama
OLLAMA_HOST=http://127.0.0.1:11434
OLLAMA_MODEL=qwen2.5-coder:7b
VIRGIL_WATCHDOG_CONTEXT_LIMIT=12000
```

Run:

```bash
./virgil
```

## Profile B: llama.cpp OpenAI-Compatible Server

Use this when a local server exposes `/v1/chat/completions`.

```bash
LLM_PROVIDER=openai
OPENAI_API_BASE=http://127.0.0.1:8081/v1
OPENAI_API_KEY=
OPENAI_MODEL=qwen
VIRGIL_WATCHDOG_CONTEXT_LIMIT=12000
```

Run:

```bash
./virgil
```

The API key is usually blank for local llama.cpp, but keep the variable present if your local gateway expects one.

## Profile C: OpenAI API

Use this for paid cloud operation.

```bash
LLM_PROVIDER=openai
OPENAI_API_BASE=https://api.openai.com/v1
OPENAI_API_KEY=sk-...your-key-here
OPENAI_MODEL=gpt-5-mini
VIRGIL_WATCHDOG_CONTEXT_LIMIT=100000
```

Run:

```bash
./virgil
```

Smoke test:

```text
こんにちは。短く自己紹介してください。
```

Expected:

- Virgil starts normally.
- The model returns a response.
- Tool calling still works for simple code questions.

## Model Selection

The example uses `gpt-5-mini` because OpenAI's model documentation lists GPT-5 mini as a cost-efficient GPT-5 variant and GPT-5 nano as the fastest, most cost-efficient GPT-5 variant.

OpenAI model availability and pricing change frequently. Before committing to a model for daily use, check:

- https://platform.openai.com/docs/models
- https://openai.com/api/pricing/

As of the 2026-05-16 check, the public pricing page emphasizes newer GPT-5.4 / GPT-5.4 mini pricing, while the model docs still list GPT-5 mini and GPT-5 nano among available models. Treat `.env.example` model names as templates, not guarantees.

Recommended approach:

1. Start with `gpt-5-mini` or the current cost-efficient coding model listed in the official model docs.
2. Verify tool calling on a small code navigation task.
3. If cost matters more than quality, compare with a nano/smaller model.
4. Keep local Ollama or llama.cpp as the compatibility baseline.

## Cost Management

Before using OpenAI API regularly:

1. Open the OpenAI usage dashboard: https://platform.openai.com/usage
2. Configure organization/project limits if available.
3. Start with a small monthly hard limit.
4. Monitor usage during the first few sessions.

Virgil can issue multiple LLM calls per user task because tool-calling loops may iterate. A single session can consume substantially more tokens than a simple chat prompt.

## Troubleshooting

### Authentication Failed

Example error:

```text
openai authentication failed (check OPENAI_API_KEY): ...
```

Check:

- `.env` contains `OPENAI_API_KEY`.
- The key has not been revoked.
- The key belongs to the expected OpenAI project/organization.
- There is no leading whitespace before `OPENAI_API_KEY`.

### Access Forbidden

Example error:

```text
openai access forbidden (check API key permissions): ...
```

Check:

- API key permissions.
- Project access.
- Whether the selected model is available to the project.

### Rate Limit or Quota

Example error:

```text
openai rate limit exceeded (status 429): ...
```

Check:

- Usage dashboard.
- Billing setup.
- Project rate limits.
- Whether another model has lower latency or better availability.

### Bad Request

Example error:

```text
openai bad request (status 400): ...
```

Likely causes:

- Model name is invalid.
- The selected model does not support the request shape.
- Tool calling or `response_format` behavior differs from local compatibility assumptions.

If this happens, inspect the request/response logs and compare against the current OpenAI API docs.

### Slow Responses

Options:

- Increase `VIRGIL_AGENT_TIMEOUT_MINUTES`.
- Reduce `VIRGIL_WATCHDOG_CONTEXT_LIMIT`.
- Use `/shrink` or `/clear`.
- Try a smaller model.

## Regression Checklist

Run before changing daily backend:

```bash
go test ./internal/llm -v
go test ./...
go build ./...
```

Manual checks:

1. Start Virgil with Ollama.
2. Ask a simple code navigation question.
3. Start Virgil with llama.cpp OpenAI-compatible mode.
4. Ask the same question.
5. Start Virgil with OpenAI API.
6. Ask the same question and confirm tool calling still works.

Example tool-calling prompt:

```text
agent.go の Run メソッドの行番号を教えて
```

Expected:

- The model calls a search/symbol tool.
- The final answer uses the tool result.
- Virgil does not require OpenAI-specific prompt or tool schema changes.

## Design Boundary

Virgil remains local-LLM-first. OpenAI API support should not require:

- OpenAI-specific system prompts.
- OpenAI-only tool definitions.
- Breaking Ollama or llama.cpp behavior.
- Hardcoding cloud-only models into core logic.

If OpenAI API support requires any of those, prefer reverting to local operation or isolating the behavior behind explicit configuration.
