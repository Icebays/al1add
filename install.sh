#!/usr/bin/env bash
# =============================================================================
#  Al1add 安裝管理程式 / installer & manager
#
#  互動選單 / interactive menu:
#    bash <(curl -fsSL https://raw.githubusercontent.com/Icebays/al1add/main/install.sh)
#    curl -fsSL https://raw.githubusercontent.com/Icebays/al1add/main/install.sh | bash
#
#  直接執行某個動作 / run one action:
#    curl -fsSL .../install.sh | bash -s -- install|update|service|public|start|stop|
#                                         restart|status|logs|resetpass|setport|backup|uninstall
#
#  自動模式 (無互動) / unattended (legacy flags):
#    curl -fsSL .../install.sh | SERVICE=1 PUBLIC=1 bash
#
#  環境變量 / environment:
#    REPO BRANCH INSTALL_DIR DATA_DIR PORT  RUN=1 SERVICE=1 PUBLIC=1
# =============================================================================
set -uo pipefail

REPO="${REPO:-https://github.com/Icebays/al1add.git}"
BRANCH="${BRANCH:-main}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/al1add}"
DATA_DIR="${DATA_DIR:-$INSTALL_DIR/data}"
BIN="$INSTALL_DIR/al1add"
LOG_FILE="$INSTALL_DIR/al1add.log"
PID_FILE="$INSTALL_DIR/al1add.pid"
UNIT="/etc/systemd/system/al1add.service"
DEFAULT_PORT=18890
GO_FALLBACK="go1.24.7"

# ----------------------------------------------------------------- output
if [ -t 1 ]; then
  C0=$'\033[0m'; CB=$'\033[1m'; CR=$'\033[31m'; CG=$'\033[32m'; CY=$'\033[33m'; CBL=$'\033[34m'; CC=$'\033[36m'; CD=$'\033[2m'
else
  C0=""; CB=""; CR=""; CG=""; CY=""; CBL=""; CC=""; CD=""
fi
info() { printf '%s==>%s %s\n' "$CBL$CB" "$C0" "$*"; }
ok()   { printf '%s ✓ %s %s\n' "$CG$CB" "$C0" "$*"; }
warn() { printf '%s [!]%s %s\n' "$CY$CB" "$C0" "$*" >&2; }
err()  { printf '%s [x]%s %s\n' "$CR$CB" "$C0" "$*" >&2; }
die()  { err "$*"; exit 1; }
line() { printf '%s%s%s\n' "$CD" "────────────────────────────────────────────────────────────" "$C0"; }

# ----------------------------------------------------------------- input (works under curl | bash)
TTY=""
if [ -t 0 ]; then TTY=/dev/stdin
elif { exec 3</dev/tty; } 2>/dev/null; then exec 3<&-; TTY=/dev/tty
fi
ask() {  # ask "prompt" [default] -> $REPLY
  local p="$1" d="${2:-}"
  if [ -z "$TTY" ]; then REPLY="$d"; return; fi
  if [ -n "$d" ]; then read -r -p "$p [$d]: " REPLY <"$TTY" || REPLY=""; else read -r -p "$p: " REPLY <"$TTY" || REPLY=""; fi
  [ -z "$REPLY" ] && REPLY="$d"
}
ask_secret() {
  [ -z "$TTY" ] && { REPLY=""; return; }
  read -r -s -p "$1: " REPLY <"$TTY" || REPLY=""; echo
}
confirm() {  # confirm "question" [y|n default]
  local d="${2:-n}" hint="y/N"; [ "$d" = "y" ] && hint="Y/n"
  ask "$1 ($hint)" ""
  local a="${REPLY:-$d}"; [[ "$a" =~ ^[Yy] ]]
}
pause() { [ -n "$TTY" ] && { echo; read -r -p "按 Enter 返回選單 / Press Enter to continue…" _ <"$TTY" || true; }; }

