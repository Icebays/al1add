"use strict";
// Cloudflare manager, Docker and Web terminal pages.

function cfAction(action, body) { return api(`/m/cloudflare/action/${action}`, { method: "POST", json: body || {} }); }

async function renderCloudflare(c) {
  pageHead(c, modTitle("cloudflare"), "Cloudflare API");
  // --- accounts (stored config) ---
  const cfg = await api("/m/cloudflare/config");
  const accCard = el(`<div class="card"><h3>${t("cf_accounts")} <button class="btn sm" style="margin-left:auto" id="addacc">+ ${t("cf_add_account")}</button></h3><div id="acclist"></div></div>`);
  c.appendChild(accCard);
  const renderAccs = () => {
    const list = accCard.querySelector("#acclist"); list.innerHTML = "";
    if (!cfg.Accounts || !cfg.Accounts.length) { list.appendChild(el(`<p class="muted">${t("none")}</p>`)); return; }
    const tbl = el(`<table><thead><tr><th>${t("name")}</th><th>${t("cf_authtype")}</th><th>${t("status")}</th><th></th></tr></thead><tbody></tbody></table>`);
    const tb = tbl.querySelector("tbody");
    for (const a of cfg.Accounts) {
      const tr = el(`<tr><td>${esc(a.Name)}</td><td>${esc(a.Type)}</td><td class="fg2">${esc(a.LastStatus || "")}</td>
        <td class="actions"><button class="btn sec sm" data-v>✓ ${t("verify")}</button> <button class="btn danger sm" data-d>✕</button></td></tr>`);
      tr.querySelector("[data-v]").onclick = async () => { try { const r = await cfAction("verify", { AccountID: a.ID }); toast(t("op_ok") + ": " + r.status, "ok"); } catch (e) { toast(e.message, "err"); } };
      tr.querySelector("[data-d]").onclick = async () => { if (!confirm(t("confirm_delete"))) return; cfg.Accounts = cfg.Accounts.filter(x => x.ID !== a.ID); await saveCfg(); renderAccs(); fillAccSelect(); };
      tb.appendChild(tr);
    }
    list.appendChild(tbl);
  };
  const saveCfg = () => api("/m/cloudflare/config", { method: "PUT", json: cfg });
  accCard.querySelector("#addacc").onclick = () => {
    const body = el(`<div>
      <label class="f"><span>${t("name")}</span><input id="n" type="text"></label>
      <label class="f"><span>${t("cf_authtype")}</span><select id="ty"><option value="token">API Token</option><option value="key">Global API Key</option></select></label>
      <div id="tok"><label class="f"><span>${t("cf_token")}</span><input id="token" type="password"></label></div>
      <div id="key" class="hidden"><div class="row">
        <label class="f"><span>${t("cf_email")}</span><input id="email" type="text"></label>
        <label class="f"><span>${t("cf_apikey")}</span><input id="apikey" type="password"></label></div></div>
      <div class="btn-row" style="margin-top:12px"><button class="btn" id="ok">${t("save")}</button><button class="btn sec" id="cx">${t("cancel")}</button></div>
    </div>`);
    const ov = modal(t("cf_add_account"), body);
    body.querySelector("#ty").onchange = e => { body.querySelector("#tok").classList.toggle("hidden", e.target.value !== "token"); body.querySelector("#key").classList.toggle("hidden", e.target.value !== "key"); };
    body.querySelector("#cx").onclick = () => ov.remove();
    body.querySelector("#ok").onclick = async () => {
      const ty = body.querySelector("#ty").value;
      const a = { Name: body.querySelector("#n").value || "Cloudflare", Type: ty, Token: body.querySelector("#token").value, Email: body.querySelector("#email").value, APIKey: body.querySelector("#apikey").value };
      cfg.Accounts = cfg.Accounts || []; cfg.Accounts.push(a);
      try { await saveCfg(); ov.remove(); renderAccs(); fillAccSelect(); toast(t("saved"), "ok"); } catch (e) { toast(e.message, "err"); }
    };
  };
  renderAccs();

  // --- working area: account + zone selectors ---
  const work = el(`<div class="card"><div class="inline">
    <select id="acc" style="max-width:220px"><option value="">${t("cf_select_account")}</option></select>
    <select id="zone" style="max-width:260px"><option value="">${t("cf_select_zone")}</option></select>
    <span class="inline" id="tabs"></span>
  </div><div class="sp"></div><div id="cfbody" class="muted">${t("cf_select_account")}</div></div>`);
  c.appendChild(work);
  const accSel = work.querySelector("#acc"), zoneSel = work.querySelector("#zone"), body = work.querySelector("#cfbody");
  function fillAccSelect() {
    accSel.innerHTML = `<option value="">${t("cf_select_account")}</option>`;
    for (const a of (cfg.Accounts || [])) accSel.appendChild(el(`<option value="${esc(a.ID)}">${esc(a.Name)}</option>`));
  }
  fillAccSelect();

  const tabs = ["cf_records", "cf_purge", "cf_settings", "cf_access"];
  let curTab = "cf_records";
  const tabBar = work.querySelector("#tabs");
  const renderTabs = () => {
    tabBar.innerHTML = "";
    for (const tk of tabs) {
      const b = el(`<span class="pill-btn ${curTab === tk ? "active" : ""}">${t(tk)}</span>`);
      b.onclick = () => { curTab = tk; renderTabs(); loadTab(); };
      tabBar.appendChild(b);
    }
  };

  accSel.onchange = async () => {
    zoneSel.innerHTML = `<option value="">${t("loading")}</option>`;
    body.textContent = "";
    if (!accSel.value) return;
    try {
      const r = await cfAction("zones", { AccountID: accSel.value });
      zoneSel.innerHTML = `<option value="">${t("cf_select_zone")}</option>`;
      for (const z of r.zones) zoneSel.appendChild(el(`<option value="${esc(z.id)}">${esc(z.name)} (${esc(z.status)})</option>`));
      renderTabs();
    } catch (e) { toast(e.message, "err"); zoneSel.innerHTML = `<option value="">${t("cf_select_zone")}</option>`; }
  };
  zoneSel.onchange = () => { renderTabs(); loadTab(); };

  function acc() { return accSel.value; }
  function zone() { return zoneSel.value; }

  async function loadTab() {
    if (!zone()) { body.innerHTML = `<span class="muted">${t("cf_select_zone")}</span>`; return; }
    body.innerHTML = `<span class="muted">${t("loading")}</span>`;
    try {
      if (curTab === "cf_records") return loadRecords();
      if (curTab === "cf_purge") return loadPurge();
      if (curTab === "cf_settings") return loadSettings();
      if (curTab === "cf_access") return loadAccess();
    } catch (e) { body.innerHTML = `<span class="err-text">${esc(e.message)}</span>`; }
  }

  // --- DNS records ---
  async function loadRecords() {
    const r = await cfAction("records", { AccountID: acc(), ZoneID: zone() });
    body.innerHTML = "";
    const add = el(`<div class="btn-row" style="margin-bottom:12px"><button class="btn sm" id="addr">+ ${t("cf_add_record")}</button></div>`);
    body.appendChild(add);
    add.querySelector("#addr").onclick = () => recordModal(null);
    const tbl = el(`<table><thead><tr><th>${t("type")}</th><th>${t("name")}</th><th>${t("cf_content")}</th><th>TTL</th><th>${t("cf_proxied")}</th><th></th></tr></thead><tbody></tbody></table>`);
    const tb = tbl.querySelector("tbody");
    for (const rec of r.records) {
      const tr = el(`<tr><td>${esc(rec.type)}</td><td class="mono">${esc(rec.name)}</td><td class="mono">${esc(rec.content)}</td>
        <td>${rec.ttl === 1 ? "auto" : rec.ttl}</td><td>${rec.proxied ? "🟠" : "⚪"}</td>
        <td class="actions"><button class="btn sec sm" data-e>${t("edit")}</button> <button class="btn danger sm" data-d>✕</button></td></tr>`);
      tr.querySelector("[data-e]").onclick = () => recordModal(rec);
      tr.querySelector("[data-d]").onclick = async () => { if (!confirm(t("confirm_delete"))) return; try { await cfAction("record_delete", { AccountID: acc(), ZoneID: zone(), RecordID: rec.id }); toast(t("op_ok"), "ok"); loadRecords(); } catch (e) { toast(e.message, "err"); } };
      tb.appendChild(tr);
    }
    body.appendChild(tbl);
  }
  function recordModal(rec) {
    const isNew = !rec;
    const m = el(`<div>
      <div class="row">
        <label class="f"><span>${t("type")}</span><select id="type">${["A", "AAAA", "CNAME", "TXT", "MX", "NS", "SRV", "CAA"].map(x => `<option ${rec && rec.type === x ? "selected" : ""}>${x}</option>`).join("")}</select></label>
        <label class="f"><span>${t("name")}</span><input id="name" type="text" value="${esc(rec ? rec.name : "")}"></label>
      </div>
      <label class="f"><span>${t("cf_content")}</span><input id="content" type="text" value="${esc(rec ? rec.content : "")}"></label>
      <div class="row">
        <label class="f"><span>TTL (1=auto)</span><input id="ttl" type="number" value="${rec ? rec.ttl : 1}"></label>
        <label class="f"><span>${t("cf_priority")}</span><input id="prio" type="number" value="${rec && rec.priority != null ? rec.priority : ""}"></label>
      </div>
      <label class="chk"><input type="checkbox" id="proxied" ${rec && rec.proxied ? "checked" : ""}> ${t("cf_proxied")}</label>
      <div class="btn-row" style="margin-top:12px"><button class="btn" id="ok">${t("save")}</button><button class="btn sec" id="cx">${t("cancel")}</button></div>
    </div>`);
    const ov = modal(isNew ? t("cf_add_record") : t("edit"), m);
    m.querySelector("#cx").onclick = () => ov.remove();
    m.querySelector("#ok").onclick = async () => {
      const record = { Type: m.querySelector("#type").value, Name: m.querySelector("#name").value, Content: m.querySelector("#content").value, TTL: +m.querySelector("#ttl").value || 1, Proxied: m.querySelector("#proxied").checked };
      const pr = m.querySelector("#prio").value; if (pr !== "") record.Priority = +pr;
      try {
        if (isNew) await cfAction("record_create", { AccountID: acc(), ZoneID: zone(), Record: record });
        else await cfAction("record_update", { AccountID: acc(), ZoneID: zone(), RecordID: rec.id, Record: record });
        ov.remove(); toast(t("saved"), "ok"); loadRecords();
      } catch (e) { toast(e.message, "err"); }
    };
  }

  // --- purge cache ---
  function loadPurge() {
    body.innerHTML = "";
    const card = el(`<div>
      <div class="btn-row"><button class="btn danger" id="all">${t("cf_purge_all")}</button></div>
      <div class="sp"></div>
      <label class="f"><span>URLs (${t("cf_purge")}, one per line)</span><textarea id="files" placeholder="https://example.com/a.js"></textarea></label>
      <div class="btn-row"><button class="btn" id="files_btn">${t("cf_purge")}</button></div>
    </div>`);
    body.appendChild(card);
    card.querySelector("#all").onclick = async () => { if (!confirm(t("cf_purge_all") + "?")) return; try { await cfAction("purge", { AccountID: acc(), ZoneID: zone(), All: true }); toast(t("op_ok"), "ok"); } catch (e) { toast(e.message, "err"); } };
    card.querySelector("#files_btn").onclick = async () => {
      const files = card.querySelector("#files").value.split("\n").map(s => s.trim()).filter(Boolean);
      if (!files.length) return; try { await cfAction("purge", { AccountID: acc(), ZoneID: zone(), Files: files }); toast(t("op_ok"), "ok"); } catch (e) { toast(e.message, "err"); }
    };
  }

  // --- zone settings ---
  async function loadSettings() {
    const r = await cfAction("settings", { AccountID: acc(), ZoneID: zone() });
    body.innerHTML = "";
    const quick = el(`<div class="card" style="background:var(--bg3)"><div class="row">
      <label class="f"><span>${t("cf_security")}</span><select id="sec">${["off", "essentially_off", "low", "medium", "high", "under_attack"].map(x => `<option>${x}</option>`).join("")}</select></label>
      <label class="f"><span>SSL</span><select id="ssl">${["off", "flexible", "full", "strict"].map(x => `<option>${x}</option>`).join("")}</select></label>
      <label class="f"><span>${t("cf_devmode")}</span><select id="dev"><option value="off">off</option><option value="on">on</option></select></label>
    </div><div class="btn-row"><button class="btn" id="apply">${t("apply")}</button></div></div>`);
    const findVal = id => { const s = (r.settings || []).find(x => x.id === id); return s ? s.value : ""; };
    body.appendChild(quick);
    quick.querySelector("#sec").value = findVal("security_level") || "medium";
    quick.querySelector("#ssl").value = findVal("ssl") || "full";
    quick.querySelector("#dev").value = findVal("development_mode") || "off";
    quick.querySelector("#apply").onclick = async () => {
      try {
        await cfAction("setting_set", { AccountID: acc(), ZoneID: zone(), Setting: "security_level", Value: quick.querySelector("#sec").value });
        await cfAction("setting_set", { AccountID: acc(), ZoneID: zone(), Setting: "ssl", Value: quick.querySelector("#ssl").value });
        await cfAction("devmode", { AccountID: acc(), ZoneID: zone(), On: quick.querySelector("#dev").value === "on" });
        toast(t("saved"), "ok");
      } catch (e) { toast(e.message, "err"); }
    };
    // full settings table
    const tbl = el(`<table><thead><tr><th>ID</th><th>${t("value")}</th></tr></thead><tbody></tbody></table>`);
    const tb = tbl.querySelector("tbody");
    for (const s of (r.settings || [])) tb.appendChild(el(`<tr><td class="mono">${esc(s.id)}</td><td class="mono">${esc(typeof s.value === "object" ? JSON.stringify(s.value) : s.value)}</td></tr>`));
    body.appendChild(tbl);
  }

  // --- IP access rules ---
  async function loadAccess() {
    const r = await cfAction("access_rules", { AccountID: acc(), ZoneID: zone() });
    body.innerHTML = "";
    const add = el(`<div class="card" style="background:var(--bg3)"><div class="row">
      <label class="f"><span>${t("cf_mode")}</span><select id="mode">${["block", "challenge", "js_challenge", "managed_challenge", "whitelist"].map(x => `<option>${x}</option>`).join("")}</select></label>
      <label class="f"><span>${t("cf_target")}</span><select id="target"><option value="ip">ip</option><option value="ip_range">ip_range</option><option value="country">country</option><option value="asn">asn</option></select></label>
      <label class="f"><span>${t("value")}</span><input id="value" type="text" placeholder="1.2.3.4 / US / AS13335"></label>
    </div><label class="f"><span>${t("cf_notes")}</span><input id="notes" type="text"></label>
    <div class="btn-row"><button class="btn" id="addr">+ ${t("cf_add_rule")}</button></div></div>`);
    body.appendChild(add);
    add.querySelector("#addr").onclick = async () => {
      try { await cfAction("access_rule_create", { AccountID: acc(), ZoneID: zone(), Mode: add.querySelector("#mode").value, Target: add.querySelector("#target").value, Value: add.querySelector("#value").value, Notes: add.querySelector("#notes").value }); toast(t("op_ok"), "ok"); loadAccess(); } catch (e) { toast(e.message, "err"); }
    };
    const tbl = el(`<table><thead><tr><th>${t("cf_mode")}</th><th>${t("cf_target")}</th><th>${t("value")}</th><th>${t("cf_notes")}</th><th></th></tr></thead><tbody></tbody></table>`);
    const tb = tbl.querySelector("tbody");
    for (const rule of (r.rules || [])) {
      const tr = el(`<tr><td>${esc(rule.mode)}</td><td>${esc(rule.configuration.target)}</td><td class="mono">${esc(rule.configuration.value)}</td><td class="fg2">${esc(rule.notes || "")}</td>
        <td class="actions"><button class="btn danger sm" data-d>✕</button></td></tr>`);
      tr.querySelector("[data-d]").onclick = async () => { if (!confirm(t("confirm_delete"))) return; try { await cfAction("access_rule_delete", { AccountID: acc(), ZoneID: zone(), RuleID: rule.id }); toast(t("op_ok"), "ok"); loadAccess(); } catch (e) { toast(e.message, "err"); } };
      tb.appendChild(tr);
    }
    body.appendChild(tbl);
  }
}

