"use strict";
// Form schema for every module: turns the JSON config into a Lucky-style GUI
// (lists with add/edit/delete modals, toggles, selects, tag inputs).
//
// field: { k, t, label, help?, ph?, options?, when?, def? }
//   t: text | password | number | bool | select | textarea | code | tags | sublist
//   label/help: {zh,en}; options: [value | [value, label|{zh,en}]]
//   when(item): show the field only when it returns true
//   sublist: { key, title, fields, columns }
//
// A module schema has optional `top` (scalar fields) and/or `list`
// ({ key, title, singular, fields, columns }).
const L = (zh, en) => ({ zh, en });

const SEL_GROUPMODE = [["", L("不參與", "Off")], ["supplement", L("授權補充 (或)", "Supplement (or)")], ["strict", L("嚴格 (且)", "Strict (and)")]];

const SCHEMA = {
  ddns: {
    list: {
      key: "Tasks", title: L("DDNS 任務", "DDNS tasks"), singular: L("DDNS任務", "DDNS task"),
      columns: ["Name", "Stack", "Domains", "LastIP", "LastError"],
      fields: [
        { k: "Name", t: "text", label: L("名稱", "Name") },
        { k: "Enable", t: "bool", label: L("啟用", "Enable") },
        { k: "Creds.Provider", t: "select", options: [["cloudflare", "Cloudflare"], ["alidns", L("阿里雲 DNS", "AliDNS")], ["dnspod", "DNSPod"]], label: L("DNS 服務商", "DNS provider") },
        { k: "Creds.ID", t: "text", label: L("帳號 ID / Email", "Account ID / Email"), help: L("Cloudflare 用 API Token 時留空；阿里雲填 AccessKeyId；DNSPod 填 SecretId", "Cloudflare API Token: leave empty; AliDNS: AccessKeyId; DNSPod: SecretId") },
        { k: "Creds.Secret", t: "password", label: L("密鑰 / Token", "Secret / Token") },
        { k: "Stack", t: "select", options: [["ipv4", "IPv4"], ["ipv6", "IPv6"]], label: L("協議棧", "IP stack") },
        { k: "Domains", t: "tags", label: L("域名（每行一個）", "Domains (one per line)"), ph: "home.example.com" },
        { k: "Proxied", t: "bool", label: L("Cloudflare 代理（橙雲）", "Cloudflare proxied"), when: it => getPath(it, "Creds.Provider") === "cloudflare" },
        { k: "TTL", t: "number", label: "TTL", def: 0 },
        { k: "IntervalSec", t: "number", label: L("檢測間隔（秒）", "Interval (sec)"), def: 120 },
        { k: "Source", t: "select", options: [["url", L("外部網址取IP", "From URL")], ["interface", L("網卡取IP", "From interface")]], label: L("IP 來源", "IP source") },
        { k: "Interface", t: "text", label: L("網卡名", "Interface"), when: it => it.Source === "interface", ph: "eth0" },
        { k: "URLs", t: "tags", label: L("取IP網址（每行一個，留空用預設）", "IP URLs (one per line)"), when: it => it.Source !== "interface" },
        { k: "IPReg", t: "text", label: L("IP 正則過濾（選填）", "IP regex filter (optional)") },
        { k: "Webhook.Enable", t: "bool", label: L("變更時通知 Webhook", "Notify webhook on change") },
        { k: "Webhook.URL", t: "text", label: "Webhook URL", when: it => getPath(it, "Webhook.Enable") },
      ],
    },
  },

  portforward: {
    list: {
      key: "Rules", title: L("轉發規則", "Forwarding rules"), singular: L("轉發規則", "forwarding rule"),
      columns: ["Name", "Protocol", "ListenPort", "TargetAddr"],
      fields: [
        { k: "Name", t: "text", label: L("名稱", "Name") },
        { k: "Enable", t: "bool", label: L("啟用", "Enable") },
        { k: "Protocol", t: "select", options: ["tcp", "udp", "tcp+udp"], label: L("協議", "Protocol") },
        { k: "ListenIP", t: "text", label: L("監聽 IP（空=全部）", "Listen IP (empty=all)") },
        { k: "ListenPort", t: "number", label: L("監聽埠", "Listen port") },
        { k: "TargetAddr", t: "text", label: L("目標地址 host:port", "Target host:port"), ph: "192.168.1.10:80" },
        { k: "IPFilter", t: "ref:ipfilter", label: L("IP 過濾器", "IP filter") },
        { k: "GroupID", t: "ref:secgroup", label: L("安全組", "Security group") },
        { k: "GroupMode", t: "select", options: SEL_GROUPMODE, label: L("安全組模式", "Group mode"), when: it => it.GroupID },
      ],
    },
  },

  webservice: {
    list: {
      key: "Rules", title: L("Web 服務規則", "Web service rules"), singular: L("Web服務規則", "web rule"),
      columns: ["Name", "ListenIP", "Port", "TLS"],
      fields: [
        { k: "Name", t: "text", label: L("名稱", "Name") },
        { k: "Enable", t: "bool", label: L("啟用", "Enable") },
        { k: "ListenIP", t: "text", label: L("監聽 IP（空=全部）", "Listen IP (empty=all)") },
        { k: "Port", t: "number", label: L("監聽埠", "Port") },
        { k: "TLS", t: "bool", label: L("啟用 TLS (HTTPS)", "Enable TLS (HTTPS)") },
        {
          k: "SubRules", t: "sublist", label: L("子規則", "Sub-rules"),
          sublist: {
            key: "SubRules", title: L("子規則", "Sub-rules"), singular: L("子規則", "sub-rule"),
            columns: ["FrontDomain", "Type", "Backend"],
            fields: [
              { k: "Enable", t: "bool", label: L("啟用", "Enable") },
              { k: "FrontDomain", t: "text", label: L("前端域名（空=任意，可 host/path）", "Front domain (empty=any)"), ph: "app.example.com" },
              { k: "Type", t: "select", options: [["proxy", L("反向代理", "Reverse proxy")], ["redirect", L("重定向 302", "Redirect 302")], ["redirect301", L("重定向 301", "Redirect 301")], ["static", L("靜態檔案", "Static files")]], label: L("類型", "Type") },
              { k: "Backend", t: "text", label: L("後端地址 / 目標", "Backend / target"), ph: "http://192.168.1.10:8080" },
              { k: "KeepClientIP", t: "bool", label: L("攜帶客戶端 IP (X-Forwarded-For)", "Send client IP"), when: it => it.Type === "proxy" },
              { k: "AppendClientIP", t: "bool", label: L("追加而非覆蓋 XFF", "Append XFF"), when: it => it.Type === "proxy" && it.KeepClientIP },
              { k: "IgnoreBackendTLS", t: "bool", label: L("忽略後端 TLS 驗證", "Ignore backend TLS"), when: it => it.Type === "proxy" },
              { k: "HostHeaderOrigin", t: "bool", label: L("保留原始 Host 標頭", "Keep original Host"), when: it => it.Type === "proxy" },
              { k: "CORS", t: "bool", label: L("跨域 (CORS)", "CORS"), when: it => it.Type === "proxy" },
              { k: "BasicUser", t: "text", label: L("BasicAuth 用戶", "BasicAuth user") },
              { k: "BasicPass", t: "password", label: L("BasicAuth 密碼", "BasicAuth pass") },
              { k: "WebLogin", t: "bool", label: L("網頁登入（用安全組）", "Web login (security group)") },
              { k: "GroupID", t: "ref:secgroup", label: L("安全組", "Security group"), when: it => it.WebLogin || it.GroupMode },
              { k: "GroupMode", t: "select", options: SEL_GROUPMODE, label: L("安全組模式", "Group mode"), when: it => !it.WebLogin },
              { k: "IPFilter", t: "ref:ipfilter", label: L("IP 過濾器", "IP filter") },
              { k: "WAFProfile", t: "ref:waf", label: L("WAF 規則集", "WAF profile") },
            ],
          },
        },
      ],
    },
  },

  stun: {
    list: {
      key: "Tasks", title: L("STUN 任務", "STUN tasks"), singular: L("STUN穿透規則", "STUN rule"),
      columns: ["Name", "LastAddr", "LastError"],
      fields: [
        { k: "Name", t: "text", label: L("名稱", "Name") },
        { k: "Enable", t: "bool", label: L("啟用", "Enable") },
        { k: "LocalPort", t: "number", label: L("本地 UDP 埠（0=隨機）", "Local UDP port (0=random)") },
        { k: "STUNServer", t: "text", label: L("STUN 伺服器", "STUN server"), ph: "stun.miwifi.com:3478" },
        { k: "IntervalSec", t: "number", label: L("保活間隔（秒）", "Keep-alive (sec)"), def: 30 },
        { k: "Webhook.Enable", t: "bool", label: L("變更時通知 Webhook", "Notify webhook on change") },
        { k: "Webhook.URL", t: "text", label: "Webhook URL", when: it => getPath(it, "Webhook.Enable") },
      ],
    },
  },

  wol: {
    list: {
      key: "Devices", title: L("設備", "Devices"), singular: L("喚醒設備", "device"),
      columns: ["Name", "MAC", "Broadcast"],
      fields: [
        { k: "Name", t: "text", label: L("名稱", "Name") },
        { k: "MAC", t: "text", label: L("MAC 地址", "MAC address"), ph: "AA:BB:CC:DD:EE:FF" },
        { k: "Broadcast", t: "text", label: L("廣播地址（空=255.255.255.255:9）", "Broadcast (default …:9)") },
        { k: "CheckHost", t: "text", label: L("探測地址 host:port（選填）", "Probe host:port (optional)") },
        { k: "Remark", t: "text", label: L("備註", "Remark") },
      ],
    },
  },

  ssl: {
    list: {
      key: "Certs", title: L("證書", "Certificates"), singular: L("SSL證書", "certificate"),
      columns: ["Name", "Domains", "NotAfter", "LastError"],
      fields: [
        { k: "Name", t: "text", label: L("名稱", "Name") },
        { k: "Domains", t: "tags", label: L("域名（每行一個，支援 *.）", "Domains (one per line)") },
        { k: "Source", t: "select", options: [["acme", L("ACME 自動申請", "ACME (auto)")], ["upload", L("手動上傳", "Upload")]], label: L("來源", "Source") },
        { k: "CA", t: "select", options: [["letsencrypt", "Let's Encrypt"], ["letsencrypt_staging", "Let's Encrypt (staging)"], ["zerossl", "ZeroSSL"], ["buypass", "Buypass"], ["google", "Google"]], label: "CA", when: it => it.Source === "acme" },
        { k: "Challenge", t: "select", options: [["http-01", "HTTP-01"], ["dns-01", "DNS-01 (Cloudflare)"]], label: L("驗證方式", "Challenge"), when: it => it.Source === "acme" },
        { k: "Email", t: "text", label: L("ACME 帳戶 Email", "ACME account email"), when: it => it.Source === "acme" },
        { k: "KeyType", t: "select", options: [["ec256", "EC-256"], ["ec384", "EC-384"], ["rsa2048", "RSA-2048"]], label: L("金鑰類型", "Key type"), when: it => it.Source === "acme" },
        { k: "CFToken", t: "password", label: L("Cloudflare Token (DNS-01)", "Cloudflare token (DNS-01)"), when: it => it.Source === "acme" && it.Challenge === "dns-01" },
        { k: "AutoRenew", t: "bool", label: L("自動續期", "Auto-renew"), when: it => it.Source === "acme" },
        { k: "CertPEM", t: "code", label: L("證書 PEM（完整鏈）", "Certificate PEM"), when: it => it.Source === "upload" },
        { k: "KeyPEM", t: "code", label: L("私鑰 PEM", "Private key PEM"), when: it => it.Source === "upload" },
        { k: "Remark", t: "text", label: L("備註", "Remark") },
      ],
    },
  },

  cloudflared: {
    top: [{ k: "BinPath", t: "text", label: L("cloudflared 路徑", "cloudflared path"), def: "cloudflared" }],
    list: {
      key: "Tunnels", title: L("隧道", "Tunnels"), singular: L("Cloudflare隧道", "tunnel"),
      columns: ["Name", "Mode"],
      fields: [
        { k: "Name", t: "text", label: L("名稱", "Name") },
        { k: "Enable", t: "bool", label: L("啟用", "Enable") },
        { k: "Mode", t: "select", options: [["token", "Token"], ["config", L("設定檔", "Config file")]], label: L("模式", "Mode") },
        { k: "Token", t: "password", label: "Tunnel Token", when: it => it.Mode !== "config" },
        { k: "ConfigFile", t: "text", label: L("設定檔路徑", "Config file path"), when: it => it.Mode === "config" },
        { k: "ExtraArgs", t: "tags", label: L("額外參數（每行一個）", "Extra args (one per line)") },
      ],
    },
  },

  frp: {
    top: [
      { k: "FrpcPath", t: "text", label: L("frpc 路徑", "frpc path"), def: "frpc" },
      { k: "FrpsPath", t: "text", label: L("frps 路徑", "frps path"), def: "frps" },
    ],
    list: {
      key: "Instances", title: L("實例", "Instances"), singular: L("FRP實例", "FRP instance"),
      columns: ["Name", "Role"],
      fields: [
        { k: "Name", t: "text", label: L("名稱", "Name") },
        { k: "Enable", t: "bool", label: L("啟用", "Enable") },
        { k: "Role", t: "select", options: [["frpc", "frpc (client)"], ["frps", "frps (server)"]], label: L("角色", "Role") },
        { k: "ConfigTOML", t: "code", label: L("設定 (TOML)", "Config (TOML)"), ph: "serverAddr = \"x.x.x.x\"\nserverPort = 7000" },
      ],
    },
  },

  storage: {
    list: {
      key: "Mounts", title: L("存儲掛載", "Storage mounts"), singular: L("存儲掛載", "storage mount"),
      columns: ["Name", "Type", "Path", "ReadOnly"],
      fields: [
        { k: "Name", t: "text", label: L("名稱", "Name") },
        { k: "Type", t: "select", options: [["local", L("本地目錄", "Local dir")]], label: L("類型", "Type") },
        { k: "Path", t: "text", label: L("路徑", "Path"), ph: "/mnt/data" },
        { k: "ReadOnly", t: "bool", label: L("唯讀", "Read-only") },
        { k: "Remark", t: "text", label: L("備註", "Remark") },
      ],
    },
  },

  webdav: {
    top: [
      { k: "Enable", t: "bool", label: L("啟用", "Enable") },
      { k: "ListenIP", t: "text", label: L("監聽 IP（空=全部）", "Listen IP (empty=all)") },
      { k: "Port", t: "number", label: L("埠", "Port"), def: 16680 },
      { k: "StorageID", t: "ref:storage", label: L("存儲掛載", "Storage mount") },
      { k: "User", t: "text", label: L("用戶（空=免認證）", "User (empty=no auth)") },
      { k: "Pass", t: "password", label: L("密碼", "Password") },
      { k: "ReadOnly", t: "bool", label: L("唯讀", "Read-only") },
    ],
  },

  ftpserver: {
    top: [
      { k: "Enable", t: "bool", label: L("啟用", "Enable") },
      { k: "ListenIP", t: "text", label: L("監聽 IP（空=全部）", "Listen IP (empty=all)") },
      { k: "Port", t: "number", label: L("埠", "Port"), def: 16621 },
      { k: "StorageID", t: "ref:storage", label: L("存儲掛載", "Storage mount") },
      { k: "User", t: "text", label: L("用戶（空=匿名）", "User (empty=anonymous)") },
      { k: "Pass", t: "password", label: L("密碼", "Password") },
      { k: "PasvMinPort", t: "number", label: L("被動埠起", "Passive min"), def: 50000 },
      { k: "PasvMaxPort", t: "number", label: L("被動埠止", "Passive max"), def: 50100 },
      { k: "PublicHost", t: "text", label: L("對外 IP（被動）", "Public host (passive)") },
    ],
  },

  docker: {
    top: [
      { k: "Enable", t: "bool", label: L("啟用", "Enable") },
      { k: "Host", t: "text", label: L("Docker 主機", "Docker host"), def: "unix:///var/run/docker.sock" },
    ],
  },

  cron: {
    list: {
      key: "Jobs", title: L("計劃任務", "Cron jobs"), singular: L("計劃任務", "cron job"),
      columns: ["Name", "Cron", "Type", "LastRun", "LastStatus"],
      fields: [
        { k: "Name", t: "text", label: L("名稱", "Name") },
        { k: "Enable", t: "bool", label: L("啟用", "Enable") },
        { k: "Cron", t: "text", label: L("Cron 表達式（5 欄）", "Cron (5 fields)"), ph: "*/5 * * * *" },
        { k: "Type", t: "select", options: [["command", L("執行命令", "Command")], ["webhook", "Webhook"]], label: L("類型", "Type") },
        { k: "Command", t: "textarea", label: L("命令", "Command"), when: it => it.Type !== "webhook" },
        { k: "Webhook.Enable", t: "bool", label: L("啟用 Webhook", "Enable webhook"), when: it => it.Type === "webhook" },
        { k: "Webhook.URL", t: "text", label: "Webhook URL", when: it => it.Type === "webhook" },
        { k: "Webhook.Method", t: "select", options: ["GET", "POST", "PUT"], label: L("方法", "Method"), when: it => it.Type === "webhook" },
        { k: "Webhook.Body", t: "textarea", label: L("請求內容", "Body"), when: it => it.Type === "webhook" },
      ],
    },
  },

  ipfilter: {
    list: {
      key: "Lists", title: L("過濾器", "Filter lists"), singular: L("IP過濾器", "filter list"),
      columns: ["Name", "Type", "Entries"],
      fields: [
        { k: "Name", t: "text", label: L("名稱", "Name") },
        { k: "Type", t: "select", options: [["white", L("白名單", "Whitelist")], ["black", L("黑名單", "Blacklist")]], label: L("類型", "Type") },
        { k: "Entries", t: "tags", label: L("條目（每行一個）", "Entries (one per line)"), help: L("支援 IP、CIDR、範圍 a-b、geo:HK、lan、*", "IP, CIDR, a-b range, geo:HK, lan, *") },
        { k: "Remark", t: "text", label: L("備註", "Remark") },
      ],
    },
  },

  ipdb: {
    top: [
      { k: "Enable", t: "bool", label: L("啟用", "Enable") },
      { k: "DBPath", t: "text", label: L("資料庫路徑（空=預設）", "DB path (empty=default)") },
      { k: "DownloadURL", t: "text", label: L("下載網址 (.mmdb/.gz)", "Download URL") },
    ],
  },

  secgroup: {
    list: {
      key: "Groups", title: L("安全組", "Security groups"), singular: L("安全組", "group"),
      columns: ["Name", "AuthTTLMinutes", "CrossRuleSSO"],
      fields: [
        { k: "Name", t: "text", label: L("名稱", "Name") },
        { k: "AuthTTLMinutes", t: "number", label: L("授權有效（分鐘）", "Auth TTL (min)"), def: 720 },
        { k: "CrossRuleSSO", t: "bool", label: L("跨子規則免登入", "Cross-rule SSO") },
        {
          k: "Users", t: "sublist", label: L("用戶", "Users"),
          sublist: {
            key: "Users", title: L("用戶", "Users"), singular: L("用戶", "user"),
            columns: ["Username", "Remark"],
            fields: [
              { k: "Username", t: "text", label: L("用戶名", "Username") },
              { k: "Password", t: "password", label: L("密碼（留空=不改）", "Password (blank=keep)") },
              { k: "Remark", t: "text", label: L("備註", "Remark") },
            ],
          },
        },
        { k: "Remark", t: "text", label: L("備註", "Remark") },
      ],
    },
  },

  waf: {
    list: {
      key: "Profiles", title: L("WAF 規則集", "WAF profiles"), singular: L("WAF規則集", "WAF profile"),
      columns: ["Name"],
      fields: [
        { k: "Name", t: "text", label: L("名稱", "Name") },
        { k: "Directives", t: "code", label: L("SecLang 指令", "SecLang directives"), ph: "SecRuleEngine On\n..." },
        { k: "Remark", t: "text", label: L("備註", "Remark") },
      ],
    },
  },

  webterminal: {
    top: [
      { k: "Enable", t: "bool", label: L("啟用 Web 終端", "Enable web terminal") },
      { k: "Shell", t: "text", label: L("Shell（空=自動）", "Shell (empty=auto)"), ph: "/bin/bash" },
      { k: "WorkDir", t: "text", label: L("工作目錄（空=資料目錄）", "Working dir (empty=data dir)") },
    ],
  },
};

