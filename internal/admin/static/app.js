"use strict";
// ---------------------------------------------------------------- base path
// The panel may be served under a "safe entrance" prefix. Derive the API base
// from where app.js itself was loaded.
const BASE = (function () {
  const s = document.currentScript || [...document.scripts].find(x => x.src.includes("app.js"));
  if (!s) return "";
  return new URL(".", s.src).pathname.replace(/\/$/, "");
})();

// ---------------------------------------------------------------- i18n
let LANG = localStorage.getItem("al1add_lang") || "zh";
function t(k) { return (I18N[LANG] && I18N[LANG][k]) || (I18N.zh[k]) || k; }
function modTitle(name) { const m = MOD_TITLES[name]; return m ? (m[LANG] || m.zh) : name; }
// tr localizes a {zh,en} label object (or returns a plain string as-is).
function tr(o) { if (o == null) return ""; if (typeof o === "object") return o[LANG] || o.zh || o.en || ""; return String(o); }
function setLang(l) { LANG = l; localStorage.setItem("al1add_lang", l); render(); }

// ---------------------------------------------------------------- api
async function api(path, opts = {}) {
  opts.headers = Object.assign({ "X-Requested-With": "al1add" }, opts.headers || {});
  if (opts.json !== undefined) {
    opts.body = JSON.stringify(opts.json);
    opts.headers["Content-Type"] = "application/json";
    delete opts.json;
  }
  const r = await fetch(BASE + "/api" + path, opts);
  const ct = r.headers.get("content-type") || "";
  if (!ct.includes("application/json")) {
    if (!r.ok) throw new Error("HTTP " + r.status);
    return r;
  }
  const data = await r.json();
  if (!r.ok) throw new Error(data.error || ("HTTP " + r.status));
  return data;
}

// ---------------------------------------------------------------- state
const S = { info: null, session: null, route: "dashboard", sidebarOpen: false };

// ---------------------------------------------------------------- helpers
function el(html) { const tpl = document.createElement("template"); tpl.innerHTML = html.trim(); return tpl.content.firstChild; }
function esc(s) { return String(s == null ? "" : s).replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])); }
function toast(msg, kind) {
  let box = document.querySelector(".toast");
  if (!box) { box = el('<div class="toast"></div>'); document.body.appendChild(box); }
  const d = el(`<div class="${kind || ""}">${esc(msg)}</div>`);
  box.appendChild(d);
  setTimeout(() => d.remove(), 2600);
}
function bytes(n) {
  n = Number(n) || 0; const u = ["B", "KB", "MB", "GB", "TB"]; let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return n.toFixed(i ? 1 : 0) + " " + u[i];
}
function tag(on, label) { return `<span class="tag ${on ? "on" : "off"}">${esc(label || (on ? t("running") : t("stopped")))}</span>`; }
function go(route) { location.hash = "#" + route; }

// modal: centered title + close button, body, optional footer node.
function modal(title, bodyNode, onClose, footNode) {
  const ov = el(`<div class="overlay"><div class="modal">
    <div class="mhead"><h3>${esc(title)}</h3><button class="mclose" aria-label="close">×</button></div>
    <div class="mbody"></div></div></div>`);
  const m = ov.querySelector(".modal");
  m.querySelector(".mbody").appendChild(bodyNode);
  if (footNode) { const f = el('<div class="mfoot"></div>'); f.appendChild(footNode); m.appendChild(f); }
  const close = () => { ov.remove(); onClose && onClose(); };
  m.querySelector(".mclose").onclick = close;
  ov.addEventListener("mousedown", e => { if (e.target === ov) close(); });
  document.body.appendChild(ov);
  return ov;
}

// theme (light default, like classic admin panels)
function applyTheme() { document.documentElement.dataset.theme = localStorage.getItem("al1add_theme") || "light"; }
function toggleTheme() { const n = (localStorage.getItem("al1add_theme") || "light") === "light" ? "dark" : "light"; localStorage.setItem("al1add_theme", n); applyTheme(); }
applyTheme();

