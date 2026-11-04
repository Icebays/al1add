"use strict";
// Schema-driven form engine (Lucky-style): label-left rows, text switches,
// steppers, radios, grouped boxes, inline "#N" sub-item cards and a tag-style
// rule list. All inputs are bound live to the object being edited.

function getPath(o, p) { return p.split(".").reduce((a, k) => (a == null ? undefined : a[k]), o); }
function setPath(o, p, v) {
  const ks = p.split("."); let a = o;
  for (let i = 0; i < ks.length - 1; i++) { if (a[ks[i]] == null || typeof a[ks[i]] !== "object") a[ks[i]] = {}; a = a[ks[i]]; }
  a[ks[ks.length - 1]] = v;
}
function optVal(o) { return Array.isArray(o) ? o[0] : o; }
function optLabel(o) { if (!Array.isArray(o)) return o; const l = o[1]; return (l && typeof l === "object") ? (l[LANG] || l.zh) : l; }
function clone(o) { return JSON.parse(JSON.stringify(o == null ? {} : o)); }

// ---------------------------------------------------------------- refs
async function loadRefs(kinds) {
  const out = {};
  await Promise.all([...new Set(kinds)].map(async kind => {
    const src = REF_SOURCES[kind]; if (!src) { out[kind] = [["", "—"]]; return; }
    try {
      const cfg = await api(`/m/${src.mod}/config`);
      out[kind] = [["", "—"]].concat((cfg[src.key] || []).map(x => [x.ID, x.Name || x.ID]));
    } catch (e) { out[kind] = [["", "—"]]; }
  }));
  return out;
}
function refKinds(fields) {
  const ks = [];
  for (const f of fields || []) {
    if (f.t && f.t.startsWith("ref:")) ks.push(f.t.slice(4));
    if (f.t === "sublist") ks.push(...refKinds(f.sublist.fields));
  }
  return ks;
}

// ---------------------------------------------------------------- controls
function switchEl(on, onToggle, small) {
  const s = el(`<span class="switch ${small ? "sm" : ""} ${on ? "on" : ""}" role="switch" tabindex="0"><span class="knob"></span><span class="st"></span></span>`);
  const paint = v => { s.classList.toggle("on", v); s.querySelector(".st").textContent = v ? t("on_txt") : t("off_txt"); s.setAttribute("aria-checked", v); };
  paint(!!on);
  const flip = e => { e.preventDefault(); e.stopPropagation(); const v = !s.classList.contains("on"); paint(v); onToggle(v); };
  s.addEventListener("click", flip);
  s.addEventListener("keydown", e => { if (e.key === " " || e.key === "Enter") flip(e); });
  return s;
}