// ref:<kind> dropdowns pull their options from another module's config.
const REF_SOURCES = {
  ipfilter: { mod: "ipfilter", key: "Lists" },
  secgroup: { mod: "secgroup", key: "Groups" },
  waf: { mod: "waf", key: "Profiles" },
  storage: { mod: "storage", key: "Mounts" },
};

// ---- titled field groups (rendered as boxed sections, like Lucky) ----
function setGroup(fields, keys, g) { for (const f of fields) if (keys.includes(f.k)) f.g = g; }
(function () {
  const d = SCHEMA.ddns.list.fields;
  setGroup(d, ["Creds.Provider", "Creds.ID", "Creds.Secret"], L("DNS 服務商設置", "DNS provider"));
  setGroup(d, ["Stack", "Source", "Interface", "URLs", "IPReg", "Domains", "TTL", "Proxied", "IntervalSec"], L("公網 IP 與域名", "Public IP & domains"));
  setGroup(d, ["Webhook.Enable", "Webhook.URL"], L("Webhook", "Webhook"));

  const p = SCHEMA.portforward.list.fields;
  setGroup(p, ["Protocol", "ListenIP", "ListenPort", "TargetAddr"], L("轉發設置", "Forwarding"));
  setGroup(p, ["IPFilter", "GroupID", "GroupMode"], L("訪問控制", "Access control"));

  const w = SCHEMA.webservice.list.fields;
  setGroup(w, ["ListenIP", "Port", "TLS"], L("監聽設置", "Listener"));
  const ws = w.find(f => f.k === "SubRules").sublist.fields;
  setGroup(ws, ["KeepClientIP", "AppendClientIP", "IgnoreBackendTLS", "HostHeaderOrigin", "CORS"], L("代理設置", "Proxy options"));
  setGroup(ws, ["BasicUser", "BasicPass", "WebLogin", "GroupID", "GroupMode", "IPFilter", "WAFProfile"], L("安全設置", "Security"));

  const s = SCHEMA.ssl.list.fields;
  setGroup(s, ["CA", "Challenge", "Email", "KeyType", "CFToken", "AutoRenew"], L("ACME 設置", "ACME"));
  setGroup(s, ["CertPEM", "KeyPEM"], L("上傳證書", "Upload certificate"));

  const st = SCHEMA.stun.list.fields;
  setGroup(st, ["LocalPort", "STUNServer", "IntervalSec"], L("穿透設置", "Traversal"));
  setGroup(st, ["Webhook.Enable", "Webhook.URL"], L("Webhook", "Webhook"));

  const c = SCHEMA.cron.list.fields;
  setGroup(c, ["Webhook.Enable", "Webhook.URL", "Webhook.Method", "Webhook.Body"], L("Webhook", "Webhook"));

  setGroup(SCHEMA.webdav.top, ["User", "Pass"], L("認證", "Authentication"));
  setGroup(SCHEMA.ftpserver.top, ["User", "Pass"], L("認證", "Authentication"));
  setGroup(SCHEMA.ftpserver.top, ["PasvMinPort", "PasvMaxPort", "PublicHost"], L("被動模式", "Passive mode"));
  setGroup(SCHEMA.cloudflared.list.fields, ["Mode", "Token", "ConfigFile", "ExtraArgs"], L("隧道設置", "Tunnel"));
})();