// ---------------------------------------------------------------- Docker
async function renderDocker(c) {
  pageHead(c, modTitle("docker"));
  const st = await api("/m/docker/status").catch(e => ({ error: e.message }));
  if (!st.enable) { c.appendChild(el(`<div class="card"><p class="muted">${t("disabled")} — ${t("config")} → Enable</p></div>`)); return renderModule(cSub(c), "docker"); }
  const bar = el(`<div class="card"><div class="inline">
    <span>${st.connected ? tag(true, t("running") + ` · ${st.running}/${st.containers}`) : tag(false, st.error || t("stopped"))}</span>
    <button class="btn sec sm" id="rf" style="margin-left:auto">↻ ${t("refresh")}</button>
  </div></div>`);
  c.appendChild(bar);
  const cont = el(`<div class="card"><h3>🐳 Containers</h3><div id="ps" class="muted">${t("loading")}</div></div>`);
  c.appendChild(cont);
  const loadPs = async () => {
    try {
      const r = await api("/m/docker/action/ps", { method: "POST", json: {} });
      const box = cont.querySelector("#ps"); box.innerHTML = "";
      const tbl = el(`<table><thead><tr><th>${t("name")}</th><th>Image</th><th>${t("status")}</th><th>Ports</th><th></th></tr></thead><tbody></tbody></table>`);
      const tb = tbl.querySelector("tbody");
      for (const ct of r.containers) {
        const run = ct.state === "running";
        const tr = el(`<tr><td>${esc(ct.name)}</td><td class="mono">${esc(ct.image)}</td><td>${tag(run, ct.status)}</td><td class="mono">${esc((ct.ports || []).join(", "))}</td>
          <td class="actions">
            <button class="btn sec sm" data-a="logs">log</button>
            <button class="btn sec sm" data-a="restart">⟳</button>
            <button class="btn ${run ? "danger" : ""} sm" data-a="${run ? "stop" : "start"}">${run ? "停" : "啟"}</button>
          </td></tr>`);
        tr.querySelectorAll("[data-a]").forEach(b => b.onclick = async () => {
          const act = b.dataset.a;
          if (act === "logs") { try { const lr = await api("/m/docker/action/logs", { method: "POST", json: { ID: ct.id, Tail: 300 } }); const pre = el(`<pre class="logbox" style="height:420px"></pre>`); pre.textContent = lr.logs || "(empty)"; modal(ct.name, pre); } catch (e) { toast(e.message, "err"); } return; }
          try { await api(`/m/docker/action/${act}`, { method: "POST", json: { ID: ct.id } }); toast(t("op_ok"), "ok"); loadPs(); } catch (e) { toast(e.message, "err"); }
        });
        tb.appendChild(tr);
      }
      box.appendChild(tbl);
    } catch (e) { cont.querySelector("#ps").textContent = e.message; }
  };
  bar.querySelector("#rf").onclick = loadPs; loadPs();
  // config at the bottom
  renderModule(cSub(c), "docker");
}
// helper: append a sub-container so renderModule can add its own cards
function cSub(c) { const d = el("<div></div>"); c.appendChild(d); return d; }

