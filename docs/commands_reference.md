# Slash Command Reference

Date: 2026-05-16

Source of truth:

- Command dispatch: `internal/tui/update.go` `handleSlashCommand`
- Help text: `internal/tui/update.go` `slashCommandHelp`
- TUI message types: `internal/tui/messages.go`

## Overview

Slash commands are entered in the TUI input field and are handled before normal agent submission. Command names are case-insensitive because the first field is lowercased before dispatch. Arguments are generally parsed with `strings.Fields`, except for free-text commands such as `/task` and `/btw`, which preserve the remaining text after the command prefix.

## Commands

### `/rewind`

Arguments:

- none

Behavior:

- Shows recent shadow git commits.
- Uses `m.shadow.LogRecent(ctx, 20)`.
- Displays numbered entries that can later be passed to `/rewind <N>`.

Related files:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/shadow/shadow.go`

Usage:

```text
/rewind
```

Notes:

- Requires shadow git to be initialized.
- If shadow git is unavailable, prints `Shadow git is not initialized.`

### `/rewind <N>`

Arguments:

- `N`: 1-based index from the recent shadow commit list

Behavior:

- Resolves the numbered commit from recent shadow history.
- Computes a diff summary from the current workspace to the target commit.
- Creates a pending rewind confirmation.
- Does not modify files until `/confirm` is run.

Related files:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/shadow/shadow.go`

Usage:

```text
/rewind 3
```

Notes:

- Invalid indexes produce an error like `invalid index: N (range: 1-M)`.
- The pending confirmation expires after 5 minutes.

### `/rewind <hash>`

Arguments:

- `hash`: shadow commit hash prefix

Behavior:

- Resolves a recent shadow commit whose hash starts with the given prefix.
- Computes a diff summary.
- Creates a pending rewind confirmation.
- Does not modify files until `/confirm` is run.

Related files:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/shadow/shadow.go`

Usage:

```text
/rewind abc1234
```

Notes:

- If no recent commit matches the prefix, returns `commit not found: <hash>`.

### `/confirm`

Arguments:

- none

Behavior:

- Confirms and executes a pending `/rewind`.
- Before rewinding, attempts to create a safety shadow commit with tool name `before-rewind`.
- Restores workspace files to the pending target commit.

Related files:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/shadow/shadow.go`

Usage:

```text
/confirm
```

Notes:

- If there is no pending rewind, prints `No pending rewind operation.`
- If the pending rewind is older than 5 minutes, it expires.

### `/clear`

Arguments:

- none

Behavior:

- Ends the current session with status `cleared`.
- Creates a new session.
- Resets in-memory TUI state:
  - session ID
  - turn number
  - current turn ID
  - visible messages
  - LLM history
  - last tool calls
  - token count
  - current error