// Build one field bound to obj. Returns {row, field}.
function buildField(f, obj, refs, onChange) {
  const isRef = f.t.startsWith("ref:");
  const opts = isRef ? (refs[f.t.slice(4)] || [["", "—"]]) : f.options;
  let cur = getPath(obj, f.k);
  // write defaults so `when` conditions and the saved config are consistent
  if (cur === undefined) {
    if (f.def !== undefined) { cur = f.def; setPath(obj, f.k, cur); }
    else if ((f.t === "select") && opts && opts.length) { cur = optVal(opts[0]); setPath(obj, f.k, cur); }
  }
  const row = el(`<div class="frow"><div class="flab">${esc(tr(f.label))}</div><div class="fctl"></div></div>`);
  const ctl = row.querySelector(".fctl");
  const write = v => { setPath(obj, f.k, v); onChange && onChange(); };
  const ph = esc(f.ph || "");

  if (f.t === "bool") {
    ctl.classList.add("inline-ctl");
    ctl.appendChild(switchEl(!!cur, v => write(v)));
  } else if (f.t === "number") {
    const sp = el(`<span class="stepper"><button type="button">−</button><input type="number" placeholder="${ph}"><button type="button">+</button></span>`);
    const inp = sp.querySelector("input");
    inp.value = cur == null ? "" : cur;
    const set = v => { inp.value = v; write(v === "" ? 0 : Number(v)); };
    const [minus, , plus] = sp.children;
    minus.onclick = () => set(Math.max(0, (Number(inp.value) || 0) - 1));
    plus.onclick = () => set((Number(inp.value) || 0) + 1);
    inp.oninput = () => write(inp.value === "" ? 0 : Number(inp.value));
    ctl.appendChild(sp);
  } else if ((f.t === "select" && opts && opts.length <= 3) && !isRef) {
    const box = el(`<div class="radios"></div>`);
    const name = "r_" + Math.random().toString(36).slice(2);
    for (const o of opts) {
      const v = optVal(o);
      const lab = el(`<label class="${String(cur) === String(v) ? "sel" : ""}"><input type="radio" name="${name}" ${String(cur) === String(v) ? "checked" : ""}> ${esc(optLabel(o))}</label>`);
      lab.querySelector("input").onchange = () => { box.querySelectorAll("label").forEach(x => x.classList.remove("sel")); lab.classList.add("sel"); write(v); };
      box.appendChild(lab);
    }
    ctl.appendChild(box);
  } else if (f.t === "select" || isRef) {
    const s = el(`<select></select>`);
    for (const o of (opts || [])) {
      const v = optVal(o);
      s.appendChild(el(`<option value="${esc(v)}" ${String(cur) === String(v) ? "selected" : ""}>${esc(optLabel(o))}</option>`));
    }
    s.onchange = () => write(s.value);
    ctl.appendChild(s);
  } else if (f.t === "tags") {
    const ta = el(`<textarea rows="3" placeholder="${ph}"></textarea>`);
    ta.value = Array.isArray(cur) ? cur.join("\n") : (cur || "");
    ta.oninput = () => write(ta.value.split("\n").map(s => s.trim()).filter(Boolean));
    ctl.appendChild(ta);
  } else if (f.t === "textarea" || f.t === "code") {
    const ta = el(`<textarea class="${f.t === "code" ? "code" : ""}" placeholder="${ph}"></textarea>`);
    ta.value = cur == null ? "" : cur;
    ta.oninput = () => write(ta.value);
    ctl.appendChild(ta);
  } else {
    const inp = el(`<input type="${f.t === "password" ? "password" : "text"}" placeholder="${ph}" autocomplete="off">`);
    inp.value = cur == null ? "" : cur;
    inp.oninput = () => write(inp.value);
    ctl.appendChild(inp);
  }
  if (f.help) ctl.appendChild(el(`<small class="fhelp">${esc(tr(f.help))}</small>`));
  return { row, field: f };
}

// Render fields into container bound to obj. Consecutive fields sharing `g`
// are wrapped in a titled box. Returns a refresh() that re-applies `when`.
function renderFields(container, fields, obj, refs) {
  const builts = [];
  const boxes = [];
  let box = null, boxG = null;
  const refresh = () => {
    for (const b of builts) if (b.field.when) b.row.style.display = b.field.when(obj) ? "" : "none";
    for (const bx of boxes) {
      const anyVisible = [...bx.querySelectorAll(":scope > .frow, :scope > .subsec")].some(r => r.style.display !== "none");
      bx.style.display = anyVisible ? "" : "none";
    }
  };
  for (const f of fields) {
    const gkey = f.g ? tr(f.g) : null;
    if (gkey !== boxG) {
      if (gkey) { box = el(`<div class="fbox"><div class="fbox-title">${esc(gkey)}</div></div>`); container.appendChild(box); boxes.push(box); }
      else box = null;
      boxG = gkey;
    }
    const target = box || container;
    if (f.t === "sublist") { target.appendChild(renderSublist(f, obj, refs)); continue; }
    const b = buildField(f, obj, refs, refresh);
    builts.push(b);
    target.appendChild(b.row);
  }
  refresh();
  return refresh;
}

// "添加X" / "Add X"
function addOf(s) { return LANG === "zh" ? t("add") + (s || "") : t("add") + (s ? " " + s : ""); }

function subSummary(spec, it) {
  return spec.columns.map(c => {
    let v = getPath(it, c);
    if (Array.isArray(v)) v = v.join(", ");
    const f = spec.fields.find(x => x.k === c);
    if (f && f.options) { const o = f.options.find(x => String(optVal(x)) === String(v)); if (o) v = optLabel(o); }
    return v;
  }).filter(v => v !== undefined && v !== null && v !== "").join("  →  ");
}

