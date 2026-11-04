"use strict";
// Page renderers — loaded after app.js (shares its globals).

// ---------------------------------------------------------------- dashboard
const DASH = { cpu: [], mem: [], net: [], timer: null, lastPf: null };

function toBytes(val, unit) {
  const n = parseFloat(val) || 0;
  const u = { B: 1, KB: 1024, MB: 1048576, GB: 1073741824, TB: 1099511627776 }[(unit || "B").toUpperCase()] || 1;
  return n * u;
}

async function renderDashboard(c) {
  pageHead(c, t("dashboard"));
  if (DASH.timer) { clearInterval(DASH.timer); DASH.timer = null; }

  // gauges row
  const gauges = el(`<div class="gauges">
    ${gaugeCard("cpu", t("sys_cpu"), "cpu")}
    ${gaugeCard("mem", t("sys_mem"), "mem")}
    <div class="tile stat"><div class="k">${icon("activity", 15)} ${t("proc_mem")}</div><div class="val" id="st-pmem">—</div><div class="sub" id="st-pmem2"></div></div>
    <div class="tile stat"><div class="k">${icon("layers", 15)} ${t("goroutines")}</div><div class="val" id="st-gor">—</div><div class="sub" id="st-ver"></div></div>
    <div class="tile stat"><div class="k">${icon("clock", 15)} ${t("uptime")}</div><div class="val" id="st-up" style="font-size:19px">—</div><div class="sub" id="st-mods"></div></div>
  </div>`);
  c.appendChild(gauges);

  // live chart
  const chartCard = el(`<div class="card"><h3>${icon("activity", 16)} ${t("live_usage") || "即時使用率"}
    <span class="legend" style="margin-left:auto">
      <span class="lg"><i style="background:var(--accent)"></i>CPU</span>
      <span class="lg"><i style="background:#22c55e"></i>${t("sys_mem")}</span>
    </span></h3>
    <canvas id="chart" height="180"></canvas></div>`);
  c.appendChild(chartCard);

  // module status
  const card = el(`<div class="card"><h3>${icon("layers", 16)} ${t("module_status")}</h3></div>`);
  const tbl = el(`<table><thead><tr><th>${t("name")}</th><th>${t("status")}</th></tr></thead><tbody></tbody></table>`);
  const tb = tbl.querySelector("tbody");
  card.appendChild(tbl); c.appendChild(card);

  const canvas = chartCard.querySelector("#chart");

  async function tick() {
    let info;
    try { info = await api("/info"); S.info = info; } catch (e) { return; }
    const v = info.vars || {};
    const cpu = Math.max(0, Math.min(100, parseFloat(v.SystemCPUUsage) || 0));
    const usedB = toBytes(v.SystemMemoryUsageVal, v.SystemMemoryUsageUnit);
    const totalB = toBytes(v.SystemTotalMemoryVal, v.SystemTotalMemoryUnit);
    const memPct = totalB ? Math.min(100, usedB / totalB * 100) : 0;
    DASH.cpu.push(cpu); DASH.mem.push(memPct);
    if (DASH.cpu.length > 60) { DASH.cpu.shift(); DASH.mem.shift(); }

    setGauge(gauges, "cpu", cpu, cpu.toFixed(0) + "%", "var(--accent)");
    setGauge(gauges, "mem", memPct, memPct.toFixed(0) + "%", "#22c55e");
    gauges.querySelector("#st-pmem").textContent = (v.MemoryUsageVal || "0") + " " + (v.MemoryUsageUnit || "");
    gauges.querySelector("#st-pmem2").textContent = (v.SystemMemoryUsage || "") + " / " + (v.SystemTotalMemory || "");
    gauges.querySelector("#st-gor").textContent = v.TotalCoroutines || "0";
    gauges.querySelector("#st-ver").textContent = "v" + info.version;
    gauges.querySelector("#st-up").textContent = v.Uptime || "";
    gauges.querySelector("#st-mods").textContent = (info.modules || []).length + " " + t("modules");

    drawChart(canvas, [
      { data: DASH.cpu, color: getCss("--accent") },
      { data: DASH.mem, color: "#22c55e" },
    ]);

    // module table
    tb.innerHTML = "";
    for (const m of info.modules) {
      const st = (info.status || {})[m.name];
      const tr = el(`<tr><td><a class="modlink">${icon(m.name, 15)} <span>${esc(modTitle(m.name))}</span></a></td><td>${summarizeStatus(m.name, st)}</td></tr>`);
      tr.querySelector("a").onclick = () => go(m.name);
      tb.appendChild(tr);
    }
  }
  await tick();
  DASH.timer = setInterval(() => { if (S.route === "dashboard") tick(); else { clearInterval(DASH.timer); DASH.timer = null; } }, 2500);
}