Related files:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/repository/session.go`

Usage:

```text
/clear
```

Notes:

- Requires the repository/session store to be available.
- Also clears any active debug context loaded by `/debug-context`.

### `/debug-context`

Arguments:

- none, or a free-text question after the command

Behavior:

- Loads a debug context JSON file from the current workspace.
- Default lookup order is `.vscode/debug-context.json`, then `.virgil/debug-context.json`, starting at the workspace root and then walking parent directories.
- `VIRGIL_DEBUG_CONTEXT_PATH` can override the path; relative paths are resolved from the workspace root.
- Parses VS Code extension debug context schema version 1.
- Stores it as active TUI state.
- Shows a concise summary with current frame, stopped reason, locals count, stack count, and warnings.
- If text follows the command, immediately sends that text as a normal chat request with the debug context attached.
- Attaches the active debug context to subsequent normal chat messages and `/task` requests.
- Does not attach to management commands such as `/help`, `/clear`, `/debug-context`, or `/debug-context clear`.
- Does not clear the active debug context after attaching it.

Related files:

- `internal/tui/update.go`
- `internal/debugctx/debugctx.go`
- `vscode-extension/src/extension.js`

Usage:

```text
/debug-context
/debug-context この停止位置で何が起きているか見てください
```

Notes:

- The VS Code extension writes the context atomically via `.vscode/debug-context.json.tmp` and rename.
- Stale detection uses `current_frame.file_mtime_unix` when available. A mismatch is reported as a warning; the context is still loaded.
- `file_sha256` is accepted but not used by the MVP.
- Unknown future `schema_version` values are parsed best-effort and reported as warnings.

### `/debug-context clear`

Arguments:

- none

Behavior:

- Clears the active debug context without clearing the normal conversation.

Usage:

```text
/debug-context clear
```

### `/vmax`

Arguments:

- none

Behavior:

- Available only when Virgil was started with `--dangerous-vmax`.
- Arms one-shot VMAX mode for the next normal chat or `/task`.
- Prints `VMAX ready!`.
- The next run uses 60 iterations and auto-accepts `run_command` confirmations.
- VMAX automatically turns off when that run finishes, errors, watchdog-stops, or reaches its iteration limit.

Related files:

- `cmd/virgil/main.go`
- `internal/tui/update.go`
- `internal/agent/agent.go`
- `internal/tools/run_command.go`

Usage:

```text
/vmax
```

Notes:

- `/vmax` does not apply to `/btw` or `/continue`.
- Destructive command deny rules, watchdog, protected path checks, shadow snapshots, and omitted-argument guards remain enabled.
- Without `--dangerous-vmax`, `/vmax` prints disabled guidance and does not arm.

### `/task <task>`

Arguments:

- `task`: task description text

Behavior:

- Creates a TODO list for the task using `m.agent.RunTask`.
- Does not pass tools during TODO generation.
- Displays the generated TODO list and waits for confirmation.
- Pressing Enter executes the TODOs through `m.agent.RunTaskTodos`.
- Pressing Esc cancels the pending task plan.

Related files:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/tui/view.go`
- `internal/agent/task.go`

Usage:

```text
/task add logging to agent.go
```

Notes:

- If no task is provided, prints `⚠️ /task requires a description. Example: /task add tests for tokenizer`.
- Plan/Edit mode is still available via Shift+Tab, but `/task` itself does not require either mode.

### `/tasks <path>`

Arguments:

- `path`: workspace-relative path to a task breakdown Markdown document

Behavior:

- Reads the task breakdown document.
- Lists task IDs, titles, and status.
- Does not call the LLM.
- Does not modify files.

Related files:

- `internal/tui/task_breakdown.go`
- `internal/tui/update.go`

Usage:

```text
/tasks docs/reporting_migration_tasks.md
```

Notes:

- Task headings must look like `## Task AREA-001: title`.
- Status is read from the first `Status: <value>` line in each task block.

### `/do <task-id> <path>`

Arguments:

- `task-id`: exact task ID from the task breakdown document
- `path`: workspace-relative path to a task breakdown Markdown document

Behavior:

- Reads exactly one task block from the task breakdown document.
- Builds a constrained execute-one-task prompt.
- Runs the existing `/task` execution path with that prompt.
- Does not automatically update task document status in P1.
- Refuses tasks already marked `done` or `skipped`.

Related files:

- `internal/tui/task_breakdown.go`
- `internal/tui/update.go`
- `internal/agent/task.go`

Usage:

```text
/do RPT-AN-03 docs/reporting_migration_tasks.md
```

Notes:

- P1 intentionally requires explicit task IDs; `/do next` is not implemented.
- The final report should suggest `blocked`, `done-pending-user-test`, or `done`.

### `/task-status <task-id> <status> <path>`

Arguments:

- `task-id`: exact task ID from the task breakdown document
- `status`: one of `todo`, `doing`, `done-pending-user-test`, `done`, `blocked`, `skipped`
- `path`: workspace-relative path to a task breakdown Markdown document

Behavior:

- Reads the task breakdown document.
- Finds the selected task block.
- Replaces only that task block's first `Status: <value>` line.
- Does not call the LLM.

