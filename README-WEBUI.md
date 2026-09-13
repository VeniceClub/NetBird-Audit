# NetBird Audit V3.4.1 Web UI-only patch

This patch only rebuilds and replaces `audit-server`, because the HTML/CSS/JS UI is embedded in the Go server binary.

It does **not** modify:
- MySQL or its Docker volume
- eBPF object or `audit-sensor`
- NetBird configuration/PAT
- TOTP secret or admin password
- TLS certificates
- VPN sessions, access logs, resources or history

## Fixes
- Fixes six TOTP boxes overflowing the login card.
- Uses `minmax(0,1fr)`, `min-width:0`, and strict card sizing.
- Improves 1440p/2K/4K and narrow-screen responsiveness.
- Keeps all V3.4 backend behavior unchanged.

## Upgrade

```bash
sudo -i
cd /data
# extract package, then:
cd netbird-audit-go-ebpf-v3.4.1-webui
./upgrade-webui-v3.4.1.sh
```

Then hard refresh the browser (`Cmd+Shift+R` / `Ctrl+Shift+R`).
