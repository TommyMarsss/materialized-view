package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Step 回放序列中的一步：操作、产生的事件、操作后的视图快照与一致性校验结果
type Step struct {
	Index      int        `json:"index"`
	Op         string     `json:"op"`
	OpType     OpType     `json:"opType"`
	Events     []Event    `json:"events"`
	Snapshot   []GroupRow `json:"snapshot"`
	Records    []Record   `json:"records"`
	Consistent bool       `json:"consistent"`
}

// RunStream 依序应用变更流，每一步都做全量重算一致性校验，收集回放数据。
// 任一步校验失败即返回错误（增量视图正确性的黄金标准）。
func RunStream(v *View, ops []Operation) ([]Step, error) {
	steps := make([]Step, 0, len(ops))
	for i, op := range ops {
		events, err := v.Apply(op)
		if err != nil {
			return nil, fmt.Errorf("step %d (%s): %w", i, op, err)
		}
		ok := v.CheckConsistency()
		steps = append(steps, Step{
			Index:      i,
			Op:         op.String(),
			OpType:     op.Type,
			Events:     events,
			Snapshot:   v.Snapshot(),
			Records:    v.Records(),
			Consistent: ok,
		})
		if !ok {
			return steps, fmt.Errorf("step %d (%s): incremental view diverges from full recompute", i, op)
		}
	}
	return steps, nil
}

// RenderHTML 生成单一静态 HTML 回放文件（数据与逻辑全部内嵌，无第三方依赖）
func RenderHTML(steps []Step, removeEmptyGroups bool) (string, error) {
	data, err := json.Marshal(steps) // encoding/json 默认转义 < > &，可安全内嵌 <script>
	if err != nil {
		return "", err
	}
	policy := "移除空分组（remove-empty）"
	if !removeEmptyGroups {
		policy = "保留空分组（keep-empty）"
	}
	html := strings.Replace(htmlTemplate, "/*__DATA__*/", string(data), 1)
	html = strings.Replace(html, "/*__POLICY__*/", policy, 1)
	return html, nil
}

