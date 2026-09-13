#!/usr/bin/env bash
set -euo pipefail
repo=${1:-.}
cd "$repo"

printf '[1/5] shell syntax\n'
for f in *.sh scripts/*.sh; do
  [ -e "$f" ] || continue
  bash -n "$f"
done

printf '[2/5] gofmt\n'
unformatted=$(gofmt -l cmd internal 2>/dev/null || true)
if [ -n "$unformatted" ]; then
  printf 'Unformatted Go files:\n%s\n' "$unformatted" >&2
  exit 1
fi

printf '[3/5] go.mod\n'
if command -v go >/dev/null 2>&1; then
  go mod verify || true
fi

printf '[4/5] obvious secret scan\n'
if grep -RInE --exclude-dir=.git --exclude='*.md' --exclude='repo_check.sh' '(nbp_[A-Za-z0-9_-]{16,}|TOTP_SECRET=[A-Z2-7]{16,}|SENSOR_KEY=[0-9a-f]{32,})' .; then
  echo 'Potential secret found. Review before commit.' >&2
  exit 1
fi

printf '[5/5] required files\n'
for f in go.mod VERSION README.md sensor/bpf/audit.bpf.c internal/server/server.go internal/db/db.go; do
  test -f "$f" || { echo "Missing $f" >&2; exit 1; }
done

echo 'Repository checks passed.'