# ----------------------------------------------------------------- environment
SUDO=""
if [ "$(id -u)" -ne 0 ]; then command -v sudo >/dev/null 2>&1 && SUDO="sudo"; fi
OS="$(uname -s)"
has_systemd() { command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; }
service_installed() { [ -f "$UNIT" ]; }
is_installed() { [ -x "$BIN" ]; }
version() { is_installed && "$BIN" -version 2>/dev/null | awk '{print $2}'; }

is_running() {
  if service_installed && has_systemd; then systemctl is-active --quiet al1add && return 0; fi
  [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE" 2>/dev/null)" 2>/dev/null
}
run_mode() {
  if service_installed && has_systemd && systemctl is-active --quiet al1add; then echo "systemd"
  elif [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE" 2>/dev/null)" 2>/dev/null; then echo "background"
  else echo ""; fi
}
panel_port() {
  if is_installed && [ -f "$DATA_DIR/base.alcf" ]; then
    local p; p="$("$BIN" -cd "$DATA_DIR" -getport 2>/dev/null | tail -n1)"
    [[ "$p" =~ ^[0-9]+$ ]] && { echo "$p"; return; }
  fi
  echo "${PORT:-$DEFAULT_PORT}"
}
lan_ip() {
  local ip=""
  command -v ip >/dev/null 2>&1 && ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}')"
  [ -z "$ip" ] && command -v hostname >/dev/null 2>&1 && ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  echo "${ip:-127.0.0.1}"
}
PUBIP_CACHE=""; PUBIP_DONE=0
fetch_public_ip() {  # fills PUBIP_CACHE once, in the current shell (not a subshell)
  [ "$PUBIP_DONE" = "1" ] && return
  PUBIP_CACHE="$(curl -fsSL --max-time 4 https://api.ipify.org 2>/dev/null || curl -fsSL --max-time 4 https://ifconfig.me 2>/dev/null || true)"
  [[ "$PUBIP_CACHE" =~ ^[0-9a-fA-F:.]+$ ]] || PUBIP_CACHE=""
  PUBIP_DONE=1
}

# ----------------------------------------------------------------- dependencies
pkg_install() {  # pkg_install <package>
  local p="$1"
  if command -v apt-get >/dev/null 2>&1; then $SUDO apt-get update -qq && $SUDO apt-get install -y -qq "$p"
  elif command -v dnf >/dev/null 2>&1; then $SUDO dnf install -y -q "$p"
  elif command -v yum >/dev/null 2>&1; then $SUDO yum install -y -q "$p"
  elif command -v apk >/dev/null 2>&1; then $SUDO apk add --no-cache "$p"
  elif command -v pacman >/dev/null 2>&1; then $SUDO pacman -Sy --noconfirm "$p"
  elif command -v zypper >/dev/null 2>&1; then $SUDO zypper -n install "$p"
  elif command -v brew >/dev/null 2>&1; then brew install "$p"
  else return 1; fi
}

ensure_tools() {
  for t in git curl tar; do
    if ! command -v "$t" >/dev/null 2>&1; then
      info "安裝 $t / installing $t"
      pkg_install "$t" || { err "請手動安裝 $t / please install $t manually"; return 1; }
    fi
  done
}

go_ok() {
  command -v go >/dev/null 2>&1 || return 1
  local v maj min; v="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
  maj="${v%%.*}"; min="${v#*.}"; min="${min%%.*}"
  [ "${maj:-0}" -gt 1 ] || { [ "${maj:-0}" -eq 1 ] && [ "${min:-0}" -ge 24 ]; }
}

ensure_go() {
  # pick up a Go we installed earlier
  for d in /usr/local/go/bin "$HOME/.local/go/bin"; do [ -x "$d/go" ] && case ":$PATH:" in *":$d:"*) ;; *) PATH="$d:$PATH";; esac; done
  export PATH
  go_ok && return 0
  warn "未搵到 Go ≥ 1.24，會自動下載安裝 / Go ≥ 1.24 not found, installing it"
  local os arch ver url dest parent
  case "$OS" in Linux) os=linux;; Darwin) os=darwin;; *) err "不支援系統 $OS / unsupported OS"; return 1;; esac
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64;; aarch64|arm64) arch=arm64;; armv7l|armv6l|armhf) arch=armv6l;;
    i386|i686) arch=386;; riscv64) arch=riscv64;; *) err "不支援架構 $(uname -m)"; return 1;;
  esac
  ver="$(curl -fsSL --max-time 10 'https://go.dev/VERSION?m=text' 2>/dev/null | head -n1)"
  [[ "$ver" =~ ^go1\.[0-9]+ ]] || ver="$GO_FALLBACK"
  url="https://go.dev/dl/${ver}.${os}-${arch}.tar.gz"
  if [ "$(id -u)" -eq 0 ]; then parent=/usr/local; else parent="$HOME/.local"; fi
  dest="$parent/go"
  info "下載 / downloading $url"
  mkdir -p "$parent" || return 1
  local tmp; tmp="$(mktemp -d)"
  if ! curl -fSL --progress-bar "$url" -o "$tmp/go.tgz"; then err "下載 Go 失敗 / Go download failed"; rm -rf "$tmp"; return 1; fi
  rm -rf "$dest" && tar -C "$parent" -xzf "$tmp/go.tgz" || { err "解壓失敗 / extract failed"; rm -rf "$tmp"; return 1; }
  rm -rf "$tmp"
  PATH="$dest/bin:$PATH"; export PATH
  go_ok || { err "Go 安裝後仍不可用 / Go still unavailable"; return 1; }
  ok "Go $(go env GOVERSION) → $dest"
}

