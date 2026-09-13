#!/usr/bin/env bash
set -euo pipefail
if [ "$(id -u)" -ne 0 ]; then echo "Run as root"; exit 1; fi
systemctl disable --now netbird-audit-sensor netbird-audit-server 2>/dev/null || true
rm -f /etc/systemd/system/netbird-audit-sensor.service /etc/systemd/system/netbird-audit-server.service
systemctl daemon-reload
if [ -d /opt/netbird-audit ]; then
  cd /opt/netbird-audit
  docker compose down 2>/dev/null || true
fi
echo "Audit services removed. MySQL volume netbird-audit-mysql-data was intentionally preserved."
echo "To permanently delete audit DB data: docker volume rm netbird-audit-mysql-data"
