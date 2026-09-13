# Architecture

## High-level

```text
NetBird Management API
   | users / peers / networks / resources
   v
Go Audit Server --------------------> MySQL 8.4
   ^                                     |
   | loopback sensor API                 | UI queries
   | 127.0.0.1:9080                      v
Go Sensor <--- eBPF TCX ---> wt0      HTTPS UI :9443
                           |
                           v
                    routed resources
```

The system is deliberately a **sidecar**. NetBird remains responsible for identity, overlay connectivity, routing, policies, and resource definitions. Audit is responsible for observation and history.

## eBPF flow path

The BPF object attaches one program to TCX ingress and one to TCX egress on the NetBird interface. It aggregates IPv4 TCP/UDP flows in a `BPF_MAP_TYPE_LRU_HASH` map called `flows`.

Userspace periodically:
1. Iterates `flows`.
2. Keeps only flows whose source address is inside the configured NetBird overlay prefix.
3. Converts monotonic timestamps to wall-clock timestamps.
4. Posts snapshots to `/api/v1/flows` with `X-Sensor-Key`.
5. Deletes ended/idle flows only after a successful API POST.

## Identity enrichment

The server polls NetBird with PAT authentication (`Authorization: Token ...`) and maps:
- source NetBird IP -> Peer
- Peer `user_id` -> User
- destination IP/CIDR -> audit-enabled Resource

Historical rows store snapshots of user, peer, and resource names.

## Failure isolation

- The eBPF programs return `TC_ACT_OK`; they do not enforce policy.
- Sensor/server/MySQL failure must not break NetBird forwarding.
- Resource audit enablement controls storage/visibility, not connectivity.

## Future HTTP gateway

An internal reverse proxy can provide application-layer observability:

```text
employee -> NetBird -> routing peer -> internal Nginx -> application
                         |                 |
                       eBPF             JSON log
                         \                 /
                          Audit correlation
```

Use this only as an additional source. Avoid mislabeling eBPF observation as a policy decision.
