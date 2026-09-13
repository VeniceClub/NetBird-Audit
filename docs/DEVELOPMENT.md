# Development

## Prerequisites

- Go 1.22+
- clang/LLVM
- libbpf headers
- Linux kernel with TCX support for runtime sensor testing
- Docker for local MySQL 8.4

## Common commands

```bash
make fmt
make check
go mod tidy
go mod verify
go test ./...
go vet ./...
make build
make build-bpf
```

The eBPF sensor cannot be meaningfully validated on a non-Linux development machine. Use a disposable Linux/AWS test host for load/attach tests.

## Local secret policy

Create runtime config only on the deployment host. Do not commit:
- NetBird PAT
- admin bcrypt/TOTP values
- sensor key
- MySQL passwords or DSNs containing passwords
- TLS private keys

Do not run secret-bearing installers with `bash -x`.

## API compatibility

Keep NetBird calls isolated in `internal/netbird/client.go`. If a NetBird upgrade changes response fields or paths, patch the adapter rather than spreading version checks throughout the project.
