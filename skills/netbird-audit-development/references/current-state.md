# Current development state

The repository snapshot bundled with this skill is based on V3.4 with the V3.4.1 Web UI patch.

Known-good behavior from the current deployment:
- NetBird client is connected on a routing peer.
- Example overlay: `100.126.0.0/16` with interface `wt0`.
- Go eBPF sensor reports successful TCX attachment to `wt0`.
- Audit server exposes public HTTPS on `:9443` and loopback sensor API on `127.0.0.1:9080`.
- `tcpdump` confirmed routed client traffic crossing `wt0` to an internal resource.

Open investigation at snapshot time:
- Access Logs were empty even though traffic was observed on `wt0`.
- The next debugging step is to inspect the eBPF `flows` map, then sensor flush/resource matching, before changing routing or UI.
- NetBird Resource sync/audit-enable state must be verified in `netbird_resources_cache` and `resources`.

Important prior lessons:
- A first heartbeat can fail with `connection refused` during simultaneous server/sensor restart; confirm current listeners before treating it as a persistent API failure.
- `/healthz` is the implemented sensor/public health route; `/health` may return 404.
- System `nftables.service` previously removed Docker `DOCKER-FORWARD` chains. V3 uses eBPF and does not need that service.
- `config.env` contains a DSN with shell-special characters and should not be blindly `source`d.
