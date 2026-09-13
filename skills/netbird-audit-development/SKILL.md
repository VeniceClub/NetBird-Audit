---
name: netbird-audit-development
description: "Develop, debug, review, package, or upgrade the NetBird Audit Gateway codebase: a Go-native sidecar for self-hosted NetBird using NetBird APIs, MySQL, and a Go+cilium/ebpf TCX flow sensor. Use for work on Audit Server/UI, VPN Sessions, Access Logs, NetBird resource sync, eBPF flow capture, installers/upgraders, production troubleshooting, release packaging, or compatibility with NetBird upgrades. Preserve the sidecar architecture and never require NetBird core/database modifications unless explicitly requested."
---

# NetBird Audit Development

Use this skill as the project runbook for the NetBird Audit Gateway repository.

## Start every task

1. Read `references/project-map.md` for ownership boundaries and source locations.
2. Read `references/current-state.md` when debugging or changing behavior.
3. Read `references/architecture.md` before changing eBPF, NetBird sync, MySQL schema, authentication, or deployment.
4. Read `references/release-process.md` before producing an upgrade/release archive.
5. Keep secrets out of source, logs, generated examples, and chat-visible command traces.

## Architectural invariants

- Keep NetBird unmodified. Treat NetBird APIs and the routing-peer interface as integration boundaries.
- Keep Audit MySQL independent from the NetBird database.
- Keep the audit path fail-open with respect to VPN connectivity: an Audit failure must not block NetBird traffic.
- Use Go for server, NetBird collector, and sensor. Use `cilium/ebpf` + TCX for flow capture.
- Do not use nftables as the V3 sensor. Do not re-enable `nftables.service` on a Docker host merely for Audit.
- Keep the sensor API loopback-only (`127.0.0.1:9080`) and authenticated with `X-Sensor-Key`.
- Keep the public console behind HTTPS, password + TOTP, CSRF protection, secure cookies, rate limiting, and restrictive security headers.
- Snapshot user/device/resource identity into historical rows so later NetBird deletions do not erase audit meaning.

## Development workflow

1. Reproduce the issue with the smallest observable layer.
2. Identify the boundary: NetBird API, routing path, eBPF map, Go sensor flush, loopback sensor API, resource matching, MySQL, or Web UI.
3. Patch only the failing layer when possible.
4. Run `scripts/repo_check.sh <repo-path>` after changes.
5. For eBPF changes, also compile `sensor/bpf/audit.bpf.c` with the same clang target used by the installer and test on a compatible Linux host before claiming production readiness.
6. Preserve existing config, certs, secrets, MySQL volume, Sessions, Flows, and Resource settings during upgrades.

## Debugging decision tree

- **VPN Session missing:** verify `/api/peers` and `/api/users`, then `users_cache`, `peers_cache`, and `vpn_sessions`.
- **Access Log missing:** verify traffic crosses the routing peer (`tcpdump -ni wt0 ...`), then eBPF attachment/map, sensor flush, `127.0.0.1:9080`, audited resource matching, and finally `network_flows`.
- **Resource missing:** verify NetBird `/api/networks` and `/api/networks/{id}/resources`; then `netbird_resources_cache`; then whether the resource is Audit-enabled in `resources`.
- **Sensor starts but no data:** inspect `bpftool net`, `bpftool map show`, and the `flows` map before modifying the UI or database.
- **Web UI issue only:** do not restart/rebuild MySQL or replace the BPF object unless required.
- **Docker network failure with missing `DOCKER-FORWARD`:** inspect system `nftables.service`; Audit V3 does not require it. Never flush host firewall rules blindly.

## Product semantics

- Call observed network records **Access Logs** in the UI, not policy-decision logs.
- Show flow state as `Active` / `Ended`; do not claim `Allowed` / `Blocked` unless policy-decision telemetry is actually collected.
- NetBird remains the source of truth for Users, Peers, Networks, Routing Peers, and Network Resources.
- Audit owns observation, history, audit enablement, filters, retention, and presentation.
- For HTTP/HTTPS deep audit, prefer a future internal reverse-proxy integration; eBPF remains the network-layer source of truth. See `references/architecture.md`.

## Release rules

- Use semantic versions and update `VERSION`, README, changelog, and upgrade script together.
- Build in a temporary directory first; replace running binaries only after successful compilation.
- Back up the current binaries/source/config before replacement.
- Never delete/recreate the MySQL named volume during an upgrade.
- Add rollback instructions to every release.
- Do not include `config.env`, `.mysql.env`, PATs, TOTP secrets, sensor keys, certificates/private keys, database dumps, or runtime logs in Git/release ZIPs.