Related files:

- `internal/tui/task_breakdown.go`
- `internal/tui/update.go`

Usage:

```text
/task-status RPT-AN-03 done-pending-user-test docs/reporting_migration_tasks.md
```

### `/breakdown <source> [--output <path>]`

Arguments:

- `source`: source document path or free-text task description
- `--output <path>`: optional workspace-relative output path for the generated task document

Behavior:

- Ensures `.virgil/task_breakdown_template.md` exists, then builds a task-breakdown prompt using that fixed schema.
- Instructs the agent not to search `docs/` or `.virgil/` for alternate templates.
- Requires small code-task `Edit Steps`.
- If `--output` is present, asks the agent to write only that Markdown file.
- If `--output` is absent, writes to `.virgil/tasks/<source-or-request>_tasks.md`.
- Creates the output directory before asking the agent to write the file.
- Does not intentionally edit implementation files.

Related files:

- `internal/tui/task_breakdown.go`
- `internal/tui/update.go`

Usage:

```text
/breakdown docs/reporting_migration.md --output docs/reporting_migration_tasks.md
/breakdown migrate the reporting service
```

Notes:

- The output path must be inside the workspace.
- Output paths containing spaces are not supported in P2.
- The default output path is local Virgil state. Use `--output docs/<name>_tasks.md` when the task document should be project-visible.

### `/reindex`

Arguments:

- none

Behavior:

- Starts a background symbol index scan using mtime-based diff behavior.
- Calls `m.indexer.StartFullScan(context.Background())`.
- Starts indexer status ticking.

Related files:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/symbols/indexer.go`

Usage:

```text
/reindex
```

Notes:

- If the indexer is unavailable, prints `⚠️ Symbol indexer is not available.`

### `/reindex --force`

Arguments:

- `--force` or `-f`

Behavior:

- Starts a background symbol index scan and ignores the mtime cache.
- Calls `m.indexer.StartFullScanWithForce(context.Background(), true)`.

Related files:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/symbols/indexer.go`

Usage:

```text
/reindex --force
/reindex -f
```

Notes:

- The help text documents `--force`; implementation also supports `-f`.

### `/callers <name>`

Arguments:

- `name`: function or method name

Behavior:

- Looks up incoming calls for the given function/method name.
- Calls `m.callRepo.FindIncoming(name, 30)`.
- Formats results with `tools.FormatCallersResult`.

Related files:

- `internal/tui/update.go`
- `internal/repository/calls.go`
- `internal/tools/get_callers.go`

Usage:

```text
/callers Execute
```

Notes:

- If call graph storage is unavailable, prints `⚠️ Call graph is not available.`
- If name is missing, prints `Usage: /callers <function_name>`.
- Limit is fixed at 30 from the TUI command.

### `/callgraph <name> [depth]`

Arguments:

- `name`: function or method name
- `depth`: optional positive integer

Behavior:

- Builds a call graph report starting from the named function.
- Defaults depth to `3`.
- Uses the optional depth when it parses as a positive integer.
- Formats output with `tools.BuildCallGraphReport`.

Related files:

- `internal/tui/update.go`
- `internal/repository/calls.go`
- `internal/tools/get_call_graph.go`

Usage:

```text
/callgraph Execute
/callgraph Execute 4
```

Notes:

- If call graph storage is unavailable, prints `⚠️ Call graph is not available.`
- If name is missing, prints `Usage: /callgraph <function_name> [depth]`.
- Invalid or non-positive depth values are ignored and default to `3`.
- `BuildCallGraphReport` normalizes depth internally, so the final max depth is controlled by the tools package.

### `/shrink`

Arguments:

- none

Behavior:

- Compresses older conversation history into a summary.
- Keeps the system message, summarizes older body messages, and retains recent messages.
- Uses `m.agent.SummarizeHistory`.
- If a current turn ID exists, saves the generated summary with `m.repo.Turns.UpdateTurnSummary`.
- Replaces `m.history` with compressed history.
- Also auto-triggers after normal `/task` or chat responses when context usage reaches 50% or history grows beyond 20 messages.
- Shows a one-time 30% context usage notice before auto-shrink triggers.

Related files:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/agent/agent.go`
- `internal/repository/turn.go`

Usage:

```text
/shrink
```

Notes:

- Requires enough history to have older messages beyond the recent retention window.
- If there is nothing to compress, prints `⚠️ Nothing to shrink yet. Continue for a few more turns, then run /shrink.`
- Auto-shrink is transparent: Virgil prints a start notice and a completion notice with the before/after context percentage.
- Auto-shrink is suppressed while another shrink is running and for 6 messages after the previous auto-shrink.

### `/confirm-run`

Arguments:

- none

Behavior:

- Approves a pending shell command requested by the `run_command` tool.
- Calls `m.agent.NotifyRunCommandConfirmationWithFeedback(true, "")`.
- Clears the pending run command state.

Related files:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/agent/agent.go`
- `internal/tools/run_command.go`

Usage:

```text
/confirm-run
```

Notes:

- If no command is pending, prints `No pending command to confirm.`

### `/reject-run`

Arguments:

- none

Behavior:

- Rejects a pending shell command requested by the `run_command` tool.
- Calls `m.agent.NotifyRunCommandConfirmationWithFeedback(false, "")`.
- Clears the pending run command state.

Related files:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/agent/agent.go`
- `internal/tools/run_command.go`

Usage:

```text
/reject-run
```

Notes:

- If no command is pending, prints `No pending command to reject.`
- While a command is pending, typing normal guidance instead of a slash command rejects the command and returns the guidance to the agent as part of the `run_command` tool result.

Example:

```text
pytest ではなく python -m unittest で確認してください
```

### `/btw <question>`

Arguments:

- `question`: free-form question

Behavior:

- Runs an isolated by-the-way question through `m.agent.RunBtw`.
- Uses current context but does not record the result as part of normal conversation history.
- Renders the response with the special BTW message style.

Related files:

- `internal/tui/update.go`
- `internal/tui/messages.go`
- `internal/agent/agent.go`

Usage:

```text
/btw What does this function do?
```

Notes:

- If question is empty, prints `⚠️ /btw requires a question. Example: /btw What does this function do?`
- If cancelled, prints `⚠️  By-the-way request cancelled by user.`

### `/help`

Arguments:

- none

Behavior:

- Displays `slashCommandHelp()` output.

Related files:

- `internal/tui/update.go`

Usage:

```text
/help
```

### `/unstuck`

Arguments:

- none

Behavior:

- Starts a new recovery turn for a stalled or cancelled local-LLM attempt.
- Does not continue hidden reasoning, partial text, or the same long analysis path.
- Asks the agent to either make exactly one focused tool call or answer with at most 5 concise bullets.
- Preserves the active task constraints from conversation history.

Usage:

```text
/unstuck
```

Related files:

- `internal/tui/update.go`

### `/history`

Arguments:

- none: show recent input history, newest first
- `<number>`: restore that numbered entry into the input box without sending it

Behavior:

- `/history` prints recent input history with numbers.
- `/history <number>` restores the selected entry to the input box.
- Restored input is not sent automatically; edit it if needed, then send with `Alt+Enter` or `Ctrl+D`.
- Slash commands are included in input history.

Usage:

```text
/history
/history 2
```

Related files:

- `internal/tui/update.go`

### `/last`

Arguments:

- none

Behavior:

- Restores the previous input into the input box without sending it.
- This is a shortcut for restoring the newest input-history entry.

Usage:

```text
/last
```

Related files:

- `internal/tui/update.go`

## Unknown Commands

Any unrecognized slash command returns:

```text
Unknown command: <cmd>. Type /help for available commands.
```

## Keyboard Shortcuts Listed By Help

The help output also documents:

- `Enter`: insert newline
- `Alt+Enter`: send message
- `Ctrl+D`: send message
- `Alt+PageUp/PageDown` or `Alt+Up/Down`: navigate input history
- `Shift+Tab`: toggle Plan/Edit mode
- `Ctrl+C (twice)`: quit Virgil

These are key bindings, not slash commands.

## Help Consistency Check

`slashCommandHelp()` lists every command currently handled by `handleSlashCommand`:

- `/rewind`
- `/confirm`
- `/clear`
- `/continue`
- `/unstuck`
- `/abort`
- `/debug-context`
- `/vmax`
- `/task`
- `/reindex`
- `/callers`
- `/callgraph`
- `/shrink`
- `/history`
- `/last`
- `/confirm-run`
- `/reject-run`
- `/btw`
- `/help`

The implementation also supports:

- `/reindex -f`

The help text documents `/reindex --force` but does not mention the short `-f` alias.

No implemented command is completely missing from the help output.

## Suggested Fixes

1. Add the short reindex alias to help text:

```text
  /reindex -f      Force reindex (ignore mtime cache)
