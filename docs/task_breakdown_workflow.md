# Task Breakdown Workflow

This document describes the manual P0 workflow for decomposing large coding work into small, resumable Virgil tasks.

The goal is to make large migrations workable with local models by keeping each run focused on one explicit task and, for code changes, one small edit step at a time.

## When To Use

Use this workflow when a normal `/task` request is too broad, for example:

- migrating a large class or module
- porting one subsystem to a new runtime
- fixing a bug that spans several methods
- continuing work after context shrink or agent interruption
- dogfooding VMAX on a large codebase

Do not use it for simple one-file fixes. A direct `/task` is still better for small changes.

## Command Flow

Virgil now supports the P1/P2 command flow:

```text
/breakdown docs/source_plan.md
/tasks .virgil/tasks/source_plan_tasks.md
/do AREA-001 .virgil/tasks/source_plan_tasks.md
/task-status AREA-001 done-pending-user-test .virgil/tasks/source_plan_tasks.md
```

`/do` intentionally executes one explicit task ID. `/do next` is not part of the initial command flow.

## Manual Flow

The manual flow is still useful when dogfooding prompt changes or when commands are unavailable.

1. Create or update a task breakdown document from `.virgil/task_breakdown_template.md`.
2. Ask Virgil to execute exactly one task from that document.
3. Include the selected task block in the prompt.
4. Tell Virgil whether user-run tests are expected.
5. After implementation, update the task status manually or ask Virgil to update only the status line.

`/breakdown` creates `.virgil/task_breakdown_template.md` automatically when it is missing. Users do not need to prepare a `docs/task_breakdown_template.md` file in the target workspace. When `--output` is omitted, `/breakdown` writes the task document to `.virgil/tasks/<source-or-request>_tasks.md`.

For important migrations, keep the task document in a project-visible location such as `docs/<feature>_tasks.md`. For local experiments, `.virgil/tasks/<slug>.md` is acceptable.

## Breakdown Prompt

Use this prompt to ask Virgil to create the task document manually. The `/breakdown` command builds the same kind of prompt.

```text
Create a Virgil task breakdown document for the following work.

Source material:
<paste file paths, source doc, or high-level task>

Rules:
- Output Markdown only.
- Use the task schema from .virgil/task_breakdown_template.md.
- Do not search docs/ or .virgil/ for other task templates.
- If the source material is a Markdown file, inspect it with get_markdown_outline first, then read only relevant sections.
- Split feature-level work into small tasks.
- Prefer one method, one helper, one loader/saver path, or one runtime error per task.
- For every code task, include Edit Steps.
- Edit Steps must be small semantic edits that can be reviewed independently.
- Include References, Edit Targets, Completion Criteria, and Out of Scope for every task.
- Include Depends on when a task requires another task to be done first.
- Use Status: todo for new tasks.
- If the user will run runtime tests, include Manual test: pending.
- Do not include company-specific names unless they are already present in the requested source material.

Output:
- Write the task breakdown Markdown to .virgil/tasks/<source-or-request>_tasks.md unless the user requested another path.
- Use write_file only for that output path.
```

## Execute-One-Task Prompt

Use this prompt to run one task manually. The `/do` command builds the same kind of prompt.

```text
Execute exactly one task from this task breakdown document.

Task ID: <TASK-ID>
Task title: <TASK-TITLE>

Hard constraints:
- Execute only this task.
- Do not start any other task ID.
- First inspect the current edit target around the intended insertion point or changed symbol.
- Read only the References listed in the task unless blocked.
- Follow the task's Edit Steps one at a time.
- Do not make a single full-method or full-class edit when the task can be split.
- Prefer small semantic edits that can be reviewed independently.
- If a large edit is rejected, immediately regenerate a smaller fresh edit; do not retry the rejected payload.
- If previous partial changes are present, repair only obvious damage needed for this task.
- If implementation is done but user testing remains, report done-pending-user-test instead of done.
- Do not update unrelated task statuses.

Task block:
<paste exactly one task block>
```

## Status Convention

Use these values:

```text
todo
doing
done-pending-user-test
done
blocked
skipped
```

Recommended transitions:

- `todo -> doing`: optional while work is in progress.
- `doing -> done-pending-user-test`: implementation-side criteria are satisfied and the user still needs to validate.
- `done-pending-user-test -> done`: user confirms validation passed.
- `doing -> blocked`: required information is missing, validation fails, or user input is required.

Do not mark `done` merely because edits were applied.

## Required Task Fields

Every task should include:

- task ID and title in the heading
- `Status`
- `Objective`
- `References`
- `Edit Targets`
- `Completion Criteria`

Strongly recommended fields:

- `Depends on`
- `Manual test`
- `Edit budget`
- `Edit Steps`
- `Out of Scope`
- `Manual Test Notes`

## Good Task Boundaries

Good task boundaries:

- one method
- one helper function
- one loader/saver path
- one runtime error fix
- one debug-context investigation
- one documentation status update

Bad task boundaries:

- whole class migration
- multiple cooperating classes together
- broad feature sections
- unrelated refactors

## Good Edit Steps

Good edit steps:

- inspect current insertion point
- insert a small skeleton
- fill one branch or mode
- add one helper
- validate syntax
- update only one status/manual-test line

Bad edit steps:

- mix unrelated implementation areas in one edit
- combine implementation and status updates
- reuse an omitted or scrubbed tool argument

## VMAX Notes

VMAX can be used with this manual workflow, but keep one explicit task per VMAX run.

Large edit payloads are allowed. If a tool rejects an omitted, scrubbed, or malformed payload, regenerate valid arguments from current source instead of retrying the same payload.

## Command Summary

- `/breakdown <source> [--output <path>]`: generate a task document from source material.
- `/tasks <path>`: list task IDs and status from the task document.
- `/do <task-id> <path>`: execute exactly one task using the execute-one-task prompt.
- `/task-status <task-id> <status> <path>`: update one status line.