// Inline sub-item editor: "#1" collapsible cards + delete + add.
function renderSublist(f, obj, refs) {
  const spec = f.sublist;
  if (!Array.isArray(obj[spec.key])) obj[spec.key] = [];
  const items = obj[spec.key];
  const sec = el(`<div class="subsec"><div class="subhead">${esc(tr(f.label))}</div><div class="sublist"></div>
    <div class="subadd"><button type="button" class="btn sm">${icon("add", 13)} ${esc(addOf(tr(spec.singular)))}</button></div></div>`);
  const holder = sec.querySelector(".sublist");
  const hasEnable = spec.fields.some(x => x.k === "Enable");
  const draw = (openIdx) => {
    holder.innerHTML = "";
    if (!items.length) holder.appendChild(el(`<div class="empty" style="padding:12px 0">${t("none")}</div>`));
    items.forEach((it, i) => {
      const card = el(`<div class="subitem ${i === openIdx ? "open" : ""}">
        <div class="sihead"><span>${esc(t("item_n").replace("{n}", i + 1))}</span><b class="sisum"></b><span class="sitag"></span><span class="chev">▸</span></div>
        <div class="sibody"></div></div>`);
      const sum = card.querySelector(".sisum"), stag = card.querySelector(".sitag");
      const paintHead = () => {
        sum.textContent = subSummary(spec, it);
        stag.innerHTML = hasEnable ? (it.Enable ? tag(true, t("on_txt")) : tag(false, t("off_txt"))) : "";
      };
      const body = card.querySelector(".sibody");
      const inner = el("<div></div>"); body.appendChild(inner);
      const rf = renderFields(inner, spec.fields, it, refs);
      inner.addEventListener("input", paintHead); inner.addEventListener("click", () => setTimeout(paintHead, 0)); inner.addEventListener("change", paintHead);
      const del = el(`<div style="margin-top:8px"><button type="button" class="btn danger sm">${t("delete")}</button></div>`);
      del.querySelector("button").onclick = () => { if (confirm(t("confirm_delete"))) { items.splice(i, 1); draw(-1); } };
      body.appendChild(del);
      card.querySelector(".sihead").onclick = () => card.classList.toggle("open");
      paintHead(); rf();
      holder.appendChild(card);
    });
  };
  sec.querySelector(".subadd button").onclick = () => {
    const n = {}; if (hasEnable) n.Enable = true;
    items.push(n); draw(items.length - 1);
  };
  draw(items.length ? 0 : -1);
  return sec;
}

// Dialog editor. Resolves to the edited copy or null.
function editItem(titleText, fields, item, refs, isNew) {
  return new Promise(resolve => {
    const w = clone(item);
    if (isNew && fields.some(f => f.k === "Enable") && w.Enable === undefined) w.Enable = true;
    const body = el(`<div class="dlgform"></div>`);
    renderFields(body, fields, w, refs);
    const foot = el(`<div class="btn-row"><button class="btn sec" id="cx">${t("cancel")}</button><button class="btn" id="ok">${isNew ? t("add") : t("save")}</button></div>`);
    let done = false;
    const ov = modal(titleText, body, () => { if (!done) resolve(null); }, foot);
    foot.querySelector("#cx").onclick = () => { done = true; ov.remove(); resolve(null); };
    foot.querySelector("#ok").onclick = () => { done = true; ov.remove(); resolve(w); };
  });
}

// ---------------------------------------------------------------- rule list
const TAG_ORANGE = /^(Stack|Protocol|Type|Role|Mode|Source|Challenge|CA)$/;
const TAG_MONO = /addr|ip|port|domain|backend|path|mac|cron|host|server/i;

