#!/usr/bin/env bash
set -euo pipefail

# Never leak credentials even if invoked with `bash -x`.
case "$-" in *x*) set +x; echo "[security] xtrace disabled to protect secrets.";; esac

if [ "$(id -u)" -ne 0 ]; then
  echo "Run as root: sudo ./setup-audit-v3.sh"
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BASE_DIR=/opt/netbird-audit
SRC_DIR="$BASE_DIR/src"

read -rp 'NetBird URL [https://vpn.eks-tools.com]: ' NETBIRD_URL
NETBIRD_URL=${NETBIRD_URL:-https://vpn.eks-tools.com}
read -rsp 'NetBird PAT: ' NETBIRD_PAT; echo
if [ -z "$NETBIRD_PAT" ]; then echo "PAT is required"; exit 1; fi
AUTO_IPCIDR="$(netbird status 2>/dev/null | awk -F': ' '/^NetBird IP:/ {print $2; exit}' || true)"
AUTO_IF=""
AUTO_CIDR=""
if [ -n "$AUTO_IPCIDR" ]; then
  AUTO_IP="${AUTO_IPCIDR%/*}"
  AUTO_IF="$(ip -o -4 addr show | awk -v ip="$AUTO_IP" '$4 ~ ("^" ip "/") {print $2; exit}')"
  if [ -n "$AUTO_IF" ]; then
    AUTO_CIDR="$(ip -4 route show dev "$AUTO_IF" proto kernel scope link 2>/dev/null | awk '$1 ~ /\// {print $1; exit}')"
  fi
fi

if [ -n "$AUTO_CIDR" ]; then
  echo "Detected NetBird interface: $AUTO_IF, overlay: $AUTO_CIDR"
fi
read -rp "NetBird overlay CIDR [${AUTO_CIDR:-100.93.0.0/16}]: " OVERLAY_CIDR
OVERLAY_CIDR=${OVERLAY_CIDR:-${AUTO_CIDR:-100.93.0.0/16}}
read -rp "NetBird interface [${AUTO_IF:-auto}]: " NETBIRD_INTERFACE
NETBIRD_INTERFACE=${NETBIRD_INTERFACE:-${AUTO_IF:-auto}}
read -rp 'Audit HTTPS bind [0.0.0.0:9443]: ' AUDIT_LISTEN
AUDIT_LISTEN=${AUDIT_LISTEN:-0.0.0.0:9443}
read -rp 'Audit certificate CN [audit.eks-tools.com]: ' AUDIT_CN
AUDIT_CN=${AUDIT_CN:-audit.eks-tools.com}
read -rp 'Audit admin username [auditadmin]: ' ADMIN_USER
ADMIN_USER=${ADMIN_USER:-auditadmin}
read -rsp 'Audit admin password (min 14 chars): ' ADMIN_PASSWORD; echo
if [ ${#ADMIN_PASSWORD} -lt 14 ]; then echo "Password must be at least 14 chars"; exit 1; fi

# Verify PAT before changing the host.
HTTP_CODE=$(curl -sS -o /tmp/netbird-audit-peers.json -w '%{http_code}' \
  -H "Authorization: Token ${NETBIRD_PAT}" \
  -H 'Accept: application/json' \
  "${NETBIRD_URL%/}/api/peers" || true)
if [ "$HTTP_CODE" != "200" ]; then
  echo "NetBird PAT verification failed (HTTP $HTTP_CODE)."
  echo "Create a fresh PAT and retry."
  exit 1
fi
rm -f /tmp/netbird-audit-peers.json

echo "[1/8] Installing build/runtime dependencies..."
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y \
  ca-certificates curl git golang-go clang llvm libbpf-dev linux-libc-dev \
  openssl qrencode
if ! command -v docker >/dev/null 2>&1; then
  DEBIAN_FRONTEND=noninteractive apt-get install -y docker.io docker-compose-v2
fi
systemctl enable --now docker
if ! docker compose version >/dev/null 2>&1; then
  DEBIAN_FRONTEND=noninteractive apt-get install -y docker-compose-v2
fi

# A standalone nftables.service with "flush ruleset" can erase Docker's DOCKER-* chains.
# V3.4 uses eBPF and does not require nftables.service. Preserve in-memory rules, but
# disable the service when this known-conflicting configuration is detected.
if systemctl is-active --quiet nftables 2>/dev/null && grep -Eq '^[[:space:]]*flush[[:space:]]+ruleset' /etc/nftables.conf 2>/dev/null; then
  echo "[preflight] nftables.service uses 'flush ruleset'; disabling it to protect Docker networking."
  systemctl disable --now nftables || true
  systemctl restart docker
fi

# Verify Docker can create bridge networks before MySQL startup.
TEST_NET=netbird-audit-preflight-$$
if ! docker network create "$TEST_NET" >/dev/null 2>&1; then
  echo "Docker bridge network preflight failed. Check DOCKER-FORWARD/iptables before retrying."
  exit 1
fi
docker network rm "$TEST_NET" >/dev/null 2>&1 || true

# TCX requires a recent kernel. 6.6+ is the practical baseline.
KERNEL_MAJOR=$(uname -r | cut -d. -f1)
KERNEL_MINOR=$(uname -r | cut -d. -f2)
if [ "$KERNEL_MAJOR" -lt 6 ] || { [ "$KERNEL_MAJOR" -eq 6 ] && [ "$KERNEL_MINOR" -lt 6 ]; }; then
  echo "Kernel $(uname -r) is too old for the TCX sensor. Linux 6.6+ required."
  exit 1
fi

if [ -d "$BASE_DIR" ]; then
  BACKUP="${BASE_DIR}.backup.$(date +%Y%m%d-%H%M%S)"
  echo "Existing $BASE_DIR found; moving it to $BACKUP"
  systemctl disable --now netbird-audit-sensor netbird-audit-server 2>/dev/null || true
  mv "$BASE_DIR" "$BACKUP"
fi

if ! id -u netbird-audit >/dev/null 2>&1; then
  useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin netbird-audit
fi
mkdir -p "$SRC_DIR" "$BASE_DIR/bin" "$BASE_DIR/bpf" "$BASE_DIR/certs"
cp -a "$SCRIPT_DIR"/. "$SRC_DIR"/
cd "$SRC_DIR"

echo "[2/8] Resolving Go modules and building Go services..."
export GOTOOLCHAIN=auto
# Some fresh hosts have GOFLAGS=-mod=readonly inherited from the environment.
# This project is installed from a source bundle without a pre-generated go.sum,
# so explicitly allow Go to resolve modules and create/update go.sum locally.
unset GOFLAGS || true
export GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"

# `go mod tidy` writes the package checksums required by the compiler.
# `-mod=mod` below is intentional so a missing go.sum on a fresh install
# cannot turn into a misleading "missing go.sum entry" build error.
go mod tidy
go mod verify
go build -mod=mod -trimpath -ldflags='-s -w' -o "$BASE_DIR/bin/audit-server" ./cmd/audit-server
go build -mod=mod -trimpath -ldflags='-s -w' -o "$BASE_DIR/bin/audit-sensor" ./cmd/audit-sensor

echo "[3/8] Compiling eBPF TCX program..."
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) TARGET_ARCH=x86; MULTIARCH=x86_64-linux-gnu ;;
  aarch64|arm64) TARGET_ARCH=arm64; MULTIARCH=aarch64-linux-gnu ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac
clang -O2 -g -target bpf -D__TARGET_ARCH_${TARGET_ARCH} \
  -I/usr/include -I/usr/include/${MULTIARCH} \
  -c sensor/bpf/audit.bpf.c -o "$BASE_DIR/bpf/audit.bpf.o"

DB_ROOT_PASSWORD=$(openssl rand -hex 24)
DB_PASSWORD=$(openssl rand -hex 24)
SENSOR_KEY=$(openssl rand -hex 32)
TOTP_SECRET=$($BASE_DIR/bin/audit-server generate-totp)
ADMIN_HASH=$(printf '%s\n' "$ADMIN_PASSWORD" | $BASE_DIR/bin/audit-server hash-password)
unset ADMIN_PASSWORD

cat > "$BASE_DIR/.mysql.env" <<MYSQL
MYSQL_ROOT_PASSWORD=$DB_ROOT_PASSWORD
MYSQL_DATABASE=netbird_audit
MYSQL_USER=netbird_audit
MYSQL_PASSWORD=$DB_PASSWORD
MYSQL
chmod 600 "$BASE_DIR/.mysql.env"
chown root:root "$BASE_DIR/.mysql.env"
cp docker-compose.mysql.yml "$BASE_DIR/docker-compose.yml"

cat > "$BASE_DIR/config.env" <<ENV
NETBIRD_URL=$NETBIRD_URL
NETBIRD_PAT=$NETBIRD_PAT
OVERLAY_CIDR=$OVERLAY_CIDR
NETBIRD_INTERFACE=$NETBIRD_INTERFACE
MYSQL_DSN=netbird_audit:$DB_PASSWORD@tcp(127.0.0.1:3307)/netbird_audit?parseTime=true&charset=utf8mb4&loc=UTC
LISTEN_ADDR=$AUDIT_LISTEN
SENSOR_LISTEN_ADDR=127.0.0.1:9080
TLS_CERT=$BASE_DIR/certs/audit.crt
TLS_KEY=$BASE_DIR/certs/audit.key
ADMIN_USER=$ADMIN_USER
ADMIN_HASH=$ADMIN_HASH
TOTP_SECRET=$TOTP_SECRET
SENSOR_KEY=$SENSOR_KEY
SECURE_COOKIE=true
POLL_SECONDS=10
ENV
chmod 640 "$BASE_DIR/config.env"
chown root:netbird-audit "$BASE_DIR/config.env"

# Self-signed bootstrap certificate. Replace with a trusted certificate later.
echo "[4/8] Generating bootstrap TLS certificate..."
openssl req -x509 -nodes -newkey rsa:3072 -sha256 -days 825 \
  -keyout "$BASE_DIR/certs/audit.key" -out "$BASE_DIR/certs/audit.crt" \
  -subj "/CN=$AUDIT_CN" \
  -addext "subjectAltName=DNS:$AUDIT_CN"
chmod 640 "$BASE_DIR/certs/audit.key"
chmod 644 "$BASE_DIR/certs/audit.crt"
chown root:netbird-audit "$BASE_DIR/certs/audit.key" "$BASE_DIR/certs/audit.crt"
chown root:netbird-audit "$BASE_DIR/bin/audit-server" "$BASE_DIR/bin/audit-sensor" "$BASE_DIR/bpf/audit.bpf.o"
chmod 755 "$BASE_DIR/bin/audit-server" "$BASE_DIR/bin/audit-sensor"
chmod 644 "$BASE_DIR/bpf/audit.bpf.o"
# The application directory is traversable only by root and the service group.
chown root:netbird-audit "$BASE_DIR" "$BASE_DIR/certs" "$BASE_DIR/bin" "$BASE_DIR/bpf"
chmod 750 "$BASE_DIR" "$BASE_DIR/certs" "$BASE_DIR/bin" "$BASE_DIR/bpf"

echo "[5/8] Starting MySQL 8.4..."
cd "$BASE_DIR"
docker compose up -d
for i in $(seq 1 60); do
  if docker inspect --format='{{.State.Health.Status}}' netbird-audit-mysql 2>/dev/null | grep -q healthy; then break; fi
  sleep 2
  if [ "$i" -eq 60 ]; then echo "MySQL did not become healthy"; docker logs netbird-audit-mysql --tail=100; exit 1; fi
done

cp "$SRC_DIR/scripts/netbird-audit-server.service" /etc/systemd/system/netbird-audit-server.service
cp "$SRC_DIR/scripts/netbird-audit-sensor.service" /etc/systemd/system/netbird-audit-sensor.service
systemctl daemon-reload

echo "[6/8] Starting Go audit server..."
systemctl enable --now netbird-audit-server
sleep 3
if ! curl -ksf "https://127.0.0.1:${AUDIT_LISTEN##*:}/healthz" >/dev/null; then
  systemctl status netbird-audit-server --no-pager || true
  journalctl -u netbird-audit-server -n 100 --no-pager || true
  exit 1
fi

echo "[7/8] Starting Go + eBPF TCX sensor..."
systemctl enable --now netbird-audit-sensor
sleep 3
if ! systemctl is-active --quiet netbird-audit-sensor; then
  systemctl status netbird-audit-sensor --no-pager || true
  journalctl -u netbird-audit-sensor -n 100 --no-pager || true
  exit 1
fi

echo "[8/8] MFA enrollment"
OTP_URI=$($BASE_DIR/bin/audit-server otpauth "$TOTP_SECRET" "$ADMIN_USER" "NetBird-Audit")
echo
echo "Scan this QR code with your authenticator app:"
qrencode -t ANSIUTF8 "$OTP_URI"
echo
echo "Manual TOTP secret: $TOTP_SECRET"
echo
HOST_IP=$(hostname -I | awk '{print $1}')
PORT=${AUDIT_LISTEN##*:}
echo "============================================================"
echo " NetBird Audit V3.4 installed"
echo "============================================================"
echo "Audit UI: https://${HOST_IP}:${PORT}"
echo "Admin:    $ADMIN_USER"
echo "Backend:  Go (V3.4 UI)"
echo "Sensor:   Go + eBPF TCX"
echo "Database: MySQL 8.4"
echo
echo "SECURITY: Allow TCP/$PORT only from your administrator IP in AWS SG."
echo "Secrets:  $BASE_DIR/config.env (mode 600, root only)"
echo "Backup:   $BASE_DIR/docker-compose.yml + .mysql.env + MySQL volume + config.env"
echo
echo "Useful commands:"
echo "  systemctl status netbird-audit-server netbird-audit-sensor"
echo "  journalctl -u netbird-audit-sensor -f"
echo "  cd $BASE_DIR && docker compose ps"