// ---------------------------------------------------------------- UI mode (mobile / desktop)
// Auto: phones, narrow screens and home-screen web apps on touch devices get the
// mobile UI. The user can override it; choosing the auto value resets to auto.
const MQ_NARROW = window.matchMedia("(max-width: 820px)");
function autoMobile() {
  return MQ_NARROW.matches ||
    /Android|iPhone|iPod|Mobile|HarmonyOS|Windows Phone/i.test(navigator.userAgent) ||
    (window.matchMedia("(display-mode: standalone)").matches && window.matchMedia("(pointer: coarse)").matches) ||
    window.navigator.standalone === true;
}
function uiPref() { try { return localStorage.getItem("al1add_ui") || "auto"; } catch (e) { return "auto"; } }
function setUIPref(v) { const auto = autoMobile() ? "mobile" : "desktop"; try { localStorage.setItem("al1add_ui", v === auto ? "auto" : v); } catch (e) {} }
function isMobileUI() { const p = uiPref(); return p === "mobile" ? true : p === "desktop" ? false : autoMobile(); }
MQ_NARROW.addEventListener("change", () => { if (uiPref() === "auto" && S.info) render(); });

// Installable web app: register a (non-caching) service worker on secure origins.
if ("serviceWorker" in navigator && window.isSecureContext) {
  window.addEventListener("load", () => navigator.serviceWorker.register("sw.js").catch(() => {}));
}

// ---------------------------------------------------------------- boot
async function boot() {
  try {
    S.session = await api("/session");
    S.info = await api("/info");
    window.addEventListener("hashchange", onRoute);
    onRoute();
  } catch (e) {
    renderLogin();
  }
}
function onRoute() {
  S.route = (location.hash || "#dashboard").slice(1) || "dashboard";
  S.sidebarOpen = false;
  render();
}

// ---------------------------------------------------------------- login
function renderLogin() {
  const app = document.getElementById("app");
  app.innerHTML = "";
  const form = el(`<form>
    <div class="inline" style="justify-content:space-between">
      <h1><span class="logo">${icon("logo", 22)}</span> ${t("login_title")}</h1>
      <span class="langsw"></span>
    </div>
    <p class="sub">${t("login_sub")}</p>
    <label class="f"><span>${t("user")}</span><input id="lu" type="text" autocomplete="username" autofocus></label>
    <label class="f"><span>${t("pass")}</span><input id="lp" type="password" autocomplete="current-password"></label>
    <div class="err-text hidden" id="le"></div>
    <button class="btn" style="width:100%;margin-top:14px" id="lb" type="submit">${t("login")}</button>
  </form>`);
  form.querySelector(".langsw").appendChild(langSwitch());
  form.addEventListener("submit", async e => {
    e.preventDefault();
    const b = form.querySelector("#lb"), err = form.querySelector("#le");
    b.disabled = true; b.textContent = t("logging_in"); err.classList.add("hidden");
    try {
      await api("/login", { method: "POST", json: { User: form.querySelector("#lu").value, Pass: form.querySelector("#lp").value } });
      await boot();
    } catch (ex) {
      err.textContent = ex.message; err.classList.remove("hidden");
      b.disabled = false; b.textContent = t("login");
    }
  });
  const wrap = el('<div class="login"></div>'); wrap.appendChild(form); app.appendChild(wrap);
}

function langSwitch() {
  const s = el(`<span class="inline">
    <span class="pill-btn ${LANG === "zh" ? "active" : ""}" data-l="zh">繁中</span>
    <span class="pill-btn ${LANG === "en" ? "active" : ""}" data-l="en">EN</span>
  </span>`);
  s.querySelectorAll(".pill-btn").forEach(b => b.onclick = () => setLang(b.dataset.l));
  return s;
}

// ---------------------------------------------------------------- shell
const GROUPS = [
  { key: "grp_overview", items: [["dashboard", "dashboard", () => t("dashboard")]] },
  { key: "grp_network", mods: ["webservice", "portforward", "ddns", "stun", "wol", "ssl"] },
  { key: "grp_tunnel", mods: ["cloudflared", "frp"] },
  { key: "grp_files", mods: ["storage", "webdav", "ftpserver"] },
  { key: "grp_manage", mods: ["cloudflare", "docker", "cron", "webterminal", "ipfilter", "ipdb", "secgroup", "waf"] },
  { key: "grp_system", items: [["logs", "logs", () => t("logs")], ["globalvars", "globalvars", () => t("globalvars")], ["settings", "settings", () => t("settings")], ["backup", "backup", () => t("backup")]] },
];