function cellTag(f, v) {
  if (v === undefined || v === null || v === "" || (Array.isArray(v) && !v.length)) return "";
  const lab = esc(tr(f.label) || f.k);
  if (typeof v === "boolean") return v ? `<span class="tag t-green" title="${lab}">${lab}</span>` : "";
  if (Array.isArray(v)) v = v.join(", ");
  if (/LastError|Error/.test(f.k)) return `<span class="tag t-red" title="${lab}">${esc(v)}</span>`;
  if (f.options) { const o = f.options.find(x => String(optVal(x)) === String(v)); if (o) v = optLabel(o); }
  if (TAG_ORANGE.test(f.k)) return `<span class="tag t-orange" title="${lab}">${esc(v)}</span>`;
  if (/Port$/.test(f.k)) return `<span class="tag t-blue mono" title="${lab}">:${esc(v)}</span>`;
  if (TAG_MONO.test(f.k)) return `<span class="tag t-blue mono" title="${lab}">${esc(v)}</span>`;
  return `<span class="tag t-gray" title="${lab}">${esc(v)}</span>`;
}

function statusTags(st) {
  if (!st) return "";
  let h = "";
  if (st.Running !== undefined) h += st.Running ? tag(true, t("running")) : `<span class="tag t-gray">${t("stopped")}</span>`;
  if (st.TCPConns !== undefined) h += `<span class="tag t-gray">TCP ${st.TCPConns}</span>`;
  if (st.UDPSess) h += `<span class="tag t-gray">UDP ${st.UDPSess}</span>`;
  if (st.TxBytes !== undefined || st.RxBytes !== undefined) h += `<span class="tag t-gray mono">↑ ${bytes(st.TxBytes)}  ↓ ${bytes(st.RxBytes)}</span>`;
  if (st.Valid && st.DaysLeft !== undefined) {
    const cls = st.DaysLeft > 30 ? "t-green" : st.DaysLeft > 7 ? "t-orange" : "t-red";
    h += `<span class="tag ${cls}">${esc(t("days_left").replace("{n}", st.DaysLeft))}</span>`;
  }
  if (st.Error) h += `<span class="tag t-red">${esc(st.Error)}</span>`;
  return h;
}

function ruleList(spec, arr, refs, onChange, extraActions, statusByID) {
  arr = arr || [];
  const wrap = el(`<div class="rlist"></div>`);
  if (!arr.length) { wrap.appendChild(el(`<div class="empty">${t("empty_rules")}</div>`)); return wrap; }
  const hasEnable = spec.fields.some(f => f.k === "Enable");
  const subF = spec.fields.find(f => f.t === "sublist");
  const cols = spec.columns.filter(c => c !== "Name").map(c => spec.fields.find(f => f.k === c) || { k: c, label: { zh: c, en: c } });
  arr.forEach((item, idx) => {
    const rl = el(`<div class="rl"><div class="rl-main"></div></div>`);
    const main = rl.querySelector(".rl-main");
    if (hasEnable) main.appendChild(switchEl(!!item.Enable, v => { const n = arr.slice(); n[idx] = Object.assign({}, item, { Enable: v }); onChange(n); }, true));
    main.appendChild(el(`<span class="rl-name">${esc(item.Name || item.Username || item.FrontDomain || "#" + (idx + 1))}</span>`));
    const tags = el(`<span class="inline" style="gap:6px"></span>`);
    tags.innerHTML = cols.map(cf => cellTag(cf, getPath(item, cf.k))).join("") + statusTags(statusByID && statusByID[item.ID]);
    main.appendChild(tags);
    const acts = el(`<span class="rl-acts"></span>`);
    for (const xa of (extraActions || [])) {
      const xb = el(`<button class="btn ${xa.cls || "plain"} xs">${xa.icon ? icon(xa.icon, 12) : ""} ${esc(xa.label)}</button>`);
      xb.onclick = async () => { xb.disabled = true; try { const r = await xa.run(item); toast((r && r.msg) || t("op_ok"), "ok"); } catch (e) { toast(e.message, "err"); } xb.disabled = false; };
      acts.appendChild(xb);
    }
    const be = el(`<button class="btn plain xs">${t("edit")}</button>`);
    const bd = el(`<button class="btn plain-danger xs">${t("delete")}</button>`);
    be.onclick = async () => { const res = await editItem(t("edit") + " · " + (item.Name || tr(spec.singular)), spec.fields, item, refs, false); if (res) { const n = arr.slice(); n[idx] = res; onChange(n); } };
    bd.onclick = () => { if (confirm(t("confirm_delete"))) { const n = arr.slice(); n.splice(idx, 1); onChange(n); } };
    acts.appendChild(be); acts.appendChild(bd);
    main.appendChild(acts);
    // sub-items, shown indented (front → backend)
    if (subF) {
      for (const s of (item[subF.sublist.key] || [])) {
        const sub = el(`<div class="rl-sub"></div>`);
        const sh = subF.sublist.fields.some(f => f.k === "Enable");
        const sc = subF.sublist.columns.map(c => subF.sublist.fields.find(f => f.k === c) || { k: c, label: { zh: c, en: c } });
        sub.innerHTML = (sh ? (s.Enable ? tag(true, t("on_txt")) : tag(false, t("off_txt"))) : "") +
          sc.map((cf, i) => (i > 0 && /Backend/.test(cf.k) ? `<span class="arrow">→</span>` : "") + cellTag(cf, getPath(s, cf.k))).join("");
        rl.appendChild(sub);
      }
    }
    wrap.appendChild(rl);
  });
  return wrap;
}

