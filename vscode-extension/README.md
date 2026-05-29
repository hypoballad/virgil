# Virgil Debug Context

Minimal VS Code extension for exporting the active debugger context to Virgil.

## Usage

1. Start a VS Code debug session.
2. Stop at a breakpoint, step, or exception.
3. Run `Virgil: Export Debug Context` from the command palette.
4. The extension writes `.vscode/debug-context.json` in the selected workspace folder.
5. In Virgil, run `/debug-context` or use `/debug-watch once`.

The extension writes through `.vscode/debug-context.json.tmp` and then renames it into place.

## Build

```bash
npm run compile
```

Packaging requires `@vscode/vsce` and can be done with:

```bash
npm run package
```