function render() {
  if (!S.info) { renderLogin(); return; }
  document.documentElement.lang = LANG === "zh" ? "zh-Hant" : "en";
  const mobile = isMobileUI();
  document.documentElement.classList.toggle("m", mobile);
  if (mobile) return renderMobile();
  const app = document.getElementById("app");
  app.innerHTML = "";
  const shell = el('<div class="shell"></div>');

  // sidebar
  const sb = el(`<aside class="sidebar ${S.sidebarOpen ? "open" : ""}">
    <div class="brand"><span class="logo">${icon("logo", 22)}</span> <span class="bt">${esc(S.info.title || "Al1add")}</span> <span class="v">v${esc(S.info.version)}</span></div>
    <nav class="nav"></nav>
    <div class="side-foot">
      <span class="langsw"></span>
      <button class="pill-btn" id="theme" title="${esc(t("m_theme"))}">${icon(document.documentElement.dataset.theme === "dark" ? "sun" : "moon", 14)}</button>
      <button class="pill-btn" id="tomobile" title="${esc(t("m_mobile"))}">${icon("mobile", 14)}</button>
      <button class="btn sec sm" id="logout" style="margin-left:auto">${icon("logout", 14)} ${t("logout")}</button>
    </div>
  </aside>`);
  const nav = sb.querySelector(".nav");
  const avail = new Set(S.info.modules.map(m => m.name));
  for (const g of GROUPS) {
    const items = [];
    if (g.items) for (const [r, ic, lbl] of g.items) items.push([r, ic, lbl()]);
    if (g.mods) for (const name of g.mods) if (avail.has(name)) items.push([name, name, modTitle(name)]);
    if (!items.length) continue;
    nav.appendChild(el(`<div class="group">${t(g.key)}</div>`));
    for (const [r, ic, lbl] of items) {
      const a = el(`<a class="${S.route === r ? "active" : ""}"><span class="ic">${icon(ic)}</span><span>${esc(lbl)}</span></a>`);
      a.onclick = () => go(r);
      nav.appendChild(a);
    }
  }
  sb.querySelector(".langsw").appendChild(langSwitch());
  sb.querySelector("#theme").onclick = () => { toggleTheme(); render(); };
  sb.querySelector("#tomobile").onclick = () => { setUIPref("mobile"); render(); };
  sb.querySelector("#logout").onclick = async () => { await api("/logout", { method: "POST" }); S.info = null; renderLogin(); };

  // main
  const main = el(`<div class="main">
    <div class="topbar"><button class="hamb">${icon("menu", 20)}</button><b>${esc(S.info.title || "Al1add")}</b></div>
    <div class="content" id="content"></div>
  </div>`);
  main.querySelector(".hamb").onclick = () => { S.sidebarOpen = !S.sidebarOpen; render(); };

  shell.appendChild(sb); shell.appendChild(main);
  app.appendChild(shell);

  renderRoute(main.querySelector("#content"));
}

function pageHead(c, title, desc) {
  if (isMobileUI()) setBar({ title });
  else c.appendChild(el(`<h2 class="page">${esc(title)}</h2>`));
  if (desc) c.appendChild(el(`<p class="page-desc">${esc(desc)}</p>`));
}

// ---------------------------------------------------------------- mobile shell
const M_TABS = [["dashboard", "home", "m_home"], ["apps", "apps", "m_apps"], ["logs", "logs", "logs"], ["me", "user", "m_me"]];
function mobileTab(r) {
  if (r === "dashboard" || r === "logs") return r;
  if (["me", "settings", "backup", "globalvars"].includes(r)) return "me";
  return "apps";
}
function backTarget(r) {
  if (["dashboard", "apps", "logs", "me"].includes(r)) return null;
  if (["settings", "backup", "globalvars"].includes(r)) return "me";
  return "apps";
}
function routeTitle(r) {
  if (r === "apps") return t("m_apps");
  if (r === "me") return t("m_me");
  if (["dashboard", "logs", "settings", "backup", "globalvars"].includes(r)) return t(r);
  return modTitle(r);
}

function renderMobile() {
  const app = document.getElementById("app");
  app.innerHTML = "";
  const sh = el(`<div class="mshell">
    <header class="mbar"><button class="mback hidden" aria-label="back">${icon("back", 22)}</button><div class="mtitle"></div><div class="mbar-acts"></div></header>
    <main class="mcontent" id="content"></main>
    <nav class="mtabs"></nav></div>`);
  const nav = sh.querySelector(".mtabs");
  const active = mobileTab(S.route);
  for (const [r, ic, k] of M_TABS) {
    const a = el(`<a class="${active === r ? "active" : ""}">${icon(ic, 22)}<span>${esc(t(k))}</span></a>`);
    a.onclick = () => { if (S.route !== r) go(r); };
    nav.appendChild(a);
  }
  app.appendChild(sh);
  setBar({ title: routeTitle(S.route), back: backTarget(S.route), actions: [] });
  renderRoute(sh.querySelector("#content"));
}