function gaugeCard(id, label, ic) {
  return `<div class="tile gauge">
    <svg viewBox="0 0 120 120" class="gsvg">
      <circle cx="60" cy="60" r="50" class="gbg"/>
      <circle cx="60" cy="60" r="50" class="gfg" id="g-${id}" transform="rotate(-90 60 60)"/>
    </svg>
    <div class="glabel"><div class="gval" id="gv-${id}">—</div><div class="gk">${icon(ic, 14)} ${esc(label)}</div></div>
  </div>`;
}
function setGauge(root, id, pct, text, color) {
  const circ = 2 * Math.PI * 50;
  const fg = root.querySelector("#g-" + id);
  fg.style.strokeDasharray = circ;
  fg.style.strokeDashoffset = circ * (1 - pct / 100);
  fg.style.stroke = color;
  root.querySelector("#gv-" + id).textContent = text;
}
function getCss(name) { return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || "#3b82f6"; }

function drawChart(canvas, series) {
  const dpr = window.devicePixelRatio || 1;
  const w = canvas.clientWidth || canvas.parentElement.clientWidth - 36;
  const h = 180;
  canvas.width = w * dpr; canvas.height = h * dpr;
  const ctx = canvas.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  const pad = { l: 34, r: 8, t: 10, b: 18 };
  const gw = w - pad.l - pad.r, gh = h - pad.t - pad.b;
  const grid = getCss("--line") || "#2a3340", fg2 = getCss("--fg2") || "#9aa7b4";
  ctx.strokeStyle = grid; ctx.fillStyle = fg2; ctx.lineWidth = 1; ctx.font = "11px system-ui";
  for (let i = 0; i <= 4; i++) {
    const y = pad.t + gh * i / 4;
    ctx.globalAlpha = .5; ctx.beginPath(); ctx.moveTo(pad.l, y); ctx.lineTo(w - pad.r, y); ctx.stroke(); ctx.globalAlpha = 1;
    ctx.fillText((100 - i * 25) + "%", 4, y + 4);
  }
  const N = 60;
  for (const s of series) {
    if (!s.data.length) continue;
    ctx.beginPath();
    s.data.forEach((val, i) => {
      const idx = i + (N - s.data.length);
      const x = pad.l + gw * idx / (N - 1);
      const y = pad.t + gh * (1 - Math.max(0, Math.min(100, val)) / 100);
      i ? ctx.lineTo(x, y) : ctx.moveTo(x, y);
    });
    ctx.strokeStyle = s.color; ctx.lineWidth = 2; ctx.lineJoin = "round"; ctx.stroke();
    // area fill
    const last = s.data.length - 1;
    const lastIdx = last + (N - s.data.length);
    ctx.lineTo(pad.l + gw * lastIdx / (N - 1), pad.t + gh);
    ctx.lineTo(pad.l + gw * (N - s.data.length) / (N - 1), pad.t + gh);
    ctx.closePath();
    ctx.globalAlpha = .08; ctx.fillStyle = s.color; ctx.fill(); ctx.globalAlpha = 1;
  }
}

const customModules = ["cloudflare", "docker", "webterminal"];