// ---------------------------------------------------------------- Web terminal
async function renderTerminal(c) {
  pageHead(c, modTitle("webterminal"));
  const st = await api("/m/webterminal/status").catch(() => ({}));
  if (!st.enable) {
    c.appendChild(el(`<div class="card"><p class="muted">${t("term_disabled")}</p></div>`));
    return renderModule(cSub(c), "webterminal");
  }
  const card = el(`<div class="card">
    <div class="btn-row"><button class="btn" id="conn">${t("term_connect")}</button><button class="btn sec" id="disc" disabled>${t("term_disconnect")}</button><span class="fg2 mono">${esc(st.shell || "")}</span></div>
    <div class="sp"></div>
    <div class="term" id="scr"></div>
    <div class="term-in"><span>$</span><input id="in" type="text" autocomplete="off" disabled></div>
  </div>`);
  c.appendChild(card);
  const scr = card.querySelector("#scr"), input = card.querySelector("#in");
  let ws = null;
  const write = s => { scr.textContent += s; scr.scrollTop = scr.scrollHeight; };
  const dec = new TextDecoder();
  card.querySelector("#conn").onclick = () => {
    if (ws) return;
    const proto = location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(`${proto}://${location.host}${BASE}/api/x/webterminal`);
    ws.binaryType = "arraybuffer";
    ws.onopen = () => { card.querySelector("#conn").disabled = true; card.querySelector("#disc").disabled = false; input.disabled = false; input.focus(); write("\n* connected *\n"); };
    ws.onmessage = e => write(typeof e.data === "string" ? e.data : dec.decode(e.data));
    ws.onclose = () => { write("\n* disconnected *\n"); ws = null; card.querySelector("#conn").disabled = false; card.querySelector("#disc").disabled = true; input.disabled = true; };
    ws.onerror = () => toast("WebSocket error", "err");
  };
  card.querySelector("#disc").onclick = () => ws && ws.close();
  input.addEventListener("keydown", e => {
    if (e.key === "Enter" && ws) { ws.send(JSON.stringify({ type: "input", data: input.value + "\n" })); input.value = ""; }
  });
  renderModule(cSub(c), "webterminal");
}
