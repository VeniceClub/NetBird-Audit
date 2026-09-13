# Release process

1. Update source and `VERSION`.
2. Run `gofmt` and shell syntax checks.
3. Run `go mod tidy`, `go mod verify`, `go test ./...`, and `go vet ./...` where network/module access is available.
4. Compile the eBPF C object with clang and verify it loads on a supported Linux host.
5. Run secret scan patterns against the repository.
6. Test fresh install on a disposable host.
7. Test in-place upgrade from the previous supported release with a populated MySQL volume.
8. Verify rollback restores prior binaries without touching MySQL data.
9. Update `CHANGELOG.md`, docs, upgrade notes, and checksums.
10. Package source without runtime secrets, DB dumps, cert private keys, or logs.
