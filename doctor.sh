#!/usr/bin/env bash
set -u
BASE=/opt/netbird-audit
echo "== Kernel =="
uname -a
echo
echo "== NetBird interface candidates =="
ip -br -4 addr | grep -E '100\.|wt|netbird' || true
echo
echo "== Audit services =="
systemctl --no-pager --full status netbird-audit-server netbird-audit-sensor 2>/dev/null || true
echo
echo "== MySQL =="
(cd "$BASE" && docker compose ps) 2>/dev/null || true
echo
echo "== Sensor logs =="
journalctl -u netbird-audit-sensor -n 30 --no-pager 2>/dev/null || true
echo
echo "== Server logs =="
journalctl -u netbird-audit-server -n 30 --no-pager 2>/dev/null || true
