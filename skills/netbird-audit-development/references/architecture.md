# Architecture

## Data plane

`NetBird client -> routing peer (wt0) -> internal resource`

The Audit sensor attaches TCX ingress/egress programs to the routing-peer NetBird interface and aggregates IPv4 TCP/UDP flows in an LRU hash map. Userspace periodically reads the map and POSTs flow snapshots to the loopback Audit API.

## Identity plane

The Audit server polls NetBird APIs using a PAT:
- `/api/users`
- `/api/peers`
- `/api/networks`
- `/api/networks/{networkID}/resources`

It caches identity and resource metadata in MySQL and reconciles VPN Sessions from Peer connection state.

## Storage

MySQL tables include:
- `users_cache`
- `peers_cache`
- `vpn_sessions`
- `netbird_resources_cache`
- `resources`
- `network_flows`
- `admin_audit`

Historical tables keep snapshot fields for user/device/resource names.

## UI/API

- Public HTTPS console: default `0.0.0.0:9443`.
- Internal sensor API: default `127.0.0.1:9080`.
- Public security: bcrypt password, TOTP, Secure/HttpOnly/SameSite cookies, CSRF, rate limiting, HSTS/CSP.
- Sensor authentication: `X-Sensor-Key`.

## Reverse-proxy future path

For HTTP/HTTPS applications, an internal Nginx gateway can add URL/method/status/upstream visibility while eBPF preserves network-layer evidence. A target architecture is:

`NetBird -> Internal Nginx -> JumpServer/GitLab/ArgoCD/Jenkins`

Audit can correlate:
- NetBird identity/session
- eBPF L3/L4 flow
- Nginx HTTP access event
- application-native audit logs

Prevent bypass if the reverse proxy is intended as the mandatory HTTP audit point: internal app security groups/firewalls should accept traffic from the gateway rather than directly from employee overlay addresses.
