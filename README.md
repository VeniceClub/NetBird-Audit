# NetBird Audit Gateway

> Git-ready development snapshot: **V3.4 + V3.4.1 Web UI patch**. See [`docs/HANDOFF.md`](docs/HANDOFF.md) before continuing development. The current unresolved investigation is an empty Access Logs pipeline despite confirmed routed traffic on `wt0`; debug the eBPF map/sensor/resource-match path before changing NetBird routing.

## Developer quick start

```bash
make check
go mod tidy
go test ./...
```

Useful project documentation:

- [`docs/HANDOFF.md`](docs/HANDOFF.md) — current runtime state, open issue, next steps
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — component/data-flow boundaries
- [`docs/TROUBLESHOOTING.md`](docs/TROUBLESHOOTING.md) — production debugging order
- [`docs/NGINX_GATEWAY.md`](docs/NGINX_GATEWAY.md) — optional future HTTP audit gateway
- [`skills/netbird-audit-development/SKILL.md`](skills/netbird-audit-development/SKILL.md) — reusable ChatGPT development skill

---

Go-native audit sidecar for a self-hosted NetBird routing peer.

## Architecture

- Go HTTPS Audit UI/API
- Go NetBird API collector
- Go + eBPF TCX flow sensor
- MySQL 8.4
- Password + TOTP MFA
- Independent from the NetBird database and NetBird server binaries

V3.4 does **not** modify NetBird core. NetBird remains the source of truth for Users, Peers, Networks and Network Resources.

## V3.4 highlights

- New Access Logs page with time/user/resource/protocol/search filters
- CSV export for recent access logs
- Click a flow row to open an Access Details drawer
- NetBird Network Resources auto-sync from `/api/networks` and `/api/networks/{id}/resources`
- Enable/disable auditing per NetBird IP/CIDR resource without changing NetBird routing/policies
- New Peers page with user/device/NetBird IP/public IP/last seen
- New System Health page
- eBPF sensor heartbeat and interface status
- Existing Sessions, Flows, Resources, PAT, TOTP, admin password hash, TLS certificate and MySQL volume are preserved during upgrade

> Network flow status is shown as Active/Ended. V3.4 does not claim a packet was “Allowed” or “Blocked” by a NetBird policy because the eBPF sidecar observes routed traffic; policy-decision telemetry is a separate concern.

## One-click upgrade from V3.3 / V3.3.1

```bash
sudo -i
cd /data
tar -xzf netbird-audit-go-ebpf-v3.4.tar.gz
cd netbird-audit-go-ebpf-v3.4
./upgrade-v3.4.sh
```

The upgrade script builds everything in `/tmp` first, then backs up the current binaries/source, preserves `/opt/netbird-audit/config.env`, certificates, Docker/MySQL configuration and the MySQL named volume, installs V3.4, runs DB migrations automatically and performs health checks.

After upgrade, hard-refresh the browser:

```text
macOS Chrome/Safari: Cmd + Shift + R
Windows/Linux:       Ctrl + Shift + R
```

## Fresh install

For a fresh host with an already-working NetBird self-hosted server and PAT:

```bash
sudo ./setup-audit-v3.sh
```

The installer auto-detects the NetBird interface/CIDR when possible.

## Resource model

NetBird owns connectivity and access policy. Audit V3.4 only owns observation.

Example:

```text
NetBird Network: HongKong-Internal
  JumpServer  10.0.0.33/32
  GitLab      10.0.0.50/32
  ArgoCD      10.0.0.60/32

Audit Resources page
  Sync Now
  JumpServer  -> Enable audit tcp 80,443
  GitLab      -> Enable audit tcp 443
  ArgoCD      -> Enable audit tcp 443
```

Domain-only resources are displayed but cannot be matched by the current IP-level eBPF flow sensor unless they resolve to an audited IP/CIDR resource.

## Security

The console includes HTTPS, bcrypt password verification, TOTP MFA, HttpOnly/Secure/SameSite cookies, CSRF checking, HSTS/CSP and login rate limiting. Keep TCP/9443 restricted to administrator IPs at the AWS Security Group whenever possible.

## Operations

```bash
systemctl status netbird-audit-server netbird-audit-sensor --no-pager
journalctl -u netbird-audit-server -n 100 --no-pager
journalctl -u netbird-audit-sensor -n 100 --no-pager
cd /opt/netbird-audit && docker compose ps
```

## Rollback material

Every V3.4 upgrade creates a directory like:

```text
/opt/netbird-audit.upgrade-backup.YYYYMMDD-HHMMSS
```

It contains the previous binaries/source/config/certs. The MySQL named volume is not removed or recreated.
