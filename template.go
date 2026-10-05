package main

// reportTemplate 是单文件 HTML 回放报告模板。/*__DATA__*/ 会被替换为
// 内嵌的 JSON 回放数据。全部逻辑为原生 HTML/CSS/JavaScript，无任何外部依赖。
const reportTemplate = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>增量物化视图回放</title>
<style>
  :root {
    --bg: #0f1420; --panel: #171e2e; --border: #2a3550; --text: #dbe4f5;
    --muted: #7d8db0; --green: #34d399; --amber: #fbbf24; --red: #f87171;
    --blue: #60a5fa;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { background: var(--bg); color: var(--text); font: 14px/1.6 -apple-system, "PingFang SC", "Segoe UI", sans-serif; padding: 24px; }
  h1 { font-size: 20px; margin-bottom: 4px; }
  .meta { color: var(--muted); margin-bottom: 20px; font-size: 13px; }
  .badge { display: inline-block; padding: 2px 10px; border-radius: 999px; font-size: 12px; font-weight: 600; }
  .badge.ok { background: rgba(52,211,153,.15); color: var(--green); }
  .badge.fail { background: rgba(248,113,113,.15); color: var(--red); }
  .controls { display: flex; gap: 8px; align-items: center; margin-bottom: 16px; flex-wrap: wrap; }
  button { background: var(--panel); color: var(--text); border: 1px solid var(--border); border-radius: 8px; padding: 8px 16px; cursor: pointer; font-size: 14px; }
  button:hover { border-color: var(--blue); }
  button:disabled { opacity: .4; cursor: default; }
  input[type=range] { flex: 1; min-width: 160px; accent-color: var(--blue); }
  .step-label { font-variant-numeric: tabular-nums; color: var(--muted); min-width: 110px; text-align: right; }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
  @media (max-width: 860px) { .grid { grid-template-columns: 1fr; } }
  .panel { background: var(--panel); border: 1px solid var(--border); border-radius: 12px; padding: 16px; }
  .panel h2 { font-size: 13px; text-transform: uppercase; letter-spacing: .08em; color: var(--muted); margin-bottom: 12px; }
  .change { font-family: ui-monospace, Menlo, monospace; font-size: 13px; padding: 10px 12px; border-radius: 8px; background: var(--bg); border-left: 3px solid var(--blue); word-break: break-all; }
  .change.INSERT { border-left-color: var(--green); }
  .change.UPDATE { border-left-color: var(--amber); }
  .change.DELETE { border-left-color: var(--red); }
  .event { display: flex; gap: 10px; align-items: baseline; padding: 8px 10px; border-radius: 8px; margin-top: 8px; background: var(--bg); font-size: 13px; }
  .event .tag { flex-shrink: 0; font-size: 11px; font-weight: 700; padding: 1px 8px; border-radius: 4px; }
  .event.GROUP_CREATED .tag { background: rgba(52,211,153,.18); color: var(--green); }
  .event.AGGREGATE_ADJUSTED .tag { background: rgba(251,191,36,.18); color: var(--amber); }
  .event.GROUP_REMOVED .tag { background: rgba(248,113,113,.18); color: var(--red); }
  .event .detail { color: var(--muted); }
  .event .detail b { color: var(--text); font-weight: 600; }
  table { width: 100%; border-collapse: collapse; font-variant-numeric: tabular-nums; }
  th { text-align: left; color: var(--muted); font-size: 12px; font-weight: 600; padding: 6px 10px; border-bottom: 1px solid var(--border); }
  td { padding: 8px 10px; border-bottom: 1px solid rgba(42,53,80,.5); }
  tr.hl-created td { background: rgba(52,211,153,.12); }
  tr.hl-adjusted td { background: rgba(251,191,36,.10); }
  tr.empty-row td { color: var(--muted); font-style: italic; }
  .removed-list { margin-top: 10px; font-size: 13px; color: var(--red); }
  .check { margin-top: 12px; font-size: 13px; }
  .empty-hint { color: var(--muted); font-style: italic; padding: 8px 0; }
</style>
</head>
<body>
<h1>增量物化视图维护 · 变更回放</h1>
<div class="meta" id="meta"></div>
<div class="controls">
  <button id="first">|&lt;</button>
  <button id="prev">&lt; 上一步</button>
  <button id="play">播放</button>
  <button id="next">下一步 &gt;</button>
  <button id="last">&gt;|</button>
  <input type="range" id="slider" min="0" value="0">
  <span class="step-label" id="stepLabel"></span>
</div>
<div class="grid">
  <div class="panel">
    <h2>当前变更与视图事件</h2>
    <div id="changeBox"></div>
    <div id="events"></div>
    <div class="check" id="check"></div>
  </div>
  <div class="panel">
    <h2>聚合视图（count / sum / avg）</h2>
    <table>
      <thead><tr><th>分组</th><th>Count</th><th>Sum</th><th>Avg</th></tr></thead>
      <tbody id="viewBody"></tbody>
    </table>
    <div class="removed-list" id="removed"></div>
  </div>
</div>
<script>
const REPORT = /*__DATA__*/;
let cur = -1; // -1 表示初始空视图
let timer = null;
const $ = id => document.getElementById(id);
const fmt = v => (Math.round(v * 100) / 100).toFixed(2);

$("meta").innerHTML =
  "清理策略 <b>" + REPORT.policy + "</b> · 共 <b>" + REPORT.numSteps + "</b> 条变更 · " +
  "一致性校验 " + (REPORT.allPassed
    ? '<span class="badge ok">全部通过（每步与全量重算一致）</span>'
    : '<span class="badge fail">存在失败步骤</span>');

$("slider").max = REPORT.numSteps - 1;

function render() {
  const step = cur >= 0 ? REPORT.steps[cur] : null;
  $("stepLabel").textContent = (cur + 1) + " / " + REPORT.numSteps;
  $("slider").value = cur;

  // 变更描述
  $("changeBox").innerHTML = step
    ? '<div class="change ' + step.op + '">' + esc(step.change) + "</div>"
    : '<div class="empty-hint">初始状态：空基表，空视图。点击「下一步」开始回放。</div>';

  // 事件列表
  const ev = $("events");
  ev.innerHTML = "";
  if (step && step.events.length) {
    for (const e of step.events) {
      const div = document.createElement("div");
      div.className = "event " + e.kind;
      div.innerHTML = '<span class="tag">' + kindLabel(e.kind) + "</span>" +
        '<span class="detail"><b>' + esc(e.group) + "</b> " +
        aggText(e.before) + " → " + aggText(e.after) + "<br>" + esc(e.note) + "</span>";
      ev.appendChild(div);
    }
  } else if (step) {
    ev.innerHTML = '<div class="empty-hint">该变更未引起视图变化。</div>';
  }

  // 一致性校验
  $("check").innerHTML = step
    ? (step.consistent
        ? '<span class="badge ok">✓ 与全量重算一致</span>'
        : '<span class="badge fail">✗ 与全量重算不一致</span>')
      + ' <span style="color:var(--muted)">活跃记录 ' + step.liveCount + ' 条</span>'
    : "";

  // 视图表格 + 高亮
  const body = $("viewBody");
  body.innerHTML = "";
  const groups = step ? step.groups : {};
  const keys = Object.keys(groups).sort();
  const hl = {};
  const removed = [];
  if (step) {
    for (const e of step.events) {
      if (e.kind === "GROUP_CREATED") hl[e.group] = "hl-created";
      else if (e.kind === "AGGREGATE_ADJUSTED" && !hl[e.group]) hl[e.group] = "hl-adjusted";
      else if (e.kind === "GROUP_REMOVED") removed.push(e.group);
    }
  }
  if (!keys.length) {
    body.innerHTML = '<tr class="empty-row"><td colspan="4">（视图为空）</td></tr>';
  }
  for (const k of keys) {
    const g = groups[k];
    const tr = document.createElement("tr");
    if (hl[k]) tr.className = hl[k];
    tr.innerHTML = "<td><b>" + esc(k) + "</b></td><td>" + g.count +
      "</td><td>" + fmt(g.sum) + "</td><td>" + fmt(g.count ? g.sum / g.count : 0) + "</td>";
    body.appendChild(tr);
  }
  $("removed").innerHTML = removed.length
    ? "🗑 本步清理的分组：" + removed.map(esc).join("、") : "";

  $("prev").disabled = cur < 0;
  $("first").disabled = cur < 0;
  $("next").disabled = cur >= REPORT.numSteps - 1;
  $("last").disabled = cur >= REPORT.numSteps - 1;
}

function kindLabel(k) {
  return { GROUP_CREATED: "分组创建", AGGREGATE_ADJUSTED: "聚合修正", GROUP_REMOVED: "分组清理" }[k] || k;
}
function aggText(g) { return "count=" + g.count + " sum=" + fmt(g.sum); }
function esc(s) {
  return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
function go(n) {
  cur = Math.max(-1, Math.min(REPORT.numSteps - 1, n));
  render();
}
function stop() { clearInterval(timer); timer = null; $("play").textContent = "播放"; }
$("play").onclick = () => {
  if (timer) { stop(); return; }
  if (cur >= REPORT.numSteps - 1) go(-1);
  $("play").textContent = "暂停";
  timer = setInterval(() => {
    if (cur >= REPORT.numSteps - 1) { stop(); return; }
    go(cur + 1);
  }, 700);
};
$("next").onclick = () => go(cur + 1);
$("prev").onclick = () => go(cur - 1);
$("first").onclick = () => go(-1);
$("last").onclick = () => go(REPORT.numSteps - 1);
$("slider").oninput = e => go(parseInt(e.target.value, 10));
document.addEventListener("keydown", e => {
  if (e.key === "ArrowRight") go(cur + 1);
  if (e.key === "ArrowLeft") go(cur - 1);
});
render();
</script>
</body>
</html>
`