function summarizeStatus(name, st) {
  if (!st || typeof st !== "object") return `<span class="muted">—</span>`;
  const n = v => Array.isArray(v) ? v.length : v;
  if (st.enable !== undefined) {
    if (st.connected !== undefined) return st.connected ? tag(true, t("running") + (st.containers != null ? ` · ${st.running}/${st.containers}` : "")) : tag(false, st.error ? t("error") : t("stopped"));
    if (st.running !== undefined) return st.enable ? tag(st.running) : tag(false, t("disabled"));
    return tag(st.enable, st.enable ? t("enable") : t("disabled"));
  }
  const counts = [];
  for (const k of ["rules", "tasks", "jobs", "tunnels", "instances", "accounts", "certs", "devices", "mounts", "profiles", "groups", "grants", "lists"])
    if (st[k] != null) counts.push(`${n(st[k])} ${k}`);
  if (st.loaded !== undefined) counts.unshift(st.loaded ? "DB ✓" : "DB ✗");
  return counts.length ? `<span class="fg2">${esc(counts.join(" · "))}</span>` : `<span class="muted">—</span>`;
}

// ---------------------------------------------------------------- generic module page
// Per-row action buttons shown in the module's list (schema-driven GUI).
const ROW_ACTIONS = {
  ddns: [{ action: "update", label: { zh: "更新", en: "Update" }, icon: "refresh" }],
  ssl: [{ action: "issue", label: { zh: "簽發", en: "Issue" }, icon: "ssl" }],
  wol: [{ action: "wake", label: { zh: "喚醒", en: "Wake" }, icon: "wol" }, { action: "probe", label: { zh: "探測", en: "Probe" } }],
  cron: [{ action: "run", label: { zh: "執行", en: "Run" }, icon: "play" }],
};
// Module-level action buttons (no per-row target) shown in the status header.
const HEAD_ACTIONS = {
  ipdb: [{ action: "download", label: { zh: "下載資料庫", en: "Download DB" }, icon: "backup" }],
};

// Index status rows by ID so rule rows can show live state (running, traffic…).
function indexStatus(st) {
  const m = {};
  if (!st || typeof st !== "object") return m;
  for (const k of Object.keys(st)) if (Array.isArray(st[k])) for (const r of st[k]) if (r && r.ID) m[r.ID] = r;
  return m;
}

function showLogs(name) {
  const box = el(`<div class="logbox">${t("loading")}</div>`);
  const load = async () => {
    try {
      const logs = await api("/logs?n=300&module=" + encodeURIComponent(name));
      box.innerHTML = logs.length ? logs.map(e => `<div><span class="log-t">${esc((e.time || "").replace("T", " ").slice(0, 19))}</span> <span class="log-${esc(e.level)}">${esc(e.msg)}</span></div>`).join("") : `<span class="log-t">${t("none")}</span>`;
      box.scrollTop = box.scrollHeight;
    } catch (e) { box.textContent = e.message; }
  };
  const foot = el(`<div class="btn-row"><button class="btn sec">${icon("refresh", 14)} ${t("refresh")}</button></div>`);
  foot.querySelector("button").onclick = load;
  modal(modTitle(name) + " · " + t("run_logs"), box, null, foot);
  load();
}