// ---------------------------------------------------------------- module form
// opts: {rowActions, headActs (element for the add button), statusByID}
async function renderModuleForm(c, name, cfg, reload, opts) {
  const sc = SCHEMA[name];
  if (!sc) return false;
  opts = opts || {};
  const refs = await loadRefs(refKinds(sc.list && sc.list.fields).concat(refKinds(sc.top)));
  const persist = async () => {
    await api(`/m/${name}/config`, { method: "PUT", json: cfg });
    toast(t("saved"), "ok");
  };

  if (sc.top) {
    const card = el(`<div class="card"><h3>${icon("settings", 16)} ${t("config")}</h3><div class="dlgform"></div>
      <div class="btn-row" style="justify-content:flex-end;margin-top:8px"><button class="btn" id="save">${t("save")}</button></div></div>`);
    const work = clone(cfg);
    renderFields(card.querySelector(".dlgform"), sc.top, work, refs);
    card.querySelector("#save").onclick = async () => {
      Object.assign(cfg, work);
      try { await persist(); reload && reload(); } catch (e) { toast(e.message, "err"); }
    };
    c.appendChild(card);
  }

  if (sc.list) {
    const card = el(`<div class="card"><h3>${icon(name, 16)} ${esc(tr(sc.list.title))}</h3><div class="lt"></div></div>`);
    c.appendChild(card);
    const holder = card.querySelector(".lt");
    const extraActions = (opts.rowActions || []).map(ra => ({
      label: tr(ra.label), icon: ra.icon, cls: ra.cls,
      run: async item => {
        const r = await api(`/m/${name}/action/${ra.action}`, { method: "POST", json: { ID: item.ID } });
        return { msg: t("op_ok") + (r && r.ip ? " · " + r.ip : (r && r.notAfter ? " · " + r.notAfter : (r && r.online !== undefined ? " · " + (r.online ? "online" : "offline") : ""))) };
      },
    }));
    const commit = async arr => {
      const prev = cfg[sc.list.key];
      cfg[sc.list.key] = arr;
      try { await persist(); reload && reload(); }
      catch (e) { cfg[sc.list.key] = prev; toast(e.message, "err"); draw(); }
    };
    const draw = () => { holder.innerHTML = ""; holder.appendChild(ruleList(sc.list, cfg[sc.list.key] || [], refs, commit, extraActions, opts.statusByID)); };
    draw();
    const addBtn = el(`<button class="btn">${icon("add", 14)} ${esc(addOf(tr(sc.list.singular)))}</button>`);
    addBtn.onclick = async () => {
      const res = await editItem(addOf(tr(sc.list.singular)), sc.list.fields, {}, refs, true);
      if (res) commit((cfg[sc.list.key] || []).concat([res]));
    };
    if (opts.headActs) opts.headActs.prepend(addBtn); else card.querySelector("h3").appendChild(addBtn);
  }
  return true;
}
