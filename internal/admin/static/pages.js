"use strict";
// Page renderers — loaded after app.js (shares its globals).

// ---------------------------------------------------------------- dashboard
async function renderDashboard(c) {
  pageHead(c, t("dashboard"));
  let info;
  try { info = await api("/info"); S.info = info; } catch (e) { info = S.info; }
  const v = info.vars || {};
  const grid = el('<div class="grid"></div>');
  const tiles = [
    [t("version"), info.version, ""],
    [t("sys_cpu"), (v.SystemCPUUsage || "0"), "%"],
    [t("proc_mem"), (v.MemoryUsageVal || "0"), v.MemoryUsageUnit || "MB"],
    [t("sys_mem"), (v.SystemMemoryUsageVal || "0"), (v.SystemMemoryUsageUnit || "") + " / " + (v.SystemTotalMemory || "")],
    [t("goroutines"), v.TotalCoroutines || "0", ""],
    [t("uptime"), v.Uptime || "", ""],
    [t("modules"), (info.modules || []).length, ""],
  ];
  for (const [k, val, unit] of tiles)
    grid.appendChild(el(`<div class="tile"><div class="k">${esc(k)}</div><div class="val">${esc(val)} <small>${esc(unit)}</small></div></div>`));
  c.appendChild(grid);

  // module status summary
  const card = el(`<div class="card"><h3>${t("module_status")}</h3></div>`);
  const tbl = el(`<table><thead><tr><th>${t("name")}</th><th>${t("status")}</th></tr></thead><tbody></tbody></table>`);
  const tb = tbl.querySelector("tbody");
  for (const m of info.modules) {
    const st = (info.status || {})[m.name];
    tb.appendChild(el(`<tr><td><a>${esc(modTitle(m.name))}</a></td><td>${summarizeStatus(m.name, st)}</td></tr>`));
    tb.lastChild.querySelector("a").onclick = () => go(customModules.includes(m.name) ? m.name : m.name);
  }
  card.appendChild(tbl); c.appendChild(card);
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

// ---------------------------------------------------------------- generic module (JSON config + status + actions)
const MODULE_ACTIONS = {
  ddns: [["update", "ID"]], ssl: [["issue", "ID"]], wol: [["wake", "ID"], ["probe", "ID"]],
  cron: [["run", "ID"]], ipdb: [["download", null]],
};

async function renderModule(c, name) {
  pageHead(c, modTitle(name), t("type_help"));
  const statusCard = el(`<div class="card"><h3>${t("status")} <button class="btn sec sm" style="margin-left:auto" id="rf">↻ ${t("refresh")}</button></h3><div id="stbody" class="muted">${t("loading")}</div></div>`);
  c.appendChild(statusCard);
  const loadStatus = async () => {
    try {
      const st = await api(`/m/${name}/status`);
      statusCard.querySelector("#stbody").innerHTML = "";
      statusCard.querySelector("#stbody").appendChild(renderStatusTable(name, st));
    } catch (e) { statusCard.querySelector("#stbody").textContent = e.message; }
  };
  statusCard.querySelector("#rf").onclick = loadStatus;
  loadStatus();

  // JSON config editor
  const editCard = el(`<div class="card"><h3>${t("raw_config")}</h3>
    <textarea class="code" id="cfg" spellcheck="false">${t("loading")}</textarea>
    <div class="err-text hidden" id="ce"></div>
    <div class="btn-row" style="margin-top:12px">
      <button class="btn" id="save">${t("save")}</button>
      <button class="btn sec" id="reload">${t("refresh")}</button>
      <span id="acts" class="btn-row"></span>
    </div>
  </div>`);
  c.appendChild(editCard);
  const ta = editCard.querySelector("#cfg");
  const loadCfg = async () => {
    const data = await api(`/m/${name}/config`);
    ta.value = JSON.stringify(data, null, 2);
  };
  loadCfg().catch(e => ta.value = e.message);
  editCard.querySelector("#reload").onclick = () => loadCfg();
  editCard.querySelector("#save").onclick = async () => {
    const err = editCard.querySelector("#ce"); err.classList.add("hidden");
    let parsed;
    try { parsed = JSON.parse(ta.value); } catch (e) { err.textContent = "JSON: " + e.message; err.classList.remove("hidden"); return; }
    const btn = editCard.querySelector("#save"); btn.disabled = true; btn.textContent = t("saving");
    try {
      await api(`/m/${name}/config`, { method: "PUT", json: parsed });
      toast(t("saved"), "ok"); loadStatus();
    } catch (e) { err.textContent = e.message; err.classList.remove("hidden"); }
    btn.disabled = false; btn.textContent = t("save");
  };

  // id-targeted actions
  const acts = editCard.querySelector("#acts");
  for (const [action, arg] of (MODULE_ACTIONS[name] || [])) {
    const b = el(`<button class="btn sec sm">${esc(action)}</button>`);
    b.onclick = async () => {
      let body = {};
      if (arg) { const id = prompt(`${action} — ${arg}:`); if (id == null) return; body[arg] = id; }
      try { const r = await api(`/m/${name}/action/${action}`, { method: "POST", json: body }); toast(t("op_ok") + " · " + JSON.stringify(r), "ok"); loadStatus(); }
      catch (e) { toast(e.message, "err"); }
    };
    acts.appendChild(b);
  }
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
    <button class="btn sec sm" id="rf">↻ ${t("refresh")}</button>
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
    <label class="chk"><input type="checkbox" id="AllowInternet"> ${t("allow_internet")}</label>
    <div class="btn-row" style="margin-top:12px"><button class="btn" id="save">${t("save")}</button></div>
  </div>`);
  const set = (id, val) => { const e = f.querySelector("#" + id); if (e.type === "checkbox") e.checked = !!val; else e.value = val == null ? "" : val; };
  ["Title", "AdminUser", "Port", "HTTPSPort", "ListenIP", "SafeEntrance", "AdminIPFilter", "SessionHours", "AllowInternet"].forEach(k => set(k, b[k]));
  f.querySelector("#save").onclick = async () => {
    const out = {
      Title: f.querySelector("#Title").value, AdminUser: f.querySelector("#AdminUser").value,
      Port: +f.querySelector("#Port").value, HTTPSPort: +f.querySelector("#HTTPSPort").value,
      ListenIP: f.querySelector("#ListenIP").value, SafeEntrance: f.querySelector("#SafeEntrance").value,
      AdminIPFilter: f.querySelector("#AdminIPFilter").value, SessionHours: +f.querySelector("#SessionHours").value,
      AllowInternet: f.querySelector("#AllowInternet").checked,
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