async function renderModule(c, name, embedded) {
  let headActs = null;
  if (!embedded) {
    const isList = !!(SCHEMA[name] && SCHEMA[name].list);
    const head = el(`<div class="phead"><span class="ptab">${icon(name, 16)} ${esc(modTitle(name))} · ${esc(isList ? t("rule_list") : t("config"))}</span>
      <span class="btn-row hacts"></span><span class="sp-auto"></span><span class="btn-row tools"></span></div>`);
    c.appendChild(head);
    headActs = head.querySelector(".hacts");
    const tools = head.querySelector(".tools");
    for (const ha of (HEAD_ACTIONS[name] || [])) {
      const b = el(`<button class="btn plain">${ha.icon ? icon(ha.icon, 13) : ""} ${esc(tr(ha.label))}</button>`);
      b.onclick = async () => { b.disabled = true; try { const r = await api(`/m/${name}/action/${ha.action}`, { method: "POST", json: {} }); toast(t("op_ok") + (r && r.path ? " · " + r.path : ""), "ok"); render(); } catch (e) { toast(e.message, "err"); } b.disabled = false; };
      headActs.appendChild(b);
    }
    const lb = el(`<button class="btn sec">${icon("logs", 14)} ${t("run_logs")}</button>`); lb.onclick = () => showLogs(name);
    const rb = el(`<button class="btn sec">${icon("refresh", 14)} ${t("refresh")}</button>`); rb.onclick = () => render();
    tools.appendChild(lb); tools.appendChild(rb);
  }

  let st = null, cfg;
  try {
    [st, cfg] = await Promise.all([api(`/m/${name}/status`).catch(() => null), api(`/m/${name}/config`)]);
  } catch (e) { c.appendChild(el(`<div class="card err-text">${esc(e.message)}</div>`)); return; }

  const handled = await renderModuleForm(c, name, cfg, () => render(), { rowActions: ROW_ACTIONS[name], headActs, statusByID: indexStatus(st) });

  // Detailed runtime status (open by default for modules without a list).
  if (!embedded && st) {
    const sc = SCHEMA[name];
    const det = el(`<details class="card" ${!(sc && sc.list) ? "open" : ""}><summary>${icon("activity", 14)} ${t("detail_status")}</summary><div></div></details>`);
    det.querySelector("div").appendChild(renderStatusTable(name, st));
    c.appendChild(det);
  }

  // Advanced raw-JSON editor (collapsed).
  const adv = el(`<details class="card adv"><summary>${icon("globalvars", 14)} ${t("raw_config")}</summary>
    <textarea class="code" id="cfg" spellcheck="false"></textarea>
    <div class="err-text hidden" id="ce"></div>
    <div class="btn-row" style="margin-top:12px"><button class="btn" id="save">${t("save")}</button></div></details>`);
  c.appendChild(adv);
  const ta = adv.querySelector("#cfg");
  const fill = () => ta.value = JSON.stringify(cfg, null, 2);
  adv.querySelector("summary").addEventListener("click", () => setTimeout(fill, 0), { once: true });
  adv.querySelector("#save").onclick = async () => {
    const err = adv.querySelector("#ce"); err.classList.add("hidden");
    let parsed;
    try { parsed = JSON.parse(ta.value); } catch (e) { err.textContent = "JSON: " + e.message; err.classList.remove("hidden"); return; }
    try { await api(`/m/${name}/config`, { method: "PUT", json: parsed }); toast(t("saved"), "ok"); render(); }
    catch (e) { err.textContent = e.message; err.classList.remove("hidden"); }
  };
  if (!handled) { adv.open = true; fill(); }
}

function renderStatusTable(name, st) {
  if (!st || typeof st !== "object") return el(`<span class="muted">${t("none")}</span>`);
  // find the first array field => render as table
  const arrKey = Object.keys(st).find(k => Array.isArray(st[k]));
  const wrap = el("<div></div>");
  // scalar fields
  const scal = Object.entries(st).filter(([k, v]) => !Array.isArray(v) && typeof v !== "object");
  if (scal.length) {
    const p = el('<div class="inline" style="margin-bottom:10px;gap:16px"></div>');
    for (const [k, v] of scal) {
      let disp = v;
      if (k === "connected" || k === "enable" || k === "running" || k === "loaded") disp = v ? "✓" : "✗";
      p.appendChild(el(`<span class="fg2"><b>${esc(k)}</b>: ${esc(disp)}</span>`));
    }
    wrap.appendChild(p);
  }
  if (arrKey && st[arrKey].length) {
    const rows = st[arrKey];
    const cols = Object.keys(rows[0]).filter(k => typeof rows[0][k] !== "object" || rows[0][k] === null);
    const tbl = el("<table><thead><tr></tr></thead><tbody></tbody></table>");
    const hr = tbl.querySelector("tr");
    for (const col of cols) hr.appendChild(el(`<th>${esc(col)}</th>`));
    const tb = tbl.querySelector("tbody");
    for (const row of rows) {
      const tr = el("<tr></tr>");
      for (const col of cols) {
        let v = row[col];
        if (typeof v === "boolean") v = v ? tag(true) : tag(false, "✗");
        else if (col.toLowerCase().includes("byte")) v = bytes(v);
        else v = `<span class="${/addr|ip|target|listen|domain|backend/i.test(col) ? "mono" : ""}">${esc(v == null ? "" : v)}</span>`;
        tr.appendChild(el(`<td>${v}</td>`));
      }
      tb.appendChild(tr);
    }
    wrap.appendChild(tbl);
  } else if (arrKey) {
    wrap.appendChild(el(`<span class="muted">${t("none")}</span>`));
  }
  return wrap;
}

