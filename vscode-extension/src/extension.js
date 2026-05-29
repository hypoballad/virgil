const vscode = require("vscode");
const fs = require("fs/promises");
const path = require("path");

const MAX_LOCALS = 50;
const MAX_DEPTH = 2;
const MAX_STRING_CHARS = 200;
const MAX_COLLECTION_ITEMS = 20;
const MAX_STACK_FRAMES = 20;
const MAX_CODE_CONTEXT_LINES = 11;

const stoppedEventsBySession = new Map();

function activate(context) {
  context.subscriptions.push(
    vscode.debug.onDidReceiveDebugSessionCustomEvent((event) => {
      if (event.event === "stopped") {
        stoppedEventsBySession.set(sessionKey(event.session), event.body || {});
      }
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("virgil.exportDebugContext", exportDebugContext)
  );
}

function deactivate() {}

async function exportDebugContext() {
  const session = vscode.debug.activeDebugSession;
  if (!session) {
    vscode.window.showErrorMessage("Virgil: no active debug session.");
    return;
  }

  let stackFrames;
  let selectedThreadId;
  try {
    const stack = await getCurrentStack(session);
    stackFrames = stack.frames;
    selectedThreadId = stack.threadId;
  } catch (err) {
    vscode.window.showErrorMessage(`Virgil: no stopped debug frame available (${shortError(err)}).`);
    return;
  }

  if (!stackFrames.length) {
    vscode.window.showErrorMessage("Virgil: no stopped debug frame available.");
    return;
  }

  const workspaceFolder = resolveWorkspaceFolder(stackFrames);
  if (!workspaceFolder) {
    vscode.window.showErrorMessage("Virgil: no workspace folder is open; debug context was not exported.");
    return;
  }

  const stopped = stoppedEventsBySession.get(sessionKey(session)) || {};
  const stoppedReason = safeString(stopped.reason || "unknown");
  const currentFrame = stackFrames[0];
  const locals = await collectLocals(session, currentFrame.id);
  const exception = await collectException(session, selectedThreadId, stoppedReason);
  const exceptionCandidates = collectExceptionCandidates(locals);
  const finalException = fillExceptionFromCandidates(exception, exceptionCandidates);
  const context = await buildDebugContext({
    session,
    workspaceFolder,
    threadId: selectedThreadId,
    stopped,
    currentFrame,
    stackFrames,
    locals,
    exception: finalException,
    exceptionCandidates,
  });

  const outputPath = path.join(workspaceFolder.uri.fsPath, ".vscode", "debug-context.json");
  try {
    await atomicWriteJSON(outputPath, context);
  } catch (err) {
    vscode.window.showErrorMessage(`Virgil: failed to write debug context (${shortError(err)}).`);
    return;
  }

  const summary = exportSummary(context);
  vscode.window.showInformationMessage(`Virgil: exported debug context${summary}`);
  vscode.window.setStatusBarMessage(`Virgil debug context exported${summary}`, 5000);
}

async function getCurrentStack(session) {
  const active = vscode.debug.activeStackItem;
  const activeThreadId = numericField(active, "threadId") || numericField(active && active.thread, "threadId") || numericField(active && active.thread, "id");
  const activeFrameId = numericField(active, "frameId") || numericField(active, "id");

  const threadsResp = await session.customRequest("threads", {});
  const threads = Array.isArray(threadsResp && threadsResp.threads) ? threadsResp.threads : [];
  if (!threads.length && !activeThreadId) {
    throw new Error("debug adapter returned no threads");
  }

  const candidateThreadIds = [];
  if (activeThreadId) {
    candidateThreadIds.push(activeThreadId);
  }
  for (const thread of threads) {
    if (typeof thread.id === "number" && !candidateThreadIds.includes(thread.id)) {
      candidateThreadIds.push(thread.id);
    }
  }

  let lastErr;
  for (const threadId of candidateThreadIds) {
    try {
      const resp = await session.customRequest("stackTrace", {
        threadId,
        startFrame: 0,
        levels: MAX_STACK_FRAMES,
      });
      let frames = Array.isArray(resp && resp.stackFrames) ? resp.stackFrames : [];
      if (activeFrameId) {
        const index = frames.findIndex((frame) => frame.id === activeFrameId);
        if (index > 0) {
          frames = frames.slice(index).concat(frames.slice(0, index));
        }
      }
      if (frames.length) {
        return { threadId, frames };
      }
    } catch (err) {
      lastErr = err;
    }
  }
  throw lastErr || new Error("debug adapter returned no stack frames");
}

async function collectLocals(session, frameId) {
  if (!frameId) {
    return [];
  }
  let scopesResp;
  try {
    scopesResp = await session.customRequest("scopes", { frameId });
  } catch {
    return [];
  }
  const scopes = Array.isArray(scopesResp && scopesResp.scopes) ? scopesResp.scopes : [];
  const ordered = scopes.slice().sort((a, b) => scopePriority(a) - scopePriority(b));
  const locals = [];
  for (const scope of ordered) {
    if (!scope || typeof scope.variablesReference !== "number" || scope.variablesReference === 0) {
      continue;
    }
    let variablesResp;
    try {
      variablesResp = await session.customRequest("variables", {
        variablesReference: scope.variablesReference,
        start: 0,
        count: Math.max(0, MAX_LOCALS - locals.length),
      });
    } catch {
      continue;
    }
    const variables = Array.isArray(variablesResp && variablesResp.variables) ? variablesResp.variables : [];
    for (const variable of variables) {
      if (locals.length >= MAX_LOCALS) {
        return locals;
      }
      locals.push(formatVariable(variable));
    }
  }
  return locals;
}

async function collectException(session, threadId, reason) {
  const base = {
    type: "",
    message: "",
    traceback_source: reason === "exception" ? "dap_stack" : "none",
  };
  if (reason !== "exception" || !threadId) {
    return base;
  }
  try {
    const info = await session.customRequest("exceptionInfo", { threadId });
    const details = info && info.details ? info.details : {};
    return {
      type: safeString(info && info.exceptionId),
      message: safeString(details.message || info.description || info.breakMode),
      traceback_source: "dap_stack",
    };
  } catch {
    return base;
  }
}

async function buildDebugContext(input) {
  const workspaceRoot = input.workspaceFolder.uri.fsPath;
  const currentFile = sourcePath(input.currentFrame);
  const currentFileInfo = await fileInfo(currentFile, workspaceRoot);

  return {
    schema_version: 1,
    source: "vscode-debugpy",
    exported_at: new Date().toISOString(),
    workspace_root: workspaceRoot,
    language: "python",
    event: "stopped",
    stopped: {
      reason: safeString(input.stopped.reason || "unknown"),
      thread_id: input.stopped.threadId || input.threadId || null,
      thread_name: safeString(input.stopped.threadName || ""),
    },
    exception: input.exception,
    exception_candidates: input.exceptionCandidates || [],
    current_frame: {
      file: displayPath(currentFile, workspaceRoot, input.currentFrame.source && input.currentFrame.source.name),
      absolute_file: currentFile || "",
      line: input.currentFrame.line || 0,
      function: safeString(input.currentFrame.name),
      file_mtime_unix: currentFileInfo.mtime_unix,
      file_sha256: "",
      code_context: await codeContext(currentFile, input.currentFrame.line),
      locals: input.locals,
    },
    stack: input.stackFrames.slice(0, MAX_STACK_FRAMES).map((frame) => {
      const file = sourcePath(frame);
      return {
        file: displayPath(file, workspaceRoot, frame.source && frame.source.name),
        absolute_file: file || "",
        line: frame.line || 0,
        function: safeString(frame.name),
      };
    }),
    user_focus: userFocus(workspaceRoot),
    limits: {
      max_locals: MAX_LOCALS,
      max_depth: MAX_DEPTH,
      max_string_chars: MAX_STRING_CHARS,
      max_collection_items: MAX_COLLECTION_ITEMS,
      max_stack_frames: MAX_STACK_FRAMES,
      max_code_context_lines: MAX_CODE_CONTEXT_LINES,
    },
  };
}

function resolveWorkspaceFolder(stackFrames) {
  const folders = vscode.workspace.workspaceFolders || [];
  if (!folders.length) {
    return undefined;
  }
  for (const frame of stackFrames) {
    const file = sourcePath(frame);
    if (!file) {
      continue;
    }
    const folder = vscode.workspace.getWorkspaceFolder(vscode.Uri.file(file));
    if (folder) {
      return folder;
    }
  }
  const activeFile = vscode.window.activeTextEditor && vscode.window.activeTextEditor.document.uri;
  if (activeFile) {
    const folder = vscode.workspace.getWorkspaceFolder(activeFile);
    if (folder) {
      return folder;
    }
  }
  return folders[0];
}

function userFocus(workspaceRoot) {
  const editor = vscode.window.activeTextEditor;
  if (!editor) {
    return {};
  }
  const selection = editor.selection;
  return {
    active_file: displayPath(editor.document.uri.fsPath, workspaceRoot, editor.document.fileName),
    active_line: selection.active.line + 1,
    selection_start: selection.start.line + 1,
    selection_end: selection.end.line + 1,
  };
}

async function codeContext(file, currentLine) {
  if (!file || !currentLine) {
    return { start_line: 0, current_line: currentLine || 0, lines: [] };
  }
  let text;
  try {
    const openDoc = vscode.workspace.textDocuments.find((doc) => doc.uri.fsPath === file);
    text = openDoc ? openDoc.getText() : await fs.readFile(file, "utf8");
  } catch {
    return { start_line: 0, current_line: currentLine, lines: [] };
  }

  const all = text.replace(/\r\n/g, "\n").split("\n");
  const half = Math.floor(MAX_CODE_CONTEXT_LINES / 2);
  const start = Math.max(1, currentLine - half);
  const end = Math.min(all.length, start + MAX_CODE_CONTEXT_LINES - 1);
  const lines = [];
  for (let line = start; line <= end; line++) {
    lines.push({ line, text: all[line - 1] || "" });
  }
  return { start_line: start, current_line: currentLine, lines };
}

async function fileInfo(file) {
  if (!file) {
    return { mtime_unix: 0 };
  }
  try {
    const stat = await fs.stat(file);
    return { mtime_unix: Math.floor(stat.mtimeMs / 1000) };
  } catch {
    return { mtime_unix: 0 };
  }
}

async function atomicWriteJSON(targetPath, value) {
  const dir = path.dirname(targetPath);
  const tmpPath = `${targetPath}.tmp`;
  const data = `${JSON.stringify(value, null, 2)}\n`;
  await fs.mkdir(dir, { recursive: true });
  await fs.writeFile(tmpPath, data, "utf8");
  try {
    await fs.rename(tmpPath, targetPath);
  } catch (err) {
    if (process.platform !== "win32") {
      throw err;
    }
    try {
      await fs.rm(targetPath, { force: true });
      await fs.rename(tmpPath, targetPath);
    } catch (fallbackErr) {
      throw fallbackErr;
    }
  }
}

function formatVariable(variable) {
  const variablesReference = typeof variable.variablesReference === "number" ? variable.variablesReference : 0;
  return {
    name: safeString(variable.name),
    type: safeString(variable.type),
    value: truncate(safeString(variable.value), MAX_STRING_CHARS),
    variablesReference,
    children_omitted: variablesReference > 0,
  };
}

function collectExceptionCandidates(locals) {
  if (!Array.isArray(locals)) {
    return [];
  }
  const candidates = [];
  for (const local of locals) {
    const candidate = exceptionCandidateFromLocal(local);
    if (candidate) {
      candidates.push(candidate);
    }
  }
  candidates.sort((a, b) => confidenceRank(b.confidence) - confidenceRank(a.confidence));
  return candidates.slice(0, 5);
}

function exceptionCandidateFromLocal(local) {
  if (!local) {
    return null;
  }
  const name = safeString(local.name);
  const type = safeString(local.type);
  const value = safeString(local.value);
  const nameHit = isExceptionName(name);
  const typeHit = isExceptionType(type);
  const valueHit = isExceptionValue(value);
  if (!nameHit && !typeHit && !valueHit) {
    return null;
  }
  let confidence = "low";
  if (typeHit && (nameHit || valueHit)) {
    confidence = "high";
  } else if (typeHit || (nameHit && valueHit)) {
    confidence = "medium";
  }
  return {
    name,
    type,
    value,
    source: "locals",
    confidence,
  };
}

function fillExceptionFromCandidates(exception, candidates) {
  const current = exception || { type: "", message: "", traceback_source: "none" };
  if ((current.type || current.message) || !Array.isArray(candidates) || !candidates.length) {
    return current;
  }
  const candidate = candidates[0];
  return {
    type: candidate.type || candidate.name,
    message: candidate.value || "",
    traceback_source: `locals:${candidate.name}`,
  };
}

function isExceptionName(name) {
  return ["e", "err", "error", "exc", "exception"].includes(String(name || "").toLowerCase());
}

function isExceptionType(type) {
  const value = String(type || "");
  if (value === "BaseException" || value === "Exception") {
    return true;
  }
  return value.endsWith("Error") || value.endsWith("Exception");
}

function isExceptionValue(value) {
  return /\b[A-Za-z_][A-Za-z0-9_]*(Error|Exception)\(/.test(String(value || ""));
}

function confidenceRank(confidence) {
  switch (confidence) {
    case "high":
      return 3;
    case "medium":
      return 2;
    case "low":
      return 1;
    default:
      return 0;
  }
}

function scopePriority(scope) {
  const name = String(scope && scope.name ? scope.name : "").toLowerCase();
  if (name.includes("local")) {
    return 0;
  }
  if (name.includes("closure")) {
    return 1;
  }
  if (name.includes("global")) {
    return 3;
  }
  return 2;
}

function sourcePath(frame) {
  if (!frame || !frame.source) {
    return "";
  }
  return normalizeSourcePath(frame.source.path || "");
}

function normalizeSourcePath(value) {
  const raw = safeString(value);
  if (!raw) {
    return "";
  }
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(raw)) {
    try {
      const uri = vscode.Uri.parse(raw);
      if (uri.scheme === "file" || uri.scheme === "vscode-remote") {
        return uri.fsPath;
      }
    } catch {
      return raw;
    }
  }
  return raw;
}

function displayPath(file, workspaceRoot, fallback) {
  if (!file) {
    return safeString(fallback || "");
  }
  const relative = path.relative(workspaceRoot, file);
  if (relative && !relative.startsWith("..") && !path.isAbsolute(relative)) {
    return normalizePath(relative);
  }
  return normalizePath(file);
}

function exportSummary(context) {
  const frame = context && context.current_frame ? context.current_frame : {};
  const file = frame.file || "unknown";
  const line = frame.line ? `:${frame.line}` : "";
  const locals = Array.isArray(frame.locals) ? frame.locals.length : 0;
  const stack = Array.isArray(context && context.stack) ? context.stack.length : 0;
  return ` (${file}${line}, locals ${locals}, stack ${stack})`;
}

function normalizePath(value) {
  return String(value || "").replace(/\\/g, "/");
}

function numericField(value, key) {
  if (!value || typeof value !== "object") {
    return 0;
  }
  const candidate = value[key];
  return typeof candidate === "number" ? candidate : 0;
}

function sessionKey(session) {
  return session && (session.id || session.name || String(session));
}

function safeString(value) {
  if (value === undefined || value === null) {
    return "";
  }
  return String(value);
}

function truncate(value, limit) {
  if (value.length <= limit) {
    return value;
  }
  return `${value.slice(0, limit)}...`;
}

function shortError(err) {
  return err && err.message ? err.message : String(err);
}

module.exports = {
  activate,
  deactivate,
  _test: {
    collectExceptionCandidates,
    fillExceptionFromCandidates,
  },
};