# ----------------------------------------------------------------- firewall
open_firewall() {
  local port="$1"
  if command -v ufw >/dev/null 2>&1 && $SUDO ufw status 2>/dev/null | grep -qi active; then
    $SUDO ufw allow "${port}/tcp" >/dev/null 2>&1 && ok "ufw: 已放行 / allowed ${port}/tcp" || warn "ufw 失敗 / failed: sudo ufw allow ${port}/tcp"
  elif command -v firewall-cmd >/dev/null 2>&1 && $SUDO firewall-cmd --state >/dev/null 2>&1; then
    $SUDO firewall-cmd --permanent --add-port="${port}/tcp" >/dev/null 2>&1 && $SUDO firewall-cmd --reload >/dev/null 2>&1 \
      && ok "firewalld: 已放行 / allowed ${port}/tcp" || warn "firewalld 失敗 / failed"
  elif command -v iptables >/dev/null 2>&1; then
    if $SUDO iptables -C INPUT -p tcp --dport "$port" -j ACCEPT >/dev/null 2>&1 || \
       $SUDO iptables -I INPUT -p tcp --dport "$port" -j ACCEPT >/dev/null 2>&1; then
      ok "iptables: 已放行 / allowed ${port}/tcp"
      warn "iptables 規則重開機後會消失，可用 iptables-save 保存 / not persistent across reboot"
    else warn "iptables 失敗 / failed"; fi
  else
    warn "未偵測到防火牆工具 / no firewall tool found — 如有防火牆請手動放行 TCP ${port}"
  fi
  warn "雲端主機 (阿里雲/騰訊雲/AWS…) 或路由器後面：記得喺「安全組 / 端口轉發」放行 TCP ${port}"
}

# ----------------------------------------------------------------- actions
FIRST_RUN=0; START_TS=0
mark_start() { START_TS="$(date +%s)"; FIRST_RUN=0; [ -f "$DATA_DIR/base.alcf" ] || FIRST_RUN=1; }
show_initial_password() {
  [ "$FIRST_RUN" = "1" ] || return 0   # only a brand-new data dir prints a password
  local pw="" i
  for i in 1 2 3 4 5 6; do
    if service_installed && has_systemd; then
      pw="$($SUDO journalctl -u al1add --since "@$START_TS" --no-pager 2>/dev/null | grep -o 'password = [A-Za-z0-9]*' | tail -n1 | awk '{print $3}')"
    elif [ -f "$LOG_FILE" ]; then
      pw="$(grep -o 'password = [A-Za-z0-9]*' "$LOG_FILE" 2>/dev/null | tail -n1 | awk '{print $3}')"
    fi
    [ -n "$pw" ] && break; sleep 1
  done
  if [ -n "$pw" ]; then
    echo; ok "首次登入 / first login:  用戶 user = ${CB}admin${C0}   密碼 password = ${CB}${CG}${pw}${C0}"
    warn "登入後請即刻喺「系統設定」改密碼 / change it after logging in"
  fi
}