// setBar updates the mobile app bar: {title, back: route|null, actions: [{icon, title, onClick}]}.
function setBar(o) {
  const bar = document.querySelector(".mbar");
  if (!bar) return;
  if (o.title !== undefined) bar.querySelector(".mtitle").textContent = o.title;
  if (o.back !== undefined) {
    const b = bar.querySelector(".mback");
    b.classList.toggle("hidden", !o.back);
    b.onclick = () => go(o.back);
  }
  if (o.actions) {
    const acts = bar.querySelector(".mbar-acts"); acts.innerHTML = "";
    for (const a of o.actions) {
      const b = el(`<button class="mact" title="${esc(a.title || "")}" aria-label="${esc(a.title || "")}">${icon(a.icon, 21)}</button>`);
      b.onclick = a.onClick; acts.appendChild(b);
    }
  }
}

// "功能" — every module as an app-launcher grid.
function renderApps(c) {
  pageHead(c, t("m_apps"));
  const avail = new Set(S.info.modules.map(m => m.name));
  for (const g of GROUPS) {
    const mods = (g.mods || []).filter(n => avail.has(n));
    if (!mods.length) continue;
    c.appendChild(el(`<div class="m-sec">${esc(t(g.key))}</div>`));
    const grid = el(`<div class="appgrid" data-g="${g.key}"></div>`);
    for (const n of mods) {
      const a = el(`<a class="app"><span class="app-ic">${icon(n, 24)}</span><span class="app-name">${esc(modTitle(n))}</span></a>`);
      a.onclick = () => go(n);
      grid.appendChild(a);
    }
    c.appendChild(grid);
  }
}

// "我的" — account, system pages, language, theme, desktop switch, logout.
function renderMe(c) {
  pageHead(c, t("m_me"));
  const dark = document.documentElement.dataset.theme === "dark";
  c.appendChild(el(`<div class="card me-head"><span class="logo">${icon("logo", 28)}</span>
    <div><b>${esc((S.session && S.session.user) || "admin")}</b><div class="muted">${esc(S.info.title || "Al1add")} · v${esc(S.info.version)}</div></div></div>`));
  const list = el(`<div class="card cells"></div>`);
  const cell = (ic, label, right, on) => {
    const x = el(`<a class="cell">${icon(ic, 20)}<span class="cl">${esc(label)}</span><span class="cr">${esc(right || "")}</span>${icon("chevron", 16)}</a>`);
    x.onclick = on; list.appendChild(x);
  };
  cell("settings", t("settings"), "", () => go("settings"));
  cell("backup", t("backup"), "", () => go("backup"));
  cell("globalvars", t("globalvars"), "", () => go("globalvars"));
  cell("language", t("m_lang"), LANG === "zh" ? "繁體中文" : "English", () => setLang(LANG === "zh" ? "en" : "zh"));
  cell(dark ? "sun" : "moon", t("m_theme"), dark ? t("m_dark") : t("m_light"), () => { toggleTheme(); render(); });
  cell("monitor", t("m_desktop"), "", () => { setUIPref("desktop"); render(); });
  c.appendChild(list);
  const lo = el(`<button class="btn danger mblock">${icon("logout", 16)} ${t("logout")}</button>`);
  lo.onclick = async () => { await api("/logout", { method: "POST" }); S.info = null; renderLogin(); };
  c.appendChild(lo);
}

function renderRoute(c) {
  const r = S.route;
  if (r === "apps") return renderApps(c);
  if (r === "me") return renderMe(c);
  if (r === "dashboard") return renderDashboard(c);
  if (r === "logs") return renderLogs(c);
  if (r === "globalvars") return renderVars(c);
  if (r === "settings") return renderSettings(c);
  if (r === "backup") return renderBackup(c);
  if (r === "cloudflare") return renderCloudflare(c);
  if (r === "docker") return renderDocker(c);
  if (r === "webterminal") return renderTerminal(c);
  if (S.info.modules.find(m => m.name === r)) return renderModule(c, r);
  c.appendChild(el(`<p class="muted">${t("none")}</p>`));
}
