# Troubleshooting

## Access Logs empty

1. Confirm real traffic crosses the routing peer:
   ```bash
   sudo tcpdump -ni wt0 host <destination-ip>
   ```
2. Confirm sensor is active and attached:
   ```bash
   systemctl status netbird-audit-sensor --no-pager
   sudo bpftool net
   ```
3. Inspect the BPF flow map:
   ```bash
   sudo bpftool map show
   sudo bpftool map dump id <flows-map-id>
   ```
4. Check recent sensor flush errors:
   ```bash
   journalctl -u netbird-audit-sensor --since '5 minutes ago' --no-pager
   ```
5. Confirm internal API listener:
   ```bash
   ss -lntp | grep ':9080'
   curl -i http://127.0.0.1:9080/healthz
   ```
6. Check `netbird_resources_cache`, `resources`, and `network_flows` in MySQL.

## Sensor says no interface address found

The overlay CIDR is stale or the local NetBird client is not registered. Run `netbird status` and `ip -br addr`; update `OVERLAY_CIDR` and `NETBIRD_INTERFACE`.

## Docker cannot create bridge network / DOCKER-FORWARD missing

Check whether `nftables.service` is active and loading a ruleset that flushes Docker chains. Audit V3 does not need the system nftables service. Restart Docker after correcting the conflict. Do not blindly flush firewall rules on a production host.

## Sensor heartbeat connection refused at startup

If it occurs once while server and sensor are restarted together, verify current state first:
```bash
ss -lntp | grep -E ':(9080|9443)\b'
```
A transient first heartbeat failure is not evidence of a persistent API failure.