print_urls() {
  local port; port="$(panel_port)"
  echo "   區域網 / LAN   : ${CC}http://$(lan_ip):${port}/${C0}"
  fetch_public_ip
  [ -n "$PUBIP_CACHE" ] && echo "   公網 / Public  : ${CC}http://${PUBIP_CACHE}:${port}/${C0}  ${CD}(需放行防火牆 / needs firewall open)${C0}"
}

do_install() {
  ensure_tools || return 1
  ensure_go || return 1
  if [ -d "$INSTALL_DIR/.git" ]; then
    info "更新原始碼 / updating source in $INSTALL_DIR"
    git -C "$INSTALL_DIR" fetch -q --depth 1 origin "$BRANCH" || { err "git fetch 失敗 / failed"; return 1; }
    git -C "$INSTALL_DIR" checkout -q "$BRANCH" 2>/dev/null || git -C "$INSTALL_DIR" checkout -q -B "$BRANCH" "origin/$BRANCH"
    git -C "$INSTALL_DIR" reset -q --hard "origin/$BRANCH" || { err "git 更新失敗 / git update failed"; return 1; }
  else
    if [ -e "$INSTALL_DIR" ] && [ -n "$(ls -A "$INSTALL_DIR" 2>/dev/null | grep -v '^data$')" ]; then
      err "$INSTALL_DIR 已存在且唔係 git 目錄 / exists and is not a git checkout"; return 1
    fi
    info "下載原始碼 / cloning $REPO ($BRANCH)"
    mkdir -p "$INSTALL_DIR"
    git clone -q --depth 1 --branch "$BRANCH" "$REPO" "$INSTALL_DIR.tmp" || { err "clone 失敗 / failed"; rm -rf "$INSTALL_DIR.tmp"; return 1; }
    cp -a "$INSTALL_DIR.tmp/." "$INSTALL_DIR/" && rm -rf "$INSTALL_DIR.tmp"
  fi
  info "編譯中，第一次可能要一兩分鐘 / building (first build may take a minute)…"
  ( cd "$INSTALL_DIR" && GOTOOLCHAIN=local go build -trimpath -ldflags "-s -w" -o "$BIN.new" . ) || { err "編譯失敗 / build failed"; return 1; }
  mv -f "$BIN.new" "$BIN"
  mkdir -p "$DATA_DIR"
  ok "已安裝 / installed al1add $(version) → $BIN"
  if is_running; then info "重啟以套用新版本 / restarting to apply update"; do_restart; fi
}

do_update() { do_install; }

write_unit() {
  $SUDO tee "$UNIT" >/dev/null <<EOF
[Unit]
Description=Al1add network toolbox
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$BIN -cd $DATA_DIR
WorkingDirectory=$INSTALL_DIR
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
}

do_service() {
  is_installed || { info "未安裝，先安裝 / not installed yet, installing first"; do_install || return 1; }
  has_systemd || { err "此系統冇 systemd / systemd not available — 可用「啟動」以背景模式運行"; return 1; }
  # stop a background instance first
  if [ -f "$PID_FILE" ]; then kill "$(cat "$PID_FILE")" 2>/dev/null; rm -f "$PID_FILE"; sleep 1; fi
  info "建立系統服務 / installing systemd service ($UNIT)"
  write_unit || return 1
  $SUDO systemctl daemon-reload
  $SUDO systemctl enable al1add >/dev/null 2>&1
  mark_start
  $SUDO systemctl restart al1add
  sleep 2
  if systemctl is-active --quiet al1add; then ok "服務已啟動，開機自動運行 / service running, starts on boot"
  else err "服務啟動失敗 / service failed:  journalctl -u al1add -n 50"; return 1; fi
  show_initial_password
  print_urls
}

