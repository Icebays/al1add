#!/usr/bin/env bash
# Al1add all-in-one installer: pulls from GitHub, builds, and (optionally) runs
# or installs a systemd service.
#
#   curl -fsSL https://raw.githubusercontent.com/Icebays/al1add/main/install.sh | bash
#
# Environment overrides:
#   REPO=https://github.com/Icebays/al1add.git   git repository
#   BRANCH=main                                   branch / tag to check out
#   INSTALL_DIR=$HOME/al1add                      where to clone and build
#   DATA_DIR=$INSTALL_DIR/data                    config / encrypted store
#   PORT=18890                                    panel port (must match the port set in the panel)
#   RUN=1        run it in the foreground after building
#   SERVICE=1    install & start a systemd service (Linux, needs root/sudo)
#   PUBLIC=1     open the panel port in the host firewall for public access
set -euo pipefail

REPO="${REPO:-https://github.com/Icebays/al1add.git}"
BRANCH="${BRANCH:-main}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/al1add}"
DATA_DIR="${DATA_DIR:-$INSTALL_DIR/data}"
PORT="${PORT:-18890}"
RUN="${RUN:-0}"
SERVICE="${SERVICE:-0}"
PUBLIC="${PUBLIC:-0}"

info() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

# open_firewall opens TCP <port> on the host firewall (ufw / firewalld /
# iptables, whichever exists) so the panel is reachable from the Internet.
open_firewall() {
  local port="$1" SUDO=""
  [ "$(id -u)" -ne 0 ] && SUDO="sudo"
  if command -v ufw >/dev/null 2>&1; then
    info "Opening TCP $port via ufw"
    $SUDO ufw allow "${port}/tcp" >/dev/null 2>&1 && info "ufw: allowed ${port}/tcp" || warn "ufw rule failed (run: sudo ufw allow ${port}/tcp)"
  elif command -v firewall-cmd >/dev/null 2>&1; then
    info "Opening TCP $port via firewalld"
    $SUDO firewall-cmd --permanent --add-port="${port}/tcp" >/dev/null 2>&1 && $SUDO firewall-cmd --reload >/dev/null 2>&1 \
      && info "firewalld: allowed ${port}/tcp" || warn "firewalld rule failed"
  elif command -v iptables >/dev/null 2>&1; then
    info "Opening TCP $port via iptables"
    $SUDO iptables -C INPUT -p tcp --dport "$port" -j ACCEPT >/dev/null 2>&1 || \
      $SUDO iptables -I INPUT -p tcp --dport "$port" -j ACCEPT >/dev/null 2>&1 \
      && info "iptables: allowed ${port}/tcp (not persisted across reboot — save with iptables-save)" || warn "iptables rule failed"
  else
    warn "No firewall tool found (ufw/firewall-cmd/iptables); open TCP ${port} manually."
  fi
  warn "If this host is on a cloud provider or behind a router, also allow TCP ${port} in its security group / port-forwarding."
}

# pubip prints the host's public IP (best effort, empty on failure).
pubip() { curl -fsSL --max-time 5 https://api.ipify.org 2>/dev/null || curl -fsSL --max-time 5 https://ifconfig.me 2>/dev/null || true; }

command -v git >/dev/null 2>&1 || die "git is required. Install git and retry."
command -v go  >/dev/null 2>&1 || die "Go (>=1.24) is required. Install from https://go.dev/dl/ and retry."

# Check Go version >= 1.24
GOVER="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
GOMAJ="${GOVER%%.*}"; GOREST="${GOVER#*.}"; GOMIN="${GOREST%%.*}"
if [ "${GOMAJ:-0}" -lt 1 ] || { [ "${GOMAJ:-0}" -eq 1 ] && [ "${GOMIN:-0}" -lt 24 ]; }; then
  die "Go $GOVER found, but >= 1.24 is required."
fi

# Fetch or update the source.
if [ -d "$INSTALL_DIR/.git" ]; then
  info "Updating existing checkout in $INSTALL_DIR"
  git -C "$INSTALL_DIR" fetch --depth 1 origin "$BRANCH"
  git -C "$INSTALL_DIR" checkout -q "$BRANCH"
  git -C "$INSTALL_DIR" reset --hard "origin/$BRANCH"
else
  info "Cloning $REPO ($BRANCH) into $INSTALL_DIR"
  git clone --depth 1 --branch "$BRANCH" "$REPO" "$INSTALL_DIR"
fi

# Build.
info "Building al1add (this may take a minute on first run)…"
( cd "$INSTALL_DIR" && go build -o al1add . )
BIN="$INSTALL_DIR/al1add"
[ -x "$BIN" ] || die "build failed: $BIN not found"
info "Built $("$BIN" -version)"

mkdir -p "$DATA_DIR"

# Open the firewall for public access if requested.
[ "$PUBLIC" = "1" ] && open_firewall "$PORT"

# Work out a URL to show the user.
URL_HOST="<this-host>"
if [ "$PUBLIC" = "1" ]; then
  IP="$(pubip)"; [ -n "$IP" ] && URL_HOST="$IP"
fi

if [ "$SERVICE" = "1" ]; then
  [ "$(uname -s)" = "Linux" ] || die "SERVICE=1 is only supported on Linux (systemd)."
  SUDO=""; [ "$(id -u)" -ne 0 ] && SUDO="sudo"
  UNIT=/etc/systemd/system/al1add.service
  info "Installing systemd service at $UNIT"
  $SUDO tee "$UNIT" >/dev/null <<EOF
[Unit]
Description=Al1add network toolbox
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$BIN -cd $DATA_DIR
Restart=always
RestartSec=3
WorkingDirectory=$INSTALL_DIR

[Install]
WantedBy=multi-user.target
EOF
  $SUDO systemctl daemon-reload
  $SUDO systemctl enable al1add >/dev/null 2>&1 || true
  $SUDO systemctl restart al1add   # restart so a rebuilt binary is picked up
  info "Service started. Logs:  journalctl -u al1add -f"
  info "Initial admin password (first start only):"
  sleep 2
  $SUDO journalctl -u al1add --no-pager 2>/dev/null | grep -m1 "password =" || warn "Check: journalctl -u al1add | grep password"
  echo
  info "Open the panel at:  http://${URL_HOST}:${PORT}/"
  [ "$PUBLIC" != "1" ] && info "For public access, re-run with PUBLIC=1 to open the firewall port."
  exit 0
fi

echo
info "Done. To start the panel:"
echo "    cd $INSTALL_DIR && ./al1add -cd $DATA_DIR"
info "Then open  http://${URL_HOST}:${PORT}/  (the initial admin password is printed to the log on first start)."
[ "$PUBLIC" != "1" ] && info "For public access, re-run with PUBLIC=1 to open the firewall port."
echo

if [ "$RUN" = "1" ]; then
  info "Starting al1add (Ctrl-C to stop)…"
  exec "$BIN" -cd "$DATA_DIR"
fi
