package api

import "net/http"

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(rootStatusPageHTML))
}

const rootStatusPageHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>AudioCleaner</title>
  <style>
    :root {
      color-scheme: light dark;
      --bg: #f7f7f4;
      --panel: #ffffff;
      --text: #161613;
      --muted: #686862;
      --line: #dadad2;
      --accent: #0f766e;
      --bad: #b42318;
      --warn: #a15c07;
    }
    @media (prefers-color-scheme: dark) {
      :root {
        --bg: #151613;
        --panel: #1e201c;
        --text: #f2f1ec;
        --muted: #a9aaa3;
        --line: #363832;
        --accent: #5eead4;
        --bad: #f97066;
        --warn: #fdb022;
      }
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font: 14px/1.45 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: var(--bg);
      color: var(--text);
    }
    header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 16px;
      padding: 18px 24px;
      border-bottom: 1px solid var(--line);
      background: var(--panel);
    }
    h1 {
      margin: 0;
      font-size: 20px;
      font-weight: 650;
      letter-spacing: 0;
    }
    main {
      width: min(1120px, 100%);
      margin: 0 auto;
      padding: 24px;
    }
    .actions {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
      align-items: center;
    }
    button, a.button {
      min-height: 34px;
      padding: 7px 12px;
      border: 1px solid var(--line);
      border-radius: 6px;
      background: var(--panel);
      color: var(--text);
      text-decoration: none;
      cursor: pointer;
    }
    button.primary {
      border-color: var(--accent);
      color: var(--accent);
    }
    .message {
      color: var(--muted);
      min-height: 20px;
    }
    .grid {
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 12px;
    }
    .tile, section {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
    }
    .tile {
      padding: 14px;
      min-height: 86px;
    }
    .label {
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
    }
    .value {
      margin-top: 6px;
      font-size: 24px;
      font-weight: 700;
      letter-spacing: 0;
    }
    .ok { color: var(--accent); }
    .bad { color: var(--bad); }
    .warn { color: var(--warn); }
    section {
      margin-top: 16px;
      overflow: hidden;
    }
    h2 {
      margin: 0;
      padding: 14px 16px;
      border-bottom: 1px solid var(--line);
      font-size: 15px;
      letter-spacing: 0;
    }
    pre {
      margin: 0;
      padding: 16px;
      overflow: auto;
      color: var(--muted);
      font: 12px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    }
    table {
      width: 100%;
      border-collapse: collapse;
    }
    th, td {
      padding: 10px 12px;
      border-bottom: 1px solid var(--line);
      text-align: left;
      vertical-align: top;
      white-space: nowrap;
    }
    th {
      color: var(--muted);
      font-size: 12px;
      font-weight: 600;
      text-transform: uppercase;
    }
    td.path {
      white-space: normal;
      word-break: break-all;
      min-width: 240px;
    }
    @media (max-width: 760px) {
      header { align-items: flex-start; flex-direction: column; padding: 16px; }
      main { padding: 16px; }
      .grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
      th, td { padding: 9px 10px; }
    }
  </style>
</head>
<body>
  <header>
    <h1>AudioCleaner</h1>
    <div class="actions">
      <button class="primary" id="refresh" type="button">Refresh</button>
      <button id="scan-all" type="button">Scan All</button>
      <a class="button" href="/api/status">Status API</a>
      <a class="button" href="/api/jobs">Jobs API</a>
      <a class="button" href="/api/backups">Backups API</a>
      <span class="message" id="operation-status" aria-live="polite"></span>
    </div>
  </header>
  <main>
    <div class="grid">
      <div class="tile"><div class="label">Service</div><div class="value" id="service">loading</div></div>
      <div class="tile"><div class="label">Qualified</div><div class="value ok" id="qualified">0</div></div>
      <div class="tile"><div class="label">Unqualified</div><div class="value bad" id="unqualified">0</div></div>
      <div class="tile"><div class="label">Processing</div><div class="value warn" id="processing">0</div></div>
    </div>
    <section>
      <h2>Summary</h2>
      <pre id="summary">loading</pre>
    </section>
    <section>
      <h2>Recent Jobs</h2>
      <table>
        <thead>
          <tr><th>ID</th><th>Path</th><th>Status</th><th>Source</th><th>Updated</th></tr>
        </thead>
        <tbody id="jobs"><tr><td colspan="5">loading</td></tr></tbody>
      </table>
    </section>
  </main>
  <script>
    async function api(path, options = {}) {
      const response = await fetch(path, {cache: "no-store", ...options});
      const payload = await response.json();
      if (!response.ok || payload.code !== 0) {
        throw new Error(payload.message || response.statusText);
      }
      return payload.data;
    }

    function items(data) {
      if (Array.isArray(data)) return data;
      if (data && Array.isArray(data.items)) return data.items;
      return [];
    }

    function text(id, value) {
      document.getElementById(id).textContent = value == null ? "" : String(value);
    }

    function renderJobs(data) {
      const rows = items(data).slice(0, 12);
      const body = document.getElementById("jobs");
      if (rows.length === 0) {
        body.innerHTML = '<tr><td colspan="5">No jobs</td></tr>';
        return;
      }
      body.innerHTML = rows.map((job) => {
        const id = job.ID || job.id || "";
        const path = job.Path || job.path || "";
        const status = job.Status || job.status || "";
        const source = job.QualificationSource || job.qualification_source || "";
        const updated = job.UpdatedAt || job.updated_at || "";
        return '<tr><td>' + id + '</td><td class="path">' + escapeHTML(path) + '</td><td>' +
          escapeHTML(status) + '</td><td>' + escapeHTML(source) + '</td><td>' + escapeHTML(updated) + '</td></tr>';
      }).join("");
    }

    function escapeHTML(value) {
      return String(value).replace(/[&<>"']/g, (char) => ({
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        "\"": "&quot;",
        "'": "&#39;"
      }[char]));
    }

    async function refresh() {
      text("service", "loading");
      try {
        const [status, jobs, backups] = await Promise.all([
          api("/api/status"),
          api("/api/jobs"),
          api("/api/backups")
        ]);
        const counts = status.counts || {};
        text("service", status.status || "running");
        text("qualified", counts.qualified || 0);
        text("unqualified", counts.unqualified || 0);
        text("processing", counts.processing || 0);
        document.getElementById("summary").textContent = JSON.stringify({
          queue_count: status.queue_count || 0,
          current_processing: status.current_processing || [],
          workers: status.workers,
          transcode_success_rate: status.transcode_success_rate,
          backup_count: items(backups).length,
          backup_usage_bytes: status.backup_usage_bytes || 0,
          recent_failed: status.recent_failed || []
        }, null, 2);
        renderJobs(jobs);
      } catch (error) {
        text("service", "error");
        document.getElementById("summary").textContent = error.message;
      }
    }

    async function scanAll() {
      const button = document.getElementById("scan-all");
      const status = document.getElementById("operation-status");
      button.disabled = true;
      status.textContent = "Queueing scan";
      try {
        await api("/api/scan", {method: "POST"});
        status.textContent = "Scan queued";
        await refresh();
      } catch (error) {
        status.textContent = "Scan failed";
        document.getElementById("summary").textContent = error.message;
      } finally {
        button.disabled = false;
      }
    }

    document.getElementById("refresh").addEventListener("click", refresh);
    document.getElementById("scan-all").addEventListener("click", scanAll);
    refresh();
  </script>
</body>
</html>
`