do_public() {
  is_installed || { warn "程式未安裝 / not installed — 仍會開防火牆"; }
  local port; port="$(panel_port)"
  info "開放公網訪問：TCP $port / opening TCP $port for public access"
  open_firewall "$port"
  echo
  ok "面板預設已接受任何來源連線（有登入保護）/ the panel accepts any source by default (login-protected)"
  print_urls
}

do_start() {
  is_installed || { err "未安裝 / not installed (選 1)"; return 1; }
  if is_running; then ok "已在運行 / already running ($(run_mode))"; return 0; fi
  mark_start
  if service_installed && has_systemd; then
    $SUDO systemctl start al1add && sleep 2 && systemctl is-active --quiet al1add && ok "已啟動 / started (systemd)" || { err "啟動失敗 / failed"; return 1; }
  else
    info "背景模式啟動 / starting in background (log: $LOG_FILE)"
    nohup "$BIN" -cd "$DATA_DIR" >>"$LOG_FILE" 2>&1 &
    echo $! >"$PID_FILE"; sleep 2
    kill -0 "$(cat "$PID_FILE")" 2>/dev/null && ok "已啟動 / started (pid $(cat "$PID_FILE"))" || { err "啟動失敗，睇日誌 / failed, see $LOG_FILE"; return 1; }
    warn "背景模式重開機後唔會自動啟動；建議選「2 安裝為系統服務」/ not started on boot — use option 2"
  fi
  show_initial_password
  print_urls
}

do_stop() {
  if service_installed && has_systemd; then $SUDO systemctl stop al1add && ok "已停止 / stopped (systemd)"; fi
  if [ -f "$PID_FILE" ]; then kill "$(cat "$PID_FILE")" 2>/dev/null && ok "已停止 / stopped (pid $(cat "$PID_FILE"))"; rm -f "$PID_FILE"; fi
  return 0
}

do_restart() {
  if service_installed && has_systemd; then
    $SUDO systemctl restart al1add && sleep 2 && systemctl is-active --quiet al1add && ok "已重啟 / restarted" || err "重啟失敗 / restart failed"
  else do_stop; do_start; fi
}

do_control() {
  echo "  1) 啟動 / Start     2) 停止 / Stop     3) 重啟 / Restart     0) 返回 / Back"
  ask "請選擇 / choose" "3"
  case "$REPLY" in 1) do_start;; 2) do_stop;; 3) do_restart;; *) return 0;; esac
}

show_logs() {
  local n="${1:-40}"
  if service_installed && has_systemd; then $SUDO journalctl -u al1add -n "$n" --no-pager 2>/dev/null
  elif [ -f "$LOG_FILE" ]; then tail -n "$n" "$LOG_FILE"
  else warn "暫時冇日誌 / no logs yet"; fi
}
follow_logs() {
  info "跟蹤日誌，按 Ctrl+C 返回 / following logs, Ctrl+C to return"
  trap 'true' INT
  if service_installed && has_systemd; then $SUDO journalctl -u al1add -f -n 20
  elif [ -f "$LOG_FILE" ]; then tail -n 20 -f "$LOG_FILE"; fi
  trap - INT
}

do_status() {
  line
  if is_installed; then echo "  版本 Version : ${CB}$(version)${C0}   ($BIN)"; else echo "  版本 Version : ${CY}未安裝 / not installed${C0}"; return 0; fi
  local m; m="$(run_mode)"
  if [ -n "$m" ]; then echo "  狀態 Status  : ${CG}● 運行中 running${C0} ($m)"; else echo "  狀態 Status  : ${CR}○ 已停止 stopped${C0}"; fi
  echo "  開機自啟 Boot: $(service_installed && echo "${CG}是 yes${C0}" || echo "否 no")"
  echo "  資料 Data    : $DATA_DIR"
  print_urls
  line
  info "最近日誌 / recent logs"
  show_logs 25
  if [ -n "$TTY" ]; then
    ask "輸入 f 持續跟蹤日誌，Enter 返回 / f = follow, Enter = back" ""
    [[ "$REPLY" =~ ^[Ff] ]] && follow_logs
  fi
}

