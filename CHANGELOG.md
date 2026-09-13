# Changelog

## 3.4.1-webui
- Web UI-only patch for responsive login/TOTP layout.
- Preserves Go backend, eBPF sensor, MySQL data, runtime secrets and flow/session logic.

## 3.4
- Renamed Flow-oriented UX to Access Logs.
- Added Access Log filters, CSV export and detail view.
- Added NetBird Network Resource synchronization and audit enablement per resource.
- Added Peers and System Health views.
- Added sensor heartbeat and NetBird/resource sync health state.
- Preserved sidecar architecture and Active/Ended flow semantics.

## 3.3.x
- Major dark-console UI redesign.
- Fixed sidebar routing/view behavior.

## 3.2
- Fixed Go compile issues and deployment iteration bugs.

## 3.1
- Improved Go module resolution/build flow.

## 3.0
- Go-native Audit Server and sensor.
- MySQL 8.4.
- Go + cilium/ebpf TCX flow sensor.
