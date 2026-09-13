# Contributing

1. Keep changes scoped to the Audit sidecar unless an architectural change is explicitly approved.
2. Do not commit runtime secrets or deployment data.
3. Run `make check`, `go test ./...`, and `go vet ./...` where possible.
4. eBPF changes require a real Linux load/attach test.
5. Upgrade changes must preserve existing MySQL data and config.
6. Update `CHANGELOG.md` and relevant docs for user-visible behavior changes.