do_resetpass() {
  is_installed || { err "未安裝 / not installed"; return 1; }
  ask "管理員用戶名 / admin username" "admin"; local u="$REPLY"
  ask_secret "新密碼 (最少 6 位) / new password (min 6)"; local p1="$REPLY"
  ask_secret "再輸入一次 / repeat"; local p2="$REPLY"
  [ "${#p1}" -ge 6 ] || { err "密碼太短 / too short"; return 1; }
  [ "$p1" = "$p2" ] || { err "兩次密碼唔一致 / passwords do not match"; return 1; }
  "$BIN" -cd "$DATA_DIR" -resetuser "$u" -resetpass "$p1" >/dev/null 2>&1 || { err "重設失敗 / failed"; return 1; }
  ok "已重設 / reset: 用戶 user = $u"
  is_running && do_restart
}

do_setport() {
  is_installed || { err "未安裝 / not installed"; return 1; }
  local cur; cur="$(panel_port)"
  ask "新面板埠 / new panel port" "$cur"; local np="$REPLY"
  [[ "$np" =~ ^[0-9]+$ ]] && [ "$np" -ge 1 ] && [ "$np" -le 65535 ] || { err "無效埠 / invalid port"; return 1; }
  "$BIN" -cd "$DATA_DIR" -setport "$np" || { err "設定失敗 / failed"; return 1; }
  ok "面板埠已改為 / panel port set to $np"
  if confirm "同時喺防火牆放行 TCP $np？/ also open TCP $np in the firewall?" "y"; then open_firewall "$np"; fi
  is_running && do_restart
  print_urls
}

do_backup() {
  [ -d "$DATA_DIR" ] || { err "搵唔到資料目錄 / data dir not found: $DATA_DIR"; return 1; }
  local out="$HOME/al1add-backup-$(date +%Y%m%d-%H%M%S).tar.gz"
  tar -czf "$out" -C "$(dirname "$DATA_DIR")" "$(basename "$DATA_DIR")" || { err "備份失敗 / backup failed"; return 1; }
  chmod 600 "$out"
  ok "已備份 / backup saved: ${CB}$out${C0}"
  warn "備份包含 master.key，請妥善保管 / contains master.key — keep it private"
}

do_uninstall() {
  confirm "確定卸載 Al1add？/ uninstall Al1add?" "n" || { info "已取消 / cancelled"; return 0; }
  do_stop
  if service_installed; then
    $SUDO systemctl disable al1add >/dev/null 2>&1
    $SUDO rm -f "$UNIT"; $SUDO systemctl daemon-reload 2>/dev/null
    ok "已移除系統服務 / service removed"
  fi
  if confirm "保留設定資料 (data)？/ keep your configuration data?" "y"; then
    find "$INSTALL_DIR" -mindepth 1 -maxdepth 1 ! -name "$(basename "$DATA_DIR")" -exec rm -rf {} + 2>/dev/null
    ok "已卸載，資料保留喺 / uninstalled, data kept at: $DATA_DIR"
  else
    rm -rf "$INSTALL_DIR"
    ok "已完全移除 / fully removed: $INSTALL_DIR"
  fi
}

