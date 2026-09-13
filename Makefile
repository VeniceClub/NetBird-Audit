.PHONY: fmt check test build build-bpf doctor

fmt:
	gofmt -w cmd internal

check:
	bash skills/netbird-audit-development/scripts/repo_check.sh .

test:
	go test ./...

build:
	mkdir -p bin
	go build -o bin/audit-server ./cmd/audit-server
	go build -o bin/audit-sensor ./cmd/audit-sensor

build-bpf:
	mkdir -p bin
	clang -O2 -g -target bpf -D__TARGET_ARCH_x86 -I/usr/include/$(shell uname -m)-linux-gnu -c sensor/bpf/audit.bpf.c -o bin/audit.bpf.o

doctor:
	./doctor.sh
