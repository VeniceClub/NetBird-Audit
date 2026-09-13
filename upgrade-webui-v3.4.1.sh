#!/usr/bin/env bash
set -euo pipefail

BASE_DIR="/opt/netbird-audit"
SRC_DIR="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="$BASE_DIR/bin"
BACKUP_DIR="$BASE_DIR/backups/webui-$(date +%Y%m%d-%H%M%S)"

if [[ $EUID -ne 0 ]]; then
  echo "Please run as root: sudo ./upgrade-webui-v3.4.1.sh"
  exit 1
fi

if [[ ! -f "$BASE_DIR/config.env" ]]; then
  echo "Existing NetBird Audit installation not found at $BASE_DIR"
  exit 1
fi

mkdir -p "$BACKUP_DIR" "$BIN_DIR"

if [[ -x "$BIN_DIR/audit-server" ]]; then
  cp -a "$BIN_DIR/audit-server" "$BACKUP_DIR/audit-server"
fi

cp -a "$SRC_DIR/internal" "$BASE_DIR/" 2>/dev/null || true
cp -a "$SRC_DIR/cmd" "$BASE_DIR/" 2>/dev/null || true
cp -a "$SRC_DIR/go.mod" "$BASE_DIR/"
[[ -f "$SRC_DIR/go.sum" ]] && cp -a "$SRC_DIR/go.sum" "$BASE_DIR/" || true

BUILD_DIR="$BASE_DIR/src-webui-build"
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"
cp -a "$SRC_DIR/cmd" "$SRC_DIR/internal" "$SRC_DIR/go.mod" "$BUILD_DIR/"
[[ -f "$SRC_DIR/go.sum" ]] && cp -a "$SRC_DIR/go.sum" "$BUILD_DIR/" || true

cd "$BUILD_DIR"
unset GOFLAGS || true
export GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"
go mod tidy
go mod verify
go build -mod=mod -trimpath -ldflags='-s -w' -o "$BUILD_DIR/audit-server" ./cmd/audit-server

install -m 0755 "$BUILD_DIR/audit-server" "$BIN_DIR/audit-server.new"
mv -f "$BIN_DIR/audit-server.new" "$BIN_DIR/audit-server"

systemctl restart netbird-audit-server
sleep 2

if ! systemctl is-active --quiet netbird-audit-server; then
  echo "Web UI upgrade failed; restoring previous audit-server..."
  if [[ -x "$BACKUP_DIR/audit-server" ]]; then
    cp -a "$BACKUP_DIR/audit-server" "$BIN_DIR/audit-server"
    systemctl restart netbird-audit-server
  fi
  exit 1
fi

rm -rf "$BUILD_DIR"

echo
echo "NetBird Audit Web UI upgraded to V3.4.1."
echo "Only audit-server/Web UI was replaced."
echo "MySQL, eBPF sensor, NetBird PAT, TOTP, TLS, sessions and flow history were not modified."
echo "Browser: hard refresh with Cmd+Shift+R / Ctrl+Shift+R."
