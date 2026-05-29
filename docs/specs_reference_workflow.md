# Workspace Specs Reference

Virgil can use Markdown specification documents placed under `.virgil/SPECS/` when the user asks about expected behavior, requirements, or whether something is by design.

This is a convention, not a required project structure. Word or other source documents should be converted to Markdown by the user before placing them in `.virgil/SPECS/`.

## User Flow

Ask naturally:

```text
仕様を確認してください。
これは仕様通りですか?
Check the specs for this behavior.
Is this expected behavior?
```

Virgil should:

1. List `.virgil/SPECS/`.
2. Pick likely Markdown specs.
3. Inspect the outline with `get_markdown_outline`.
4. Read only relevant sections with `read_markdown_section`.
5. Report whether the spec answers the question.

If no relevant spec exists, Virgil should say that no matching workspace spec was found.

## Scope

P0 is read-only guidance only:

- no new tool
- no slash command
- no automatic spec scan
- no automatic Word conversion
- no spec creation or edits unless explicitly requested

## Recommended Layout

```text
.virgil/
  SPECS/
    001-feature-a.md
    002-runtime-behavior.md
    003-known-limitations.md
```

Keep specs focused enough that headings are useful. Large converted documents should have meaningful headings so `read_markdown_section` can read focused sections.
