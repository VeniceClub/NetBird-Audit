# Development handoff

## Snapshot

This source snapshot is based on **V3.4 + V3.4.1 Web UI patch**. The root `VERSION` value is `3.4.1-webui`.

The project exists to answer two questions cleanly without modifying NetBird core:
1. **Who is connected?** — VPN Sessions from NetBird user/peer state.
2. **Who accessed which routed internal resource?** — L3/L4 Access Logs from eBPF on the routing peer, enriched with NetBird identity and resource metadata.

## Current deployment shape

Example deployment used during development:
- Self-hosted NetBird management URL: `https://vpn.eks-tools.com`
- Audit console: `https://audit.eks-tools.com:9443`
- Routing peer host example: `10.0.0.210`
- NetBird interface observed: `wt0`
- Overlay observed after reinstall: `100.126.0.0/16`
- Public Audit HTTPS listener: `:9443`
- Internal sensor API: `127.0.0.1:9080`
- MySQL: 8.4, managed separately from NetBird

No credentials are included in this repository.

## Verified runtime facts

- NetBird client re-registration succeeded after the NetBird management server was reinstalled.
- eBPF TCX sensor successfully attached to `wt0`.
- Audit server was observed listening on both `:9443` and `127.0.0.1:9080`.
- `tcpdump` on `wt0` confirmed real routed client traffic, e.g. an overlay client reaching an internal HTTP service.
- Docker bridge creation can fail if system `nftables.service` flushes Docker chains; V3 eBPF does not require that service.

## Open issue at handoff

**Access Logs were still empty even though tcpdump confirmed traffic traversing `wt0`.**

Do not start by changing NetBird routing or the Web UI. Debug in this order:

1. Install/use `bpftool` matching the running AWS kernel.
2. Confirm TCX attachments with `bpftool net`.
3. Find and dump the eBPF `flows` map immediately after generating a test connection.
4. If the map is empty, debug `sensor/bpf/audit.bpf.c` and TCX direction/keying.
5. If the map has entries, debug `internal/sensor/sensor.go` flush behavior and API responses.
6. Verify the destination exists in `netbird_resources_cache` and is explicitly audit-enabled in `resources`.
7. Verify rows reach `network_flows` before changing Access Logs UI.

The internal API health endpoint is `/healthz`, not `/health`.

## Product direction discussed

For deeper HTTP/HTTPS auditing, add an **internal Nginx audit gateway** as an optional integration:

`NetBird -> Internal Nginx -> JumpServer / GitLab / ArgoCD / Jenkins`

This would allow correlation of NetBird identity + eBPF flow + HTTP method/host/path/status/upstream. Keep eBPF for non-HTTP protocols and network-layer evidence. Do not require Nginx for basic flow auditing.

## Next sensible milestones

1. Resolve the empty-flow pipeline with `bpftool` evidence.
2. Add debug metrics/counters: map entries read, flows posted, matches stored, resource-match misses.
3. Improve NetBird Resource sync UX and make audit enablement obvious.
4. Add retention/aggregation strategy for `network_flows`.
5. Add optional Nginx structured-log ingestion and correlation.
6. Move bootstrap self-signed TLS behind Traefik/ACME for a trusted `audit` hostname.
