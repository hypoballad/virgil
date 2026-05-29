package main

const indexHTML = `<!DOCTYPE html>
<html lang="ja">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Virgil Inspector</title>
<script src="https://cdn.jsdelivr.net/npm/chart.js@4"></script>
<style>
  :root {
    --bg: #1a1a2e;
    --surface: #16213e;
    --surface2: #0f3460;
    --text: #e0e0e0;
    --text-dim: #888;
    --accent: #e94560;
    --green: #4ecca3;
    --blue: #7ec8e3;
    --yellow: #f0c040;
    --purple: #b088f9;
    --border: #2a2a4a;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: 'Segoe UI', system-ui, sans-serif;
    background: var(--bg);
    color: var(--text);
    display: flex;
    height: 100vh;
  }

  /* サイドバー */
  #sidebar {
    width: 300px;
    min-width: 300px;
    background: var(--surface);
    border-right: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  #sidebar h1 {
    padding: 16px;
    font-size: 18px;
    border-bottom: 1px solid var(--border);
    color: var(--accent);
  }
  #session-list {
    flex: 1;
    overflow-y: auto;
    padding: 8px;
  }
  .session-item {
    padding: 10px 12px;
    margin-bottom: 4px;
    border-radius: 6px;
    cursor: pointer;
    font-size: 13px;
    border: 1px solid transparent;
  }
  .session-item:hover { background: var(--surface2); }
  .session-item.active { border-color: var(--accent); background: var(--surface2); }
  .session-item .model { color: var(--blue); font-size: 11px; }
  .session-item .time { color: var(--text-dim); font-size: 11px; }
  .session-item .status {
    display: inline-block;
    padding: 1px 6px;
    border-radius: 3px;
    font-size: 10px;
  }
  .status-running { background: var(--green); color: #000; }
  .status-completed { background: var(--text-dim); color: #000; }

  /* メインエリア */
  #main {
    flex: 1;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  #toolbar {
    padding: 12px 16px;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    display: flex;
    gap: 12px;
    align-items: center;
  }
  #toolbar .tab {
    padding: 6px 16px;
    border-radius: 4px;
    cursor: pointer;
    font-size: 13px;
    background: transparent;
    color: var(--text-dim);
    border: 1px solid var(--border);
  }
  #toolbar .tab.active { background: var(--accent); color: #fff; border-color: var(--accent); }

  #content {
    flex: 1;
    overflow-y: auto;
    padding: 16px;
  }

  /* タイムライン表示 */
  .exchange-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    margin-bottom: 12px;
    overflow: hidden;
  }
  .exchange-header {
    padding: 10px 14px;
    background: var(--surface2);
    cursor: pointer;
    display: flex;
    justify-content: space-between;
    align-items: center;
    font-size: 13px;
  }
  .exchange-header:hover { opacity: 0.9; }
  .exchange-body { padding: 12px 14px; display: none; }
  .exchange-body.open { display: block; }

  /* メッセージ表示 */
  .message {
    margin-bottom: 8px;
    padding: 8px 12px;
    border-radius: 6px;
    font-size: 13px;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .message-system { background: #1a1a3e; border-left: 3px solid var(--purple); }
  .message-user { background: #1a2e1a; border-left: 3px solid var(--green); }
  .message-assistant { background: #2e1a1a; border-left: 3px solid var(--accent); }
  .message-tool { background: #1a2e2e; border-left: 3px solid var(--blue); }
  .message .role-label {
    font-size: 11px;
    font-weight: bold;
    margin-bottom: 4px;
    text-transform: uppercase;
  }
  .message-system .role-label { color: var(--purple); }
  .message-user .role-label { color: var(--green); }
  .message-assistant .role-label { color: var(--accent); }
  .message-tool .role-label { color: var(--blue); }

  .token-badge {
    font-size: 10px;
    padding: 1px 6px;
    border-radius: 3px;
    background: var(--border);
    color: var(--text-dim);
    margin-left: 8px;
  }

  /* 統計表示 */
  .stats-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
    gap: 12px;
    margin-bottom: 20px;
  }
  .stat-card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px;
  }
  .stat-card .label { font-size: 12px; color: var(--text-dim); }
  .stat-card .value { font-size: 24px; font-weight: bold; margin-top: 4px; }

  .chart-container {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 16px;
    margin-bottom: 16px;
  }
  .chart-container h3 { font-size: 14px; margin-bottom: 12px; color: var(--text-dim); }

  /* ローディング */
  .loading { text-align: center; padding: 40px; color: var(--text-dim); }

  /* 空状態 */
  .empty { text-align: center; padding: 60px; color: var(--text-dim); }

  .show-more {
    display: inline-block;
    margin-top: 8px;
    padding: 2px 8px;
    background: var(--surface2);
    border: 1px solid var(--border);
    border-radius: 4px;
    font-size: 11px;
    cursor: pointer;
    color: var(--blue);
  }
  .show-more:hover { background: var(--border); }

  .btn-copy {
    float: right;
    padding: 4px 10px;
    background: var(--surface2);
    border: 1px solid var(--border);
    border-radius: 4px;
    font-size: 11px;
    cursor: pointer;
    color: var(--text);
  }
  .btn-copy:hover { background: var(--border); }

  .btn-inline {
    padding: 6px 10px;
    margin-right: 8px;
    background: var(--surface2);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text);
    cursor: pointer;
  }
  .btn-inline:hover { background: var(--border); }
  table.context-table {
    width: 100%;
    border-collapse: collapse;
    background: var(--surface);
    border: 1px solid var(--border);
    margin-bottom: 16px;
    font-size: 13px;
  }
  .context-table th, .context-table td {
    padding: 8px 10px;
    border-bottom: 1px solid var(--border);
    text-align: left;
  }
  .context-table th { color: var(--text-dim); background: var(--surface2); }
  .context-json {
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 420px;
    overflow: auto;
    background: #101024;
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 12px;
    font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
    font-size: 12px;
  }
</style>
</head>
<body>

<div id="sidebar">
  <h1>🔍 Virgil Inspector</h1>
  <div id="session-list"><div class="loading">Loading sessions...</div></div>
</div>

<div id="main">
  <div id="toolbar">
    <div class="tab active" data-tab="timeline" onclick="switchTab('timeline')">Timeline</div>
    <div class="tab" data-tab="stats" onclick="switchTab('stats')">Stats</div>
    <div class="tab" data-tab="context" onclick="switchTab('context')">Context</div>
    <span id="session-info" style="margin-left:auto; font-size:12px; color:var(--text-dim)"></span>
  </div>
  <div id="content">
    <div class="empty">Select a session from the sidebar</div>
  </div>
</div>

<script>
let currentSession = null;
let currentTab = 'timeline';
let exchangeDataCache = {}; // キャッシュ

// ---- API ----
async function api(path) {
  const r = await fetch(path);
  if (!r.ok) throw new Error(await r.text());
  return r.json();
}

function expandMsg(exchangeId, msgIdx) {
  const data = exchangeDataCache[exchangeId];
  if (!data) return;
  const content = msgIdx === 'resp' ? data.response_content : data.request_messages[msgIdx].content;
  const el = document.getElementById('msg-' + exchangeId + '-' + msgIdx);
  if (el) {
    el.textContent = content;
    const btn = el.nextElementSibling;
    if (btn && btn.classList.contains('show-more')) {
      btn.style.display = 'none';
    }
  }
}

async function copyRawData(exchangeId, btn) {
  const data = exchangeDataCache[exchangeId];
  if (!data) return;
  const raw = {
    request_messages: data.request_messages,
    response_content: data.response_content,
    response_tool_calls: data.response_tool_calls
  };
  try {
    await navigator.clipboard.writeText(JSON.stringify(raw, null, 2));
    const oldText = btn.textContent;
    btn.textContent = 'Copied!';
    btn.style.color = 'var(--green)';
    setTimeout(() => {
      btn.textContent = oldText;
      btn.style.color = 'var(--text)';
    }, 2000);
  } catch (err) {
    console.error('Failed to copy: ', err);
    alert('Failed to copy to clipboard');
  }
}

// ---- セッション一覧 ----
async function loadSessions() {
  const sessions = await api('/api/sessions');
  const el = document.getElementById('session-list');
  if (sessions.length === 0) {
    el.innerHTML = '<div class="empty">No sessions found</div>';
    return;
  }
  el.innerHTML = sessions.map(s => {
    const date = new Date(s.started_at * 1000);
    const timeStr = date.toLocaleDateString('ja-JP') + ' ' + date.toLocaleTimeString('ja-JP', {hour:'2-digit', minute:'2-digit'});
    const statusClass = s.status === 'running' ? 'status-running' : 'status-completed';
    return '<div class="session-item" data-id="'+s.id+'" onclick="selectSession(\''+s.id+'\')">'
      + '<div><span class="status '+statusClass+'">'+s.status+'</span> <span class="time">'+timeStr+'</span></div>'
      + '<div class="model">'+s.model+'</div>'
      + '<div style="margin-top:2px;font-size:12px;color:var(--text);white-space:nowrap;overflow:hidden;text-overflow:ellipsis">'+(s.task_description||'')+'</div>'
      + '</div>';
  }).join('');
}

// ---- セッション選択 ----
async function selectSession(id) {
  currentSession = id;
  document.querySelectorAll('.session-item').forEach(el => {
    el.classList.toggle('active', el.dataset.id === id);
  });

  const data = await api('/api/sessions/' + id);
  document.getElementById('session-info').textContent =
    data.session.model + ' | ' + data.turns.length + ' turns';

  if (currentTab === 'timeline') {
    await loadTimeline(id);
  } else if (currentTab === 'stats') {
    await loadStats(id);
  } else {
    await loadContext(id);
  }
}

// ---- タブ切り替え ----
function switchTab(tab) {
  currentTab = tab;
  document.querySelectorAll('.tab').forEach(el => {
    el.classList.toggle('active', el.dataset.tab === tab);
  });
  if (!currentSession) return;
  if (tab === 'timeline') loadTimeline(currentSession);
  else if (tab === 'stats') loadStats(currentSession);
  else loadContext(currentSession);
}

// ---- タイムライン ----
async function loadTimeline(sessionID) {
  const content = document.getElementById('content');
  content.innerHTML = '<div class="loading">Loading exchanges...</div>';

  const exchanges = await api('/api/exchanges/' + sessionID);
  if (exchanges.length === 0) {
    content.innerHTML = '<div class="empty">No LLM exchanges recorded</div>';
    return;
  }

  content.innerHTML = exchanges.map((ex, i) => {
    const iterLabel = exchangeIterationLabel(ex.iteration);
    const toolIcon = ex.has_tool_calls ? '🔧' : '📝';
    const formatIcon = ex.has_format ? '📋' : '';
    return '<div class="exchange-card">'
      + '<div class="exchange-header" onclick="toggleExchange('+ex.id+', this)">'
      + '<span>'+toolIcon+' #'+(i+1)+' ('+iterLabel+') — Turn '+ex.turn_id+' '+formatIcon+'</span>'
      + '<span><span class="token-badge">prompt: '+ex.prompt_tokens+'</span>'
      + '<span class="token-badge">comp: '+ex.completion_tokens+'</span>'
      + '<span class="token-badge">'+ex.duration_ms+'ms</span>'
      + '<span class="token-badge">'+Math.round(ex.request_size/1024)+'KB</span></span>'
      + '</div>'
      + '<div class="exchange-body" id="ex-body-'+ex.id+'"></div>'
      + '</div>';
  }).join('');
}

// ---- 交換記録の展開 ----
async function toggleExchange(id, headerEl) {
  const body = document.getElementById('ex-body-' + id);
  if (body.classList.contains('open')) {
    body.classList.remove('open');
    return;
  }

  if (!body.dataset.loaded) {
    body.innerHTML = '<div class="loading">Loading...</div>';
    body.classList.add('open');

    const data = await api('/api/exchange/' + id);
    exchangeDataCache[id] = data; // キャッシュに保存
    let html = '<button class="btn-copy" onclick="copyRawData('+id+', this)">Copy Raw JSON</button>';

    // Role別統計バー
    if (data.role_stats) {
      html += '<div style="margin-bottom:12px;font-size:12px">';
      const roles = Object.entries(data.role_stats);
      const total = data.total_tokens || 1;
      roles.forEach(([role, st]) => {
        const pct = Math.round(st.tokens / total * 100);
        const colors = {system:'var(--purple)',user:'var(--green)',assistant:'var(--accent)',tool:'var(--blue)'};
        html += '<span style="margin-right:16px"><span style="color:'+(colors[role]||'var(--text)')+'">■</span> '
          + role + ': ' + st.tokens + ' tokens (' + pct + '%) [' + st.count + ' msgs]</span>';
      });
      html += '<br><span style="color:var(--text-dim)">Total: '+data.total_tokens+' est. tokens, '+data.message_count+' messages</span>';
      html += '</div>';
    }

    // Messages
    if (data.request_messages) {
      const msgs = Array.isArray(data.request_messages) ? data.request_messages : [];
      msgs.forEach((msg, idx) => {
        const role = msg.role || 'unknown';
        const content = msg.content || '';
        const isLong = content.length > 2000;
        const displayContent = isLong ? content.slice(0, 2000) + '\n...' : content;
        
        html += '<div class="message message-'+role+'">'
          + '<div class="role-label">'+role+'</div>'
          + '<div id="msg-'+id+'-'+idx+'">'+escapeHtml(displayContent)+'</div>';
        
        if (isLong) {
          html += '<div class="show-more" onclick="expandMsg('+id+', '+idx+')">Show More ('+content.length+' chars)</div>';
        }
        html += '</div>';
      });
    }

    // Response
    if (data.response_content) {
      const content = data.response_content;
      const isLong = content.length > 2000;
      const displayContent = isLong ? content.slice(0, 2000) + '\n...' : content;

      html += '<div class="message message-assistant">'
        + '<div class="role-label">response</div>'
        + '<div id="msg-'+id+'-resp">'+escapeHtml(displayContent)+'</div>';
      
      if (isLong) {
        html += '<div class="show-more" onclick="expandMsg('+id+', \'resp\')">Show More ('+content.length+' chars)</div>';
      }
      html += '</div>';
    }
    if (data.response_tool_calls) {
      html += '<div class="message message-tool">'
        + '<div class="role-label">tool_calls</div>'
        + escapeHtml(JSON.stringify(data.response_tool_calls, null, 2))
        + '</div>';
    }

    body.innerHTML = html;
    body.dataset.loaded = 'true';
  }

  body.classList.add('open');
}

// ---- 統計 ----
async function loadStats(sessionID) {
  const content = document.getElementById('content');
  content.innerHTML = '<div class="loading">Loading stats...</div>';

  const stats = await api('/api/stats/' + sessionID);

  let html = '<div class="stats-grid">'
    + statCard('Total Exchanges', stats.total_exchanges)
    + statCard('Prompt Tokens', stats.total_prompt_tokens.toLocaleString())
    + statCard('Completion Tokens', stats.total_comp_tokens.toLocaleString())
    + statCard('Total Duration', Math.round(stats.total_duration_ms/1000) + 's')
    + '</div>';

  // コンテキスト膨張グラフ
  html += '<div class="chart-container"><h3>Context Size Growth (prompt_tokens per exchange)</h3>'
    + '<canvas id="ctx-chart" height="200"></canvas></div>';

  // レイテンシグラフ
  html += '<div class="chart-container"><h3>Latency per Exchange (ms)</h3>'
    + '<canvas id="latency-chart" height="200"></canvas></div>';

  // ツール呼び出し統計
  if (stats.tool_counts && Object.keys(stats.tool_counts).length > 0) {
    html += '<div class="chart-container"><h3>Tool Call Distribution</h3>'
      + '<canvas id="tool-chart" height="200"></canvas></div>';
  }

  content.innerHTML = html;

  // Chart.js グラフ描画
  const labels = stats.iterations.map((d, i) => '#' + (i+1));

  new Chart(document.getElementById('ctx-chart'), {
    type: 'bar',
    data: {
      labels: labels,
      datasets: [{
        label: 'Prompt Tokens',
        data: stats.iterations.map(d => d.prompt_tokens),
        backgroundColor: 'rgba(126, 200, 227, 0.6)',
        borderColor: 'rgba(126, 200, 227, 1)',
        borderWidth: 1
      }]
    },
    options: chartOptions('Tokens')
  });

  new Chart(document.getElementById('latency-chart'), {
    type: 'line',
    data: {
      labels: labels,
      datasets: [{
        label: 'Duration (ms)',
        data: stats.iterations.map(d => d.duration_ms),
        borderColor: 'var(--accent)',
        backgroundColor: 'rgba(233, 69, 96, 0.1)',
        fill: true,
        tension: 0.3
      }]
    },
    options: chartOptions('ms')
  });

  if (stats.tool_counts && document.getElementById('tool-chart')) {
    const toolNames = Object.keys(stats.tool_counts);
    const toolValues = Object.values(stats.tool_counts);
    const colors = ['#e94560','#4ecca3','#7ec8e3','#f0c040','#b088f9'];
    new Chart(document.getElementById('tool-chart'), {
      type: 'doughnut',
      data: {
        labels: toolNames,
        datasets: [{
          data: toolValues,
          backgroundColor: colors.slice(0, toolNames.length)
        }]
      },
      options: {
        plugins: { legend: { labels: { color: '#e0e0e0' } } }
      }
    });
  }
}

// ---- コンテキスト構造 ----
async function loadContext(sessionID) {
  const content = document.getElementById('content');
  content.innerHTML = '<div class="loading">Loading context analysis...</div>';

  const data = await api('/api/context/' + sessionID);
  const rawJSON = JSON.stringify(data.raw_context, null, 2);
  const redactedJSON = JSON.stringify(data.redacted_context, null, 2);
  const sanitizedJSON = JSON.stringify(data.sanitized_context || data.redacted_context, null, 2);

  let html = '<div class="stats-grid">'
    + statCard('Exchange', '#' + data.exchange_id + ' (' + exchangeIterationLabel(data.iteration) + ')')
    + statCard('Prompt Tokens', Number(data.prompt_tokens || 0).toLocaleString())
    + statCard('Estimated Tokens', Number(data.estimated_tokens || 0).toLocaleString())
    + statCard('Tool Def Tokens', Number(data.tool_definition_tokens || 0).toLocaleString())
    + statCard('Compacted Results', Number(data.compacted_tool_results || 0).toLocaleString())
    + statCard('Saved Tokens', Number(data.compaction_saved_tokens || 0).toLocaleString())
    + statCard('Redactions', Number(data.redaction_count || 0).toLocaleString())
    + '</div>';

  html += '<div style="margin-bottom:12px">'
    + '<button class="btn-inline" onclick="copyText(contextRawPayload)">Copy Raw Context JSON</button>'
    + '<button class="btn-inline" onclick="copyText(contextSanitizedPayload)">Copy Sanitized Context JSON</button>'
    + '</div>';

  html += '<h3 style="margin: 12px 0">Context breakdown</h3>';
  html += contextTable(['Category', 'Tokens', 'Bytes', 'Count'], data.breakdown.map(row => [
    row.label,
    formatNumber(row.tokens),
    formatNumber(row.bytes),
    formatNumber(row.count)
  ]));

  if (data.tool_result_breakdown && data.tool_result_breakdown.length > 0) {
    html += '<h3 style="margin: 12px 0">Tool results by tool</h3>';
    html += contextTable(['Tool', 'Tokens', 'Bytes', 'Count', 'Compacted', 'Saved Tokens'], data.tool_result_breakdown.map(row => [
      row.tool_name,
      formatNumber(row.tokens),
      formatNumber(row.bytes),
      formatNumber(row.count),
      formatNumber(row.compacted_count || 0),
      formatNumber(row.compaction_saved_tokens || 0)
    ]));
  }

  if (data.tool_arg_breakdown && data.tool_arg_breakdown.length > 0) {
    html += '<h3 style="margin: 12px 0">Tool call arguments by tool</h3>';
    html += contextTable(['Tool', 'Tokens', 'Bytes', 'Count'], data.tool_arg_breakdown.map(row => [
      row.tool_name,
      formatNumber(row.tokens),
      formatNumber(row.bytes),
      formatNumber(row.count)
    ]));
  }

  html += '<div class="chart-container"><h3>Redacted context preview</h3>'
    + '<div class="context-json">' + escapeHtml(redactedJSON.slice(0, 60000)) + (redactedJSON.length > 60000 ? '\n... truncated in preview' : '') + '</div>'
    + '</div>';

  content.innerHTML = html;
  window.contextRawPayload = rawJSON;
  window.contextRedactedPayload = redactedJSON;
  window.contextSanitizedPayload = sanitizedJSON;
}

function contextTable(headers, rows) {
  return '<table class="context-table"><thead><tr>'
    + headers.map(h => '<th>' + escapeHtml(h) + '</th>').join('')
    + '</tr></thead><tbody>'
    + rows.map(row => '<tr>' + row.map(cell => '<td>' + escapeHtml(String(cell)) + '</td>').join('') + '</tr>').join('')
    + '</tbody></table>';
}

function formatNumber(value) {
  return Number(value || 0).toLocaleString();
}

async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text || '');
  } catch (err) {
    console.error('Failed to copy: ', err);
    alert('Failed to copy to clipboard');
  }
}

function statCard(label, value) {
  return '<div class="stat-card"><div class="label">'+label+'</div><div class="value">'+value+'</div></div>';
}

function exchangeIterationLabel(iteration) {
  if (iteration === -1) return 'escalate';
  if (iteration === -2) return 'shrink';
  return 'iter ' + iteration;
}

function chartOptions(yLabel) {
  return {
    responsive: true,
    scales: {
      x: { ticks: { color: '#888' }, grid: { color: '#2a2a4a' } },
      y: { ticks: { color: '#888' }, grid: { color: '#2a2a4a' }, title: { display: true, text: yLabel, color: '#888' } }
    },
    plugins: { legend: { labels: { color: '#e0e0e0' } } }
  };
}

function escapeHtml(str) {
  return str.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}

// ---- 初期化 ----
loadSessions();
</script>
</body>
</html>`
