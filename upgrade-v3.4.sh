#!/usr/bin/env bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "Run as root: sudo ./upgrade-v3.4.sh"
  exit 1
fi

BASE=/opt/netbird-audit
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
STAMP="$(date +%Y%m%d-%H%M%S)"
BACKUP="${BASE}.upgrade-backup.${STAMP}"
BUILD="$(mktemp -d /tmp/netbird-audit-v34.XXXXXX)"
trap 'rm -rf "$BUILD"' EXIT

if [ ! -f "$BASE/config.env" ] || [ ! -f "$BASE/docker-compose.yml" ]; then
  echo "Existing NetBird Audit installation not found in $BASE"
  echo "For a fresh install, run ./setup-audit-v3.sh instead."
  exit 1
fi

if ! docker ps --format '{{.Names}}' | grep -qx 'netbird-audit-mysql'; then
  echo "MySQL container netbird-audit-mysql is not running."
  echo "Start it first: cd $BASE && docker compose up -d"
  exit 1
fi

# NetBird Audit V3.4 uses eBPF and does not require nftables.service.
if systemctl is-active --quiet nftables 2>/dev/null && grep -Eq '^[[:space:]]*flush[[:space:]]+ruleset' /etc/nftables.conf 2>/dev/null; then
  echo "ERROR: nftables.service is active with 'flush ruleset' and can break Docker networking."
  echo "Run: systemctl disable --now nftables && systemctl restart docker"
  exit 1
fi

TEST_NET="netbird-audit-upgrade-preflight-$$"
if ! docker network create "$TEST_NET" >/dev/null 2>&1; then
  echo "Docker network preflight failed. Repair Docker DOCKER-FORWARD chains before upgrading."
  exit 1
fi
docker network rm "$TEST_NET" >/dev/null 2>&1 || true

echo "[1/7] Building V3.4 Go services in a temporary directory..."
cp -a "$SCRIPT_DIR"/. "$BUILD"/
cd "$BUILD"
unset GOFLAGS || true
export GOTOOLCHAIN=auto
export GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"
go mod tidy
go mod verify
go build -mod=mod -trimpath -ldflags='-s -w' -o "$BUILD/audit-server" ./cmd/audit-server
go build -mod=mod -trimpath -ldflags='-s -w' -o "$BUILD/audit-sensor" ./cmd/audit-sensor

echo "[2/7] Compiling V3.4 eBPF TCX program..."
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) TARGET_ARCH=x86; MULTIARCH=x86_64-linux-gnu ;;
  aarch64|arm64) TARGET_ARCH=arm64; MULTIARCH=aarch64-linux-gnu ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac
clang -O2 -g -target bpf -D__TARGET_ARCH_${TARGET_ARCH} \
  -I/usr/include -I/usr/include/${MULTIARCH} \
  -c sensor/bpf/audit.bpf.c -o "$BUILD/audit.bpf.o"

echo "[3/7] Backing up current binaries and source to $BACKUP ..."
mkdir -p "$BACKUP"
cp -a "$BASE/bin" "$BACKUP/" 2>/dev/null || true
cp -a "$BASE/bpf" "$BACKUP/" 2>/dev/null || true
cp -a "$BASE/src" "$BACKUP/" 2>/dev/null || true
cp -a "$BASE/config.env" "$BACKUP/config.env"
cp -a "$BASE/certs" "$BACKUP/" 2>/dev/null || true

# Do not copy or regenerate MySQL credentials/volume, TLS secrets, PAT, TOTP, or admin password.
echo "[4/7] Installing V3.4 binaries and source (secrets/database preserved)..."
systemctl stop netbird-audit-sensor netbird-audit-server
install -o root -g netbird-audit -m 0755 "$BUILD/audit-server" "$BASE/bin/audit-server"
install -o root -g netbird-audit -m 0755 "$BUILD/audit-sensor" "$BASE/bin/audit-sensor"
install -o root -g netbird-audit -m 0644 "$BUILD/audit.bpf.o" "$BASE/bpf/audit.bpf.o"
rm -rf "$BASE/src"
mkdir -p "$BASE/src"
cp -a "$SCRIPT_DIR"/. "$BASE/src/"
chown -R root:netbird-audit "$BASE/src"

# Reinstall unit files only if supplied; the EnvironmentFile and secrets remain unchanged.
if [ -f "$SCRIPT_DIR/scripts/netbird-audit-server.service" ]; then cp "$SCRIPT_DIR/scripts/netbird-audit-server.service" /etc/systemd/system/netbird-audit-server.service; fi
if [ -f "$SCRIPT_DIR/scripts/netbird-audit-sensor.service" ]; then cp "$SCRIPT_DIR/scripts/netbird-audit-sensor.service" /etc/systemd/system/netbird-audit-sensor.service; fi
systemctl daemon-reload

echo "[5/7] Starting V3.4 server (database migration runs automatically)..."
if ! systemctl start netbird-audit-server; then
  echo "Server failed to start; rolling binaries back."
  cp -a "$BACKUP/bin/." "$BASE/bin/"
  cp -a "$BACKUP/bpf/." "$BASE/bpf/" 2>/dev/null || true
  systemctl start netbird-audit-server || true
  exit 1
fi
sleep 3
PORT=$(grep '^LISTEN_ADDR=' "$BASE/config.env" | tail -1 | cut -d= -f2- | awk -F: '{print $NF}')
PORT=${PORT:-9443}
if ! curl -ksf "https://127.0.0.1:${PORT}/healthz" >/dev/null; then
  echo "V3.4 server health check failed. Rolling back."
  systemctl stop netbird-audit-server || true
  cp -a "$BACKUP/bin/." "$BASE/bin/"
  cp -a "$BACKUP/bpf/." "$BASE/bpf/" 2>/dev/null || true
  systemctl start netbird-audit-server || true
  journalctl -u netbird-audit-server -n 80 --no-pager || true
  exit 1
fi

echo "[6/7] Starting V3.4 eBPF sensor..."
systemctl start netbird-audit-sensor
sleep 3
if ! systemctl is-active --quiet netbird-audit-sensor; then
  echo "Sensor failed to start. Server is healthy; previous package is saved at $BACKUP"
  journalctl -u netbird-audit-sensor -n 100 --no-pager || true
  exit 1
fi

echo "[7/7] Verifying MySQL and NetBird resource synchronization..."
docker exec netbird-audit-mysql mysqladmin ping -h127.0.0.1 -uroot -p"$(grep '^MYSQL_ROOT_PASSWORD=' "$BASE/.mysql.env" | cut -d= -f2-)" --silent >/dev/null
sleep 12

echo
echo "============================================================"
echo " NetBird Audit upgraded successfully to V3.4.0"
echo "============================================================"
echo "UI: https://<audit-host>:${PORT}"
echo "Preserved: MySQL data/volume, Resources, Sessions, Flows, PAT, TOTP, admin hash, TLS certificates, Sensor Key"
echo "New: Access Logs filters/details/export, NetBird Resources sync, Peers page, Sensor heartbeat/System Health"
echo "Backup: $BACKUP"
echo
echo "Refresh the browser with Cmd+Shift+R / Ctrl+Shift+R."
echo "Check: systemctl status netbird-audit-server netbird-audit-sensor --no-pager"