# ----------------------------------------------------------------- menu
banner() {
  [ -n "$TTY" ] && command -v clear >/dev/null 2>&1 && clear
  echo "${CBL}${CB}"
  echo "   █████╗ ██╗      ██╗ █████╗ ██████╗ ██████╗ "
  echo "  ██╔══██╗██║     ███║██╔══██╗██╔══██╗██╔══██╗"
  echo "  ███████║██║     ╚██║███████║██║  ██║██║  ██║"
  echo "  ██╔══██║██║      ██║██╔══██║██║  ██║██║  ██║"
  echo "  ██║  ██║███████╗ ██║██║  ██║██████╔╝██████╔╝"
  echo "  ╚═╝  ╚═╝╚══════╝ ╚═╝╚═╝  ╚═╝╚═════╝ ╚═════╝ ${C0}"
  echo "   ${CB}網絡工具箱 · 安裝管理程式${C0}  ${CD}Network toolbox · installer & manager${C0}"
  line
  if is_installed; then
    local m; m="$(run_mode)"
    printf "   版本 %s   狀態 %s   埠 %s\n" "${CB}$(version)${C0}" \
      "$([ -n "$m" ] && echo "${CG}● 運行中${C0}($m)" || echo "${CR}○ 已停止${C0}")" "${CB}$(panel_port)${C0}"
    [ -n "$m" ] && echo "   面板 ${CC}http://$(lan_ip):$(panel_port)/${C0}"
  else
    echo "   狀態 ${CY}未安裝 / not installed${C0}   目錄 $INSTALL_DIR"
  fi
  line
}

menu() {
  cat <<EOF
   ${CB}1)${C0} 安裝 / 更新          ${CD}Install / Update${C0}
   ${CB}2)${C0} 安裝為系統服務       ${CD}Install as service (auto-start on boot)${C0}
   ${CB}3)${C0} 開放公網訪問         ${CD}Open firewall for public access${C0}
   ${CB}4)${C0} 啟動 / 停止 / 重啟    ${CD}Start / Stop / Restart${C0}
   ${CB}5)${C0} 查看狀態 / 日誌       ${CD}Status & logs${C0}
   ${CB}6)${C0} 重設管理員密碼       ${CD}Reset admin password${C0}
   ${CB}7)${C0} 更改面板埠           ${CD}Change panel port${C0}
   ${CB}8)${C0} 備份資料             ${CD}Backup data${C0}
   ${CB}9)${C0} 卸載                 ${CD}Uninstall${C0}
   ${CB}0)${C0} 退出                 ${CD}Exit${C0}
EOF
  line
}

run_action() {
  case "$1" in
    1|install) do_install;;
    update) do_update;;
    2|service) do_service;;
    3|public) do_public;;
    4|control) do_control;;
    start) do_start;; stop) do_stop;; restart) do_restart;;
    5|status) do_status;;
    logs) show_logs 100;;
    6|resetpass) do_resetpass;;
    7|setport) do_setport;;
    8|backup) do_backup;;
    9|uninstall) do_uninstall;;
    0|q|exit|quit) echo "再見 / bye"; exit 0;;
    *) err "無效選項 / invalid option: $1"; return 1;;
  esac
}

main() {
  # 1) explicit action:  bash install.sh <action>
  if [ $# -gt 0 ]; then run_action "$1"; exit $?; fi

  # 2) legacy unattended flags, or no keyboard available
  if [ "${SERVICE:-0}" = "1" ] || [ "${PUBLIC:-0}" = "1" ] || [ "${RUN:-0}" = "1" ] || [ -z "$TTY" ]; then
    do_install || exit 1
    [ "${PUBLIC:-0}" = "1" ] && do_public
    if [ "${SERVICE:-0}" = "1" ]; then do_service || exit 1
    elif [ "${RUN:-0}" = "1" ]; then info "前景運行，Ctrl+C 停止 / running in foreground"; exec "$BIN" -cd "$DATA_DIR"
    else
      echo; info "啟動 / start:  cd $INSTALL_DIR && ./al1add -cd $DATA_DIR"
      info "或者重新執行並揀選單 / or re-run interactively for the menu"
    fi
    exit 0
  fi

  # 3) interactive menu
  while true; do
    banner; menu
    ask "請輸入選項 / choose [0-9]" ""
    echo
    run_action "${REPLY:-}"
    pause
  done
}

main "$@"
