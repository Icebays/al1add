// Al1add service worker: makes the panel installable as a web app.
// It deliberately caches nothing (the panel shows live data and must never be
// stale); it only shows an offline notice when a page load fails.
const OFFLINE = '<!doctype html><html lang="zh-Hant"><meta charset="utf-8">' +
  '<meta name="viewport" content="width=device-width,initial-scale=1">' +
  '<title>Al1add</title><body style="font-family:system-ui,sans-serif;display:flex;align-items:center;' +
  'justify-content:center;height:100vh;margin:0;color:#606266;background:#f0f2f5;text-align:center">' +
  '<div><b style="font-size:18px">連唔到伺服器 / Offline</b><p>請檢查網絡連線後再試。<br>Check your connection and retry.</p>' +
  '<button onclick="location.reload()" style="padding:10px 22px;border:0;border-radius:8px;background:#409eff;color:#fff;font-size:15px">重試 / Retry</button></div></body></html>';

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", e => e.waitUntil(self.clients.claim()));
self.addEventListener("fetch", e => {
  if (e.request.mode !== "navigate") return; // API, assets, websockets: untouched
  e.respondWith(fetch(e.request).catch(() =>
    new Response(OFFLINE, { headers: { "Content-Type": "text/html; charset=utf-8" } })));
});