const htmlTemplate = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>增量物化视图回放</title>
<style>
  :root {
    --bg: #0f1420; --panel: #1a2130; --border: #2c3650; --text: #e6e9f0; --dim: #8b94a8;
    --insert: #4ade80; --update: #fbbf24; --delete: #f87171;
    --created: #14532d; --adjusted: #713f12; --removed: #7f1d1d; --kept: #334155;
  }
  @media (prefers-color-scheme: light) {
    :root { --bg:#f6f7fb; --panel:#ffffff; --border:#d8dde8; --text:#1c2333; --dim:#5b6478;
      --created:#dcfce7; --adjusted:#fef3c7; --removed:#fee2e2; --kept:#e2e8f0; }
  }
  * { box-sizing: border-box; }
  body { margin:0; font:14px/1.5 -apple-system,"Segoe UI",Roboto,"PingFang SC","Microsoft YaHei",sans-serif;
         background:var(--bg); color:var(--text); }
  header { padding:14px 20px; border-bottom:1px solid var(--border); }
  header h1 { margin:0 0 4px; font-size:18px; }
  header .meta { color:var(--dim); font-size:12px; }
  .controls { display:flex; gap:8px; align-items:center; padding:12px 20px; flex-wrap:wrap;
              border-bottom:1px solid var(--border); position:sticky; top:0; background:var(--bg); z-index:2; }
  button { font:inherit; padding:6px 14px; border:1px solid var(--border); border-radius:6px;
           background:var(--panel); color:var(--text); cursor:pointer; }
  button:hover { border-color:var(--dim); }
  button:disabled { opacity:.4; cursor:default; }
  input[type=range] { flex:1; min-width:120px; }
  .badge { font-size:12px; padding:2px 10px; border-radius:99px; border:1px solid var(--border); }
  .badge.ok { color:var(--insert); border-color:var(--insert); }
  .badge.bad { color:var(--delete); border-color:var(--delete); }
  main { display:grid; grid-template-columns: 1fr 1fr; gap:16px; padding:16px 20px; }
  @media (max-width: 860px) { main { grid-template-columns: 1fr; } }
  section { background:var(--panel); border:1px solid var(--border); border-radius:10px; padding:14px; }
  h2 { margin:0 0 10px; font-size:14px; color:var(--dim); text-transform:uppercase; letter-spacing:.06em; }
  table { width:100%; border-collapse:collapse; }
  th, td { text-align:left; padding:6px 8px; border-bottom:1px solid var(--border); font-variant-numeric:tabular-nums; }
  th { color:var(--dim); font-weight:600; font-size:12px; }
  tr.ev-created td { background:var(--created); }
  tr.ev-adjusted td { background:var(--adjusted); }
  tr.ev-removed td { background:var(--removed); text-decoration:line-through; }
  tr.ev-kept td { background:var(--kept); }
  .op { font-family:ui-monospace,Menlo,monospace; font-size:13px; padding:8px 10px; border-radius:6px;
        border:1px solid var(--border); margin-bottom:10px; }
  .op.insert { color:var(--insert); } .op.update { color:var(--update); } .op.delete { color:var(--delete); }
  .events { list-style:none; margin:0; padding:0; }
  .events li { padding:6px 10px; border-radius:6px; margin-bottom:6px; font-size:13px; border:1px solid var(--border); }
  .events li.group_created { border-left:4px solid var(--insert); }
  .events li.group_adjusted { border-left:4px solid var(--update); }
  .events li.group_removed { border-left:4px solid var(--delete); }
  .events li.group_kept { border-left:4px solid var(--dim); }
  .events .tag { font-size:11px; color:var(--dim); margin-right:6px; text-transform:uppercase; }
  .empty { color:var(--dim); font-style:italic; }
  .legend { display:flex; gap:14px; flex-wrap:wrap; font-size:12px; color:var(--dim); padding:0 20px 16px; }
  .legend span::before { content:""; display:inline-block; width:10px; height:10px; border-radius:2px; margin-right:5px; }
  .legend .l-created::before { background:var(--created); } .legend .l-adjusted::before { background:var(--adjusted); }
  .legend .l-removed::before { background:var(--removed); } .legend .l-kept::before { background:var(--kept); }
</style>
</head>
<body>
<header>
  <h1>增量物化视图维护 · 变更流回放</h1>
  <div class="meta">分组清理策略：/*__POLICY__*/ · 每步均与全量重算比对（黄金标准）</div>
</header>
<div class="controls">
  <button id="first">⏮</button><button id="prev">◀ 上一步</button>
  <button id="play">▶ 播放</button>
  <button id="next">下一步 ▶</button><button id="last">⏭</button>
  <input type="range" id="slider" min="0" max="0" value="0">
  <span id="pos" class="badge"></span>
  <span id="consistency" class="badge"></span>
</div>
<main>
  <section>
    <h2>当前操作</h2>
    <div id="op" class="op"></div>
    <h2>维护事件</h2>
    <ul id="events" class="events"></ul>
  </section>
  <section>
    <h2>聚合视图（GROUP BY group）</h2>
    <table><thead><tr><th>分组</th><th>COUNT</th><th>SUM</th><th>AVG</th></tr></thead><tbody id="view"></tbody></table>
    <h2 style="margin-top:16px">基表当前有效记录</h2>
    <table><thead><tr><th>ID</th><th>分组</th><th>值</th></tr></thead><tbody id="recs"></tbody></table>
  </section>
</main>
<div class="legend">
  <span class="l-created">分组创建</span><span class="l-adjusted">聚合修正</span>
  <span class="l-removed">分组清理</span><span class="l-kept">空分组保留</span>
</div>
<script>
const STEPS = /*__DATA__*/;
const N = STEPS.length;
let cur = -1, timer = null;
const $ = id => document.getElementById(id);
const fmt = v => Number.isInteger(v) ? String(v) : v.toFixed(4).replace(/0+$/,"").replace(/\.$/,"");
const EV_LABEL = {group_created:"创建", group_adjusted:"修正", group_removed:"清理", group_kept:"保留"};
const EV_ROW = {group_created:"ev-created", group_adjusted:"ev-adjusted", group_removed:"ev-removed", group_kept:"ev-kept"};

function render() {
  const s = cur >= 0 ? STEPS[cur] : null;
  $("pos").textContent = "步骤 " + (cur + 1) + " / " + N;
  $("slider").value = cur + 1;
  $("first").disabled = $("prev").disabled = cur < 0;
  $("next").disabled = $("last").disabled = cur >= N - 1;
  const c = $("consistency");
  if (!s) { c.textContent = "—"; c.className = "badge"; }
  else { c.textContent = s.consistent ? "✓ 与全量重算一致" : "✗ 不一致"; c.className = "badge " + (s.consistent ? "ok" : "bad"); }
  $("op").textContent = s ? s.op : "（初始状态：空视图）";
  $("op").className = "op " + (s ? s.opType : "");
  const ev = $("events"); ev.innerHTML = "";
  const evts = s ? s.events : [];
  if (!evts.length) ev.innerHTML = '<li class="empty">无事件</li>';
  for (const e of evts) {
    const li = document.createElement("li");
    li.className = e.type;
    li.innerHTML = '<span class="tag">' + EV_LABEL[e.type] + '</span>' + e.detail +
      ' <span style="color:var(--dim)">[' + e.group + ': count ' + e.before.count + '→' + e.after.count +
      ', sum ' + fmt(e.before.sum) + '→' + fmt(e.after.sum) + ']</span>';
    ev.appendChild(li);
  }
  const changed = {};
  for (const e of evts) changed[e.group] = EV_ROW[e.type];
  const view = $("view"); view.innerHTML = "";
  const rows = s ? s.snapshot : [];
  if (!rows.length) view.innerHTML = '<tr><td colspan="4" class="empty">（视图空）</td></tr>';
  for (const r of rows) {
    const tr = document.createElement("tr");
    if (changed[r.group]) tr.className = changed[r.group];
    tr.innerHTML = "<td>" + r.group + "</td><td>" + r.count + "</td><td>" + fmt(r.sum) + "</td><td>" + fmt(r.avg) + "</td>";
    view.appendChild(tr);
  }
  // 被清理的分组以删除线行展示（仅本步）
  if (s) for (const e of s.events) if (e.type === "group_removed") {
    const tr = document.createElement("tr");
    tr.className = "ev-removed";
    tr.innerHTML = "<td>" + e.group + "</td><td>0</td><td>0</td><td>—</td>";
    view.appendChild(tr);
  }
  const recs = $("recs"); recs.innerHTML = "";
  const rl = s ? s.records : [];
  if (!rl.length) recs.innerHTML = '<tr><td colspan="3" class="empty">（无记录）</td></tr>';
  for (const r of rl) {
    const tr = document.createElement("tr");
    tr.innerHTML = "<td>#" + r.id + "</td><td>" + r.group + "</td><td>" + fmt(r.value) + "</td>";
    recs.appendChild(tr);
  }
}
function go(i) { cur = Math.max(-1, Math.min(N - 1, i)); render(); }
function stop() { if (timer) { clearInterval(timer); timer = null; $("play").textContent = "▶ 播放"; } }
$("first").onclick = () => { stop(); go(-1); };
$("prev").onclick = () => { stop(); go(cur - 1); };
$("next").onclick = () => { stop(); go(cur + 1); };
$("last").onclick = () => { stop(); go(N - 1); };
$("play").onclick = () => {
  if (timer) { stop(); return; }
  if (cur >= N - 1) go(-1);
  $("play").textContent = "⏸ 暂停";
  timer = setInterval(() => { if (cur >= N - 1) { stop(); return; } go(cur + 1); }, 900);
};
$("slider").oninput = e => { stop(); go(+e.target.value - 1); };
$("slider").max = N;
document.addEventListener("keydown", e => {
  if (e.key === "ArrowLeft") go(cur - 1); else if (e.key === "ArrowRight") go(cur + 1);
});
render();
</script>
</body>
</html>
`
