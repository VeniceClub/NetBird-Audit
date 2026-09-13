# Coding-agent instructions

This repository is a sidecar audit system for self-hosted NetBird. Do not modify NetBird core or its database unless a task explicitly changes that architecture.

Before editing:
1. Read `docs/HANDOFF.md` and `docs/ARCHITECTURE.md`.
2. Read `skills/netbird-audit-development/SKILL.md`.
3. Preserve the fail-open audit design: Audit failures must not block VPN traffic.

Security rules:
- Never commit PATs, TOTP secrets, sensor keys, DB passwords, private keys, runtime `config.env`, `.mysql.env`, dumps, or logs.
- Do not recommend `bash -x` for installers that handle secrets.
- Keep the sensor API loopback-only and authenticated.
- Keep public login protected by HTTPS + password + TOTP + CSRF + secure cookies + rate limiting.

Testing:
- Run `make check` after changes.
- Run `go test ./...` and `go vet ./...` when modules are available.
- eBPF changes require a real Linux load/attach test; compilation alone is not sufficient.
- Upgrade changes must preserve MySQL data and runtime secrets.
