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
#   PORT=18890                                    panel port (first run only)
#   RUN=1        run it in the foreground after building
#   SERVICE=1    install & start a systemd service (Linux, needs root/sudo)
set -euo pipefail

REPO="${REPO:-https://github.com/Icebays/al1add.git}"
BRANCH="${BRANCH:-main}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/al1add}"
DATA_DIR="${DATA_DIR:-$INSTALL_DIR/data}"
PORT="${PORT:-18890}"
RUN="${RUN:-0}"
SERVICE="${SERVICE:-0}"

info() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

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

# Seed the panel port on first run (base.alcf is created on first start).
EXTRA=()
[ -n "$PORT" ] && EXTRA+=(-cd "$DATA_DIR")

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
  $SUDO systemctl enable --now al1add
  info "Service started. Logs:  journalctl -u al1add -f"
  info "Initial admin password (first start only):"
  sleep 2
  $SUDO journalctl -u al1add --no-pager 2>/dev/null | grep -m1 "password =" || warn "Check: journalctl -u al1add | grep password"
  echo
  info "Open the panel at:  http://<this-host>:${PORT}/"
  exit 0
fi

echo
info "Done. To start the panel:"
echo "    cd $INSTALL_DIR && ./al1add -cd $DATA_DIR"
info "Then open  http://<this-host>:${PORT}/  (the initial admin password is printed to the log on first start)."
echo

if [ "$RUN" = "1" ]; then
  info "Starting al1add (Ctrl-C to stop)…"
  exec "$BIN" -cd "$DATA_DIR"
fi
