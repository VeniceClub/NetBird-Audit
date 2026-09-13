# Project map

| Area | Path | Responsibility |
|---|---|---|
| Server entrypoint | `cmd/audit-server/main.go` | Config loading, server startup, utility commands |
| Sensor entrypoint | `cmd/audit-sensor/main.go` | Sensor flags/config and lifecycle |
| HTTP/UI | `internal/server/` | HTTPS console, login/MFA, routes, sensor API, NetBird polling, UI templates/JS |
| MySQL | `internal/db/db.go` | Schema migrations, caches, Sessions, Resources, Flows, queries |
| NetBird adapter | `internal/netbird/client.go` | PAT-authenticated NetBird API calls and model mapping |
| Sensor userspace | `internal/sensor/sensor.go` | Load BPF object, attach TCX, read flow map, heartbeat/flush |
| eBPF | `sensor/bpf/audit.bpf.c` | L3/L4 flow aggregation in kernel map |
| systemd | `scripts/*.service` | Audit server/sensor service definitions |
| MySQL runtime | `docker-compose.mysql.yml` | MySQL 8.4 only; loopback binding expected |
| Install | `setup-audit-v3.sh` | Fresh install, build, config/secrets, services |
| Upgrade | `upgrade-v3.4.sh` or later | Preserve runtime state, build, replace, health-check, rollback material |
| Diagnostics | `doctor.sh` | Operational checks |

Do not make the Web UI or MySQL a dependency for NetBird packet forwarding.