```

or change the existing line to:

```text
  /reindex --force, -f  Force reindex (ignore mtime cache)
```

2. Consider documenting that `/callers` uses a fixed limit of 30 from the TUI command.

3. Consider documenting that `/callgraph` invalid depth values are ignored and default to 3.

## JSON Tool Notes

Large `.json` files should be inspected with JSON-specific tools rather than `read_file`.

- `get_json_outline`: returns file size, estimated tokens, and key/type structure up to `max_depth`.
- `read_json_path`: returns a focused JSON value using JSONPath (`$`, `.key`, `[index]`, `[*]`, `[start:end]`).

Typical flow:

```text
get_json_outline(path="config/large.json", max_depth=2)
read_json_path(path="config/large.json", jsonpath="$.users[0]")
```

This avoids loading multi-MB JSON documents into the LLM context.

## Markdown Tool Notes

Large `.md` files should be inspected by heading before using `read_file`.

- `get_markdown_outline`: returns heading hierarchy, line ranges, and estimated tokens.
- `read_markdown_section`: returns one focused section by heading or line range.

Typical flow:

```text
get_markdown_outline(path="docs/large_plan.md", max_depth=2)
read_markdown_section(path="docs/large_plan.md", heading="Implementation Plan")
```

This avoids loading long design docs or specs into the LLM context when only one section is needed.

## Inspector CLI Notes

`cmd/inspect` can run as the browser-based Inspector server, or as a one-shot dogfooding export command.

### Dogfood Export

Purpose:

- Create a local, reviewable package for sharing Virgil dogfooding failures.
- Reuse Inspector context sanitization.
- Generate an issue-body template without creating a GitHub issue or sending data externally.

Usage:

```text
go run ./cmd/inspect --db .virgil/virgil.db --export-dogfood --session latest --out work/dogfood
```

If `.virgil/debug.log` exists next to the selected DB, it is included automatically as a sanitized tail. To specify a log explicitly:

```text
go run ./cmd/inspect --db .virgil/virgil.db --export-dogfood --log .virgil/debug.log
```

Optional company-specific scan patterns:

```text
go run ./cmd/inspect --db .virgil/virgil.db \
  --export-dogfood \
  --deny-patterns ~/.virgil/company-deny-patterns.txt \
  --allow-patterns ~/.virgil/dogfood-allow-patterns.txt \
  --out work/dogfood
```

Generated files:

- `report.md`: human review summary and checklist
- `issue_body.md`: Markdown template suitable for manual issue creation or sharing
- `sanitized_context.json`: sanitized Inspector context payload
- `context_summary.json`: token and tool breakdown without raw context
- `debug_tail.log`: sanitized debug log tail, when available
- `scan_report.json`: secret-scan findings

Important:

- Review the generated files before sharing.
- Raw context is not exported.
- This command does not call `gh`, GitHub APIs, or any external service.
