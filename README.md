# Al1add — 自架網絡工具箱 / Self-hosted network toolbox

[繁體中文](#繁體中文) · [English](#english)

Al1add 係一個類似 Lucky 嘅單一執行檔網絡管理面板，用 Go 寫，內置一個中英雙語嘅 Web 控制台。
所有模組設定都用自家 **AES-256-GCM** 加密格式（`.alcf`）儲存，可以匯出 / 匯入備份。

Al1add is a Lucky-style, single-binary network management panel written in Go, with a
built-in bilingual (Traditional Chinese / English) web console. Every module's
configuration is stored with its own **AES-256-GCM** encrypted format (`.alcf`) and can
be exported / imported as a backup.

---

## 🚀 Quick Start / 快速開始

一行指令由 GitHub 拉取、自動編譯並啟動（需要 `git` 同 Go ≥ 1.24）。
One command pulls from GitHub, builds and runs it (requires `git` and Go ≥ 1.24).

```bash
curl -fsSL https://raw.githubusercontent.com/Icebays/al1add/main/install.sh | bash
```

啟動後打開 `http://<本機>:18890/`；首個 `admin` 密碼會印喺日誌度。
Then open `http://<host>:18890/`; the initial `admin` password is printed to the log.

安裝完即刻前景運行 / build then run immediately:

```bash
curl -fsSL https://raw.githubusercontent.com/Icebays/al1add/main/install.sh | RUN=1 bash
```

安裝做 systemd 開機自啟服務（Linux，需 root）/ install as an auto-start systemd service:

```bash
curl -fsSL https://raw.githubusercontent.com/Icebays/al1add/main/install.sh | SERVICE=1 bash
```

**公網訪問 / Public access** — 面板預設已聽 `0.0.0.0` 並容許任何來源（有登入保護）；公網入唔到通常係**防火牆未開 port**。加 `PUBLIC=1` 自動開返主機防火牆（ufw / firewalld / iptables）：
The panel already listens on `0.0.0.0` and allows any source (login-protected); if the Internet can't reach it, the **host firewall port is closed**. `PUBLIC=1` opens it automatically:

```bash
curl -fsSL https://raw.githubusercontent.com/Icebays/al1add/main/install.sh | SERVICE=1 PUBLIC=1 bash
```

> ⚠️ 若喺雲端主機（阿里雲 / AWS / GCP 等）或路由器後面，仲要去**安全組 / 端口轉發**放行 TCP `18890`。
> On a cloud VPS or behind a router, also allow TCP `18890` in the **security group / port-forwarding**.

可用環境變量 / overrides: `INSTALL_DIR`、`DATA_DIR`、`BRANCH`、`PORT`、`RUN`、`SERVICE`、`PUBLIC`。

### 📱 手機版 / Mobile app

用手機打開面板、或者由主畫面開（Web App），會**自動**轉用手機介面：頂部 App 列、底部分頁（首頁 / 功能 / 日誌 / 我的）、功能圖標格、浮動「＋」新增、底部彈出式表單。可喺「我的 → 切換到電腦版」或電腦版側欄嘅手機圖標手動切換。
Phones (and the installed web app) automatically get a mobile UI with an app bar, bottom tabs, a launcher grid, a floating "+" button and bottom-sheet forms; switch views manually under **Me → Switch to desktop view**.

- **iPhone / iPad**：Safari → 分享 → 「加入主畫面」/ Share → *Add to Home Screen*
- **Android**：Chrome → 「安裝應用程式」/ *Install app*（Android 需要 HTTPS：喺「系統設定」開 HTTPS 埠並喺「SSL 證書」配置證書 / requires HTTPS — enable the HTTPS port and a certificate）

> ⚠️ 透過管道執行網上腳本前，建議先睇一眼內容：
> Review any piped script before running it: [`install.sh`](install.sh).

---

## 繁體中文

### 功能模組

| 分類 | 模組 | 說明 |
|------|------|------|
| 網絡 / 代理 | **Web服務** | 反向代理 / 重定向 / URL跳轉 / 靜態檔案，支援多子規則、網域比對、BasicAuth、網頁登入、CORS、WAF、IP過濾 |
| | **端口轉發** | TCP / UDP 轉發，連線與流量統計，支援 IP過濾 + 安全組訪問控制 |
| | **動態域名 DDNS** | Cloudflare / 阿里雲 / DNSPod，IPv4+IPv6，介面或外部URL取IP，正則過濾，Webhook通知 |
| | **STUN 內網穿透** | 內建 STUN 客戶端探測公網 IP:Port 映射，保活，變更通知，全局變量 |
| | **網絡喚醒 WOL** | 發送魔術封包喚醒 LAN 主機，上線探測 |
| | **SSL 證書** | ACME 自動申請 / 續期（Let's Encrypt / ZeroSSL / Buypass / Google），HTTP-01 與 Cloudflare DNS-01，亦支援自行上傳 |
| 穿透隧道 | **Cloudflare 隧道** | 以 token 或設定檔監管 `cloudflared` 進程 |
| | **FRP 內網穿透** | 監管 `frpc` / `frps` 進程 |
| 檔案服務 | **網絡存儲** | 定義本地掛載點供其他檔案服務使用 |
| | **WebDAV** | 以 WebDAV 分享掛載點，支援 BasicAuth、唯讀 |
| | **FTP 服務** | 以 FTP 分享掛載點，被動埠範圍可設定 |
| 管理 / 工具 | **Cloudflare 管理** ⭐ | 新增功能：管理多個 Cloudflare 帳戶、網域、DNS記錄增刪改、清快取、安全/SSL/開發模式、IP訪問規則 |
| | **Docker 管理** | 透過 Docker socket 列出 / 啟停 / 重啟 / 刪除容器、睇日誌、列映像 |
| | **計劃任務 (Cron)** | 5 欄位 cron 表達式，執行命令或 Webhook |
| | **Web 終端** | 瀏覽器內 PTY shell（WebSocket） |
| | **IP 過濾** | 命名白 / 黑名單：IP、CIDR、範圍、地區（geo:XX）、LAN |
| | **IP 地址庫** | MaxMind 格式 (.mmdb) IP→地區查詢 |
| | **安全組** | 統一帳戶認證 + 認證後 IP 動態放行，供 Web 服務與端口轉發使用 |
| | **Web 防火牆** | 基於 Coraza 嘅 WAF，命名規則集（SecLang） |

另有：儀表板、日誌、全局變量、系統設定、備份還原。

### 編譯 / 執行

```bash
go build -o al1add .
./al1add -cd ./data
```

首次啟動會喺日誌印出 `admin` 嘅隨機密碼。預設喺 `http://<本機>:18890` 開面板。

常用參數：

| 參數 | 說明 |
|------|------|
| `-cd <dir>` | 資料目錄（預設：執行檔旁 `data/`） |
| `-passphrase <pw>` | 用密語保護 `master.key`（亦可用環境變量 `AL1ADD_PASSPHRASE`） |
| `-resetuser` / `-resetpass` | 重設管理員帳號 / 密碼後退出 |
| `-version` | 印出版本 |

### 加密存檔格式 `.alcf`

```
"ALCF" | version(1) | nonce(12) | AES-256-GCM(明文, AAD=模組名)
```

主金鑰放喺 `master.key`（可用密語再包一層 PBKDF2-SHA256）。備份 zip 內含所有 `.alcf`
加 `master.key`；喺新機放返同一個密語即可還原；亦可匯出明文 JSON 備份。

### 安全

- 管理員密碼用 bcrypt；登入失敗有限流鎖定。
- Session 用 HMAC 簽名 cookie；改密碼令舊 session 失效。
- 所有改動 API 需要 `X-Requested-With: al1add` 標頭（防 CSRF）。
- 可設「安全入口」路徑、限制僅 LAN、後台 IP 過濾。

---

## English

### Modules

Network / proxy: **Web Service** (reverse proxy / redirect / URL jump / static, with
sub-rules, host matching, BasicAuth, web login, CORS, WAF, IP filter), **Port Forwarding**
(TCP/UDP with stats and access control), **DDNS** (Cloudflare / AliDNS / DNSPod, IPv4+IPv6),
**STUN Traversal**, **Wake-on-LAN**, **SSL Certificates** (ACME HTTP-01 & Cloudflare DNS-01,
or upload your own).

Tunnels: **Cloudflare Tunnel** (`cloudflared`), **FRP**.

File services: **Storage** mounts, **WebDAV**, **FTP Server**.

Management / tools: **Cloudflare Manager** ⭐ (the new feature — manage accounts, zones,
DNS records, cache purge, security/SSL/dev-mode settings, IP access rules), **Docker**,
**Cron Jobs**, **Web Terminal**, **IP Filter**, **IP Database**, **Security Groups**,
**WAF (Coraza)**.

Plus dashboard, logs, global variables, settings and backup/restore.

### Build / run

```bash
go build -o al1add .
./al1add -cd ./data          # panel at http://<host>:18890
```

The initial random `admin` password is printed to the log on first start.
See `-passphrase`, `-resetuser`/`-resetpass`, `-version`.

### Encrypted store `.alcf`

`"ALCF" | version | nonce(12) | AES-256-GCM(plaintext, AAD=module-name)`. The 32-byte master
key lives in `master.key` (optionally wrapped with a PBKDF2-SHA256 passphrase). A backup zip
bundles every `.alcf` plus `master.key`, so restoring on another machine only needs the same
passphrase; a plain-JSON export is also available.

### Security

bcrypt admin password with login rate-limiting, HMAC-signed session cookies, a CSRF header
requirement on all mutating API calls, an optional "safe entrance" path, LAN-only mode and a
panel IP filter.

---

> 本程式為獨立實作，與 Lucky 專案無關，亦不相容其存檔格式。
> This is an independent implementation, unaffiliated with and not save-compatible with Lucky.
