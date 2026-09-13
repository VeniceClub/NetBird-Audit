# Security

## Reporting

Treat this repository as security-sensitive infrastructure. Report suspected authentication bypass, secret exposure, unsafe upgrade behavior, or traffic-interception flaws privately to the repository maintainers rather than publishing working exploitation details first.

## Deployment baseline

- Restrict Audit HTTPS ingress to administrator networks/IPs where possible.
- Use trusted TLS in production.
- Rotate any credential accidentally printed or pasted into logs/chat.
- Keep MySQL loopback/private and never expose it publicly.
- Keep `127.0.0.1:9080` loopback-only.
- Use a dedicated NetBird service identity/PAT and rotate it periodically.
- Back up MySQL before schema/upgrade work.
