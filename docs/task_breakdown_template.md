# <Project Or Migration Name> Tasks

Source: <source document, issue, or high-level request>
Generated: <YYYY-MM-DD>

## Rules

- Execute one task at a time.
- Do not implement tasks not explicitly selected.
- Prefer minimal edits.
- Follow each task's Edit Steps one at a time.
- Prefer edits under 40 inserted lines.
- User runs full runtime tests unless the task says otherwise.

## Task <AREA-001>: <short task title>

Status: todo
Priority: P1
Scope: <one method, helper, file section, or investigation>
Depends on: <optional task id or none>
Manual test: pending
Edit budget: prefer <= 40 inserted lines per edit

### Objective

<One or two sentences describing the desired behavior or investigation result.>

### References

- Source implementation: <path>, <symbol or line range>
- Existing pattern: <path>, <symbol or line range>
- Related helper: <path>, <symbol or line range>

### Edit Targets

- <path>

### Edit Steps

1. Inspect the current target state around the intended insertion point or changed symbol.
2. Insert or adjust a small skeleton.
3. Fill one branch, mode, or helper.
4. Run the narrowest useful validation.
5. Update only this task's status/manual-test line when appropriate.

### Completion Criteria

- <observable implementation-side condition>
- <existing behavior that must not change>
- <validation command or syntax check passes>

### Out of Scope

- <nearby task that must not be implemented here>
- <refactor that should not be included>

### Manual Test Notes

- <what the user will test manually, or "none">