// ---------------------------------------------------------------- logs
async function renderLogs(c) {
  pageHead(c, t("logs"));
  const bar = el(`<div class="card"><div class="inline">
    <select id="mod" style="max-width:240px"><option value="">${t("all_modules")}</option></select>
    <button class="btn sec sm" id="rf">${icon("refresh",14)} ${t("refresh")}</button>
    <label class="chk" style="margin:0"><input type="checkbox" id="auto" checked> auto</label>
  </div><div class="sp"></div><div class="logbox" id="box"></div></div>`);
  c.appendChild(bar);
  const sel = bar.querySelector("#mod");
  for (const m of S.info.modules) sel.appendChild(el(`<option value="${esc(m.name)}">${esc(modTitle(m.name))}</option>`));
  const box = bar.querySelector("#box");
  const load = async () => {
    try {
      const logs = await api("/logs?n=400&module=" + encodeURIComponent(sel.value));
      box.innerHTML = logs.map(e =>
        `<div><span class="log-t">${esc((e.time || "").replace("T", " ").slice(0, 19))}</span> ` +
        `<span class="log-${esc(e.level)}">[${esc(e.module)}] ${esc(e.msg)}</span></div>`).join("");
      box.scrollTop = box.scrollHeight;
    } catch (e) { box.textContent = e.message; }
  };
  sel.onchange = load; bar.querySelector("#rf").onclick = load;
  load();
  const timer = setInterval(() => { if (S.route === "logs" && bar.querySelector("#auto").checked) load(); else if (S.route !== "logs") clearInterval(timer); }, 4000);
}

// ---------------------------------------------------------------- global vars
async function renderVars(c) {
  pageHead(c, t("globalvars"), "{Name} → value");
  const v = (await api("/vars"));
  const card = el(`<div class="card"></div>`);
  const tbl = el(`<table><thead><tr><th>${t("name")}</th><th>${t("value")}</th></tr></thead><tbody></tbody></table>`);
  const tb = tbl.querySelector("tbody");
  for (const k of Object.keys(v).sort())
    tb.appendChild(el(`<tr><td class="mono">{${esc(k)}}</td><td>${esc(v[k])}</td></tr>`));
  card.appendChild(tbl); c.appendChild(card);
}

// ---------------------------------------------------------------- settings
async function renderSettings(c) {
  pageHead(c, t("settings"));
  const b = await api("/base");
  const f = el(`<div class="card"><p class="page-desc">${t("restart_note")}</p>
    <div class="row">
      <label class="f"><span>${t("panel_title")}</span><input id="Title" type="text"></label>
      <label class="f"><span>${t("admin_user")}</span><input id="AdminUser" type="text"></label>
    </div>
    <div class="row">
      <label class="f"><span>${t("http_port")}</span><input id="Port" type="number"></label>
      <label class="f"><span>${t("https_port")}</span><input id="HTTPSPort" type="number"></label>
      <label class="f"><span>${t("listen_ip")}</span><input id="ListenIP" type="text"></label>
    </div>
    <div class="row">
      <label class="f"><span>${t("safe_entrance")}</span><input id="SafeEntrance" type="text" placeholder="/"></label>
      <label class="f"><span>${t("admin_ipfilter")}</span><input id="AdminIPFilter" type="text"></label>
      <label class="f"><span>${t("session_hours")}</span><input id="SessionHours" type="number"></label>
    </div>
    <label class="chk"><input type="checkbox" id="LANOnly"> ${t("lan_only")}</label>
    <div class="btn-row" style="margin-top:12px"><button class="btn" id="save">${t("save")}</button></div>
  </div>`);
  const set = (id, val) => { const e = f.querySelector("#" + id); if (e.type === "checkbox") e.checked = !!val; else e.value = val == null ? "" : val; };
  ["Title", "AdminUser", "Port", "HTTPSPort", "ListenIP", "SafeEntrance", "AdminIPFilter", "SessionHours", "LANOnly"].forEach(k => set(k, b[k]));
  f.querySelector("#save").onclick = async () => {
    const out = {
      Title: f.querySelector("#Title").value, AdminUser: f.querySelector("#AdminUser").value,
      Port: +f.querySelector("#Port").value, HTTPSPort: +f.querySelector("#HTTPSPort").value,
      ListenIP: f.querySelector("#ListenIP").value, SafeEntrance: f.querySelector("#SafeEntrance").value,
      AdminIPFilter: f.querySelector("#AdminIPFilter").value, SessionHours: +f.querySelector("#SessionHours").value,
      LANOnly: f.querySelector("#LANOnly").checked,
    };
    try { const r = await api("/base", { method: "PUT", json: out }); toast(t("saved"), "ok"); if (r.restart) toast(t("restart_note"), ""); }
    catch (e) { toast(e.message, "err"); }
  };
  c.appendChild(f);

  // password
  const pw = el(`<div class="card"><h3>${t("change_pass")}</h3>
    <div class="row">
      <label class="f"><span>${t("old_pass")}</span><input id="o" type="password"></label>
      <label class="f"><span>${t("new_pass")}</span><input id="n" type="password"></label>
    </div>
    <div class="btn-row"><button class="btn" id="cp">${t("change_pass")}</button></div></div>`);
  pw.querySelector("#cp").onclick = async () => {
    try { await api("/base/password", { method: "POST", json: { Old: pw.querySelector("#o").value, New: pw.querySelector("#n").value } }); toast(t("pass_changed"), "ok"); pw.querySelector("#o").value = pw.querySelector("#n").value = ""; }
    catch (e) { toast(e.message, "err"); }
  };
  c.appendChild(pw);
}

// ---------------------------------------------------------------- backup
function renderBackup(c) {
  pageHead(c, t("backup"), t("backup_desc"));
  const card = el(`<div class="card"><div class="btn-row">
    <a class="btn" href="${BASE}/api/backup" download>${t("export_enc")}</a>
    <a class="btn sec" href="${BASE}/api/backup?plain=1" download>${t("export_plain")}</a>
  </div></div>`);
  c.appendChild(card);
  const imp = el(`<div class="card"><h3>${t("import_backup")}</h3>
    <p class="page-desc">${t("restore_warn")}</p>
    <label class="f"><span>${t("choose_file")}</span><input id="file" type="file" accept=".zip"></label>
    <label class="f"><span>${t("passphrase")}</span><input id="pp" type="password"></label>
    <div class="btn-row"><button class="btn danger" id="imp">${t("import_backup")}</button></div></div>`);
  imp.querySelector("#imp").onclick = async () => {
    const file = imp.querySelector("#file").files[0];
    if (!file) { toast(t("choose_file"), "err"); return; }
    const fd = new FormData(); fd.append("file", file); fd.append("passphrase", imp.querySelector("#pp").value);
    try {
      const r = await fetch(BASE + "/api/restore", { method: "POST", headers: { "X-Requested-With": "al1add" }, body: fd });
      const d = await r.json(); if (!r.ok) throw new Error(d.error || "HTTP " + r.status);
      toast(t("restore_done") + ": " + (d.restored || []).join(", "), "ok");
    } catch (e) { toast(e.message, "err"); }
  };
  c.appendChild(imp);
}
