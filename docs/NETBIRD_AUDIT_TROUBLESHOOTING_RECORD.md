# NetBird Audit 排错记录

> 项目：NetBird Audit Gateway V3.x\
> 环境：AWS Hong Kong / Ubuntu / NetBird Self-hosted / Go / eBPF TCX /
> MySQL 8.4\
> 审计节点：`10.0.0.210`\
> NetBird 接口：`wt0`\
> 当前 NetBird Overlay：`100.126.0.0/16`\
> 本文用于后续开发、升级和故障排查，**不包含 PAT、数据库密码、TOTP
> Secret 等敏感信息**。

## 1. Docker 网络创建失败：DOCKER-FORWARD 不存在

### 现象

安装 Audit MySQL 时 Docker 创建 network 失败：

``` text
Failed to Setup IP tables
Unable to enable ACCEPT OUTGOING rule
iptables --wait -t filter -A DOCKER-FORWARD ...
iptables: No chain/target/match by that name
```

检查：

``` bash
iptables -S DOCKER-FORWARD
iptables -S | grep DOCKER
```

没有任何 Docker chain。

### 根因

服务器上的 `nftables.service` 处于：

``` text
enabled
active
```

旧 Audit 方案曾使用 nftables。其规则加载/刷新导致 Docker 管理的
iptables/nftables chains 消失。

### 修复

``` bash
sudo systemctl disable --now nftables
sudo systemctl restart docker
```

验证：

``` bash
iptables -S | grep DOCKER

docker network create audit-test
docker network rm audit-test
```

恢复后可以看到：

``` text
-N DOCKER
-N DOCKER-BRIDGE
-N DOCKER-CT
-N DOCKER-FORWARD
-N DOCKER-INTERNAL
-N DOCKER-USER
```

### 注意

重启 Docker 会短暂重启同一 Docker daemon 上的 NetBird 容器，因此 VPN
可能短暂中断。

Audit V3 eBPF Sensor 本身不依赖 nftables。

------------------------------------------------------------------------

## 2. eBPF Sensor 报错：找不到 Overlay Interface

### 现象

``` text
no interface address found in 100.93.0.0/16
```

Sensor 不断被 systemd 自动重启。

### 排查

`netbird status` 后发现重新部署后的地址已经变化：

``` text
NetBird IP: 100.126.26.97/16
```

接口：

``` text
wt0  100.126.26.97/16
```

而旧配置仍使用：

``` text
100.93.0.0/16
```

### 修复

Audit 配置调整为：

``` text
OVERLAY_CIDR=100.126.0.0/16
NETBIRD_INTERFACE=wt0
```

重启：

``` bash
sudo systemctl restart netbird-audit-sensor
```

成功日志：

``` text
eBPF TCX sensor attached to wt0 (ifindex=39), overlay=100.126.0.0/16
```

### 结论

不要硬编码旧 NetBird Overlay。NetBird 重新部署后必须重新确认：

``` bash
netbird status
ip -br addr
```

------------------------------------------------------------------------

## 3. Audit Server 9080 Connection Refused

### 现象

Sensor 启动时出现：

``` text
sensor heartbeat:
Post "http://127.0.0.1:9080/api/v1/sensor-heartbeat":
dial tcp 127.0.0.1:9080: connect: connection refused
```

### 后续判断

该错误发生在 Audit Server / Sensor 同时重启的瞬间。Sensor
启动速度更快，第一次 heartbeat 时 Server 尚未完成监听。

之后检查：

``` bash
ss -lntp | grep -E ':(9080|9443)\b'
```

确认：

``` text
127.0.0.1:9080  LISTEN
0.0.0.0:9443    LISTEN
```

因此不是持续性 9080 故障。

### `/health` 返回 404

执行：

``` bash
curl http://127.0.0.1:9080/health
```

如果得到 HTTP 404，只说明当前版本没有定义 `/health` 路由，并不表示 9080
不工作。

### 建议改进

后续版本应：

-   增加 `/health` 或 `/readyz`
-   Sensor heartbeat 首次失败使用退避重试
-   systemd 可增加启动依赖/健康检查，减少启动竞态造成的误报

------------------------------------------------------------------------

## 4. Access Logs 为空

### 当前已确认事实

Audit Web UI 中：

``` text
Access Logs = empty
Flows = 0
```

但 NetBird 路由流量已经确认真实经过 `wt0`。

服务器执行：

``` bash
sudo tcpdump -ni wt0 host 10.0.0.33
```

员工 Mac 访问 JumpServer 后观察到：

``` text
100.126.67.176:51696 -> 10.0.0.33:80
10.0.0.33:80 -> 100.126.67.176:51696
```

并存在完整 HTTP 流量：

``` text
GET /api/v1/authentication/user-session/
HTTP/1.1 200 OK
```

这证明：

``` text
Employee Mac
    ↓
NetBird
    ↓
Routing Peer 10.0.0.210
    ↓
wt0
    ↓
10.0.0.33
```

这一段工作正常。

### 因此当前故障范围已经缩小

``` text
wt0 traffic                    ✅ confirmed
        ↓
TCX attachment                 ✅ confirmed
        ↓
eBPF flows map                 ? pending
        ↓
Go Sensor map reader / flush   ? pending
        ↓
127.0.0.1:9080                 ✅ listening
        ↓
Resource matching              ? pending
        ↓
MySQL network_flows            ? pending
        ↓
Access Logs UI                 ? pending
```

后续不要重新排查 NetBird Routing，优先从 eBPF map 开始。

------------------------------------------------------------------------

## 5. bpftool 与 AWS Kernel 不匹配

### 现象

``` bash
sudo bpftool map show
```

返回：

``` text
WARNING: bpftool not found for kernel 6.14.0-1018

You may need to install:
linux-tools-6.14.0-1018-aws
linux-cloud-tools-6.14.0-1018-aws
```

### 修复

``` bash
sudo apt-get update

sudo apt-get install -y \
  linux-tools-6.14.0-1018-aws \
  linux-cloud-tools-6.14.0-1018-aws

sudo apt-get install -y \
  linux-tools-aws \
  linux-cloud-tools-aws
```

验证：

``` bash
bpftool version
sudo bpftool prog show
sudo bpftool map show
sudo bpftool net
```

### 下一步

找到：

``` text
type lru_hash
name flows
```

然后在 Mac 访问内部资源后：

``` bash
sudo bpftool map dump id <MAP_ID>
```

判断：

-   map 有 Flow → 排查 Go Sensor flush / Resource matching / MySQL
-   map 无 Flow → 排查 eBPF TCX program / packet parsing / map update

------------------------------------------------------------------------

## 6. NetBird Resource 与 Audit Resource 不应混淆

NetBird 后台中的：

``` text
Networks
Resources
Routing Peers
Policies
```

负责**网络访问控制和路由**。

例如：

``` text
Network: Jenkins
Resource: jenkins-new
Address: 10.0.10.189/32
Routing Peer: 1
Policy: 1 Active
```

这不等同于 Audit 系统已经开始记录该 Resource。

Audit 自己需要知道哪些目标应该写入审计数据库。

后续版本需要把这个 UX 做清楚：

``` text
NetBird Resource
      ↓ sync
Audit Resource Cache
      ↓ enable/configure
Audited Resource
      ↓
network_flows
```

避免用户误以为 NetBird 中创建 Resource 就自动拥有应用审计日志。

------------------------------------------------------------------------

## 7. TOTP 二维码脚本不能直接 source config.env

### 现象

执行：

``` bash
source /opt/netbird-audit/config.env
```

报：

``` text
syntax error near unexpected token `('
```

原因是：

``` text
MYSQL_DSN=user:password@tcp(127.0.0.1:3307)/...
```

不是安全的 shell assignment 格式；`(`、`)`、`&` 等字符会被 shell 解析。

### 原则

不要直接：

``` bash
source /opt/netbird-audit/config.env
```

应该按 key 精确读取，例如：

``` bash
TOTP_SECRET="$(grep '^TOTP_SECRET=' /opt/netbird-audit/config.env | cut -d= -f2-)"
```

同理：

``` bash
MYSQL_DSN="$(grep '^MYSQL_DSN=' /opt/netbird-audit/config.env | cut -d= -f2-)"
```

### 安全要求

排错时禁止输出整个：

``` text
/opt/netbird-audit/config.env
```

避免泄露：

-   NetBird PAT
-   MySQL password
-   Admin credentials
-   TOTP Secret
-   Sensor key

------------------------------------------------------------------------

## 8. V3.2 Go 编译问题

### go.sum / module 问题

旧安装脚本仅执行：

``` bash
go mod download
```

服务器出现 missing go.sum entries。

后续改为：

``` bash
unset GOFLAGS
export GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"

go mod tidy
go mod verify
go build -mod=mod ...
```

服务器最终：

``` text
all modules verified
```

### `net.ParseCIDR` 编译错误

错误：

``` text
assignment mismatch: 2 variables but net.ParseCIDR returns 3 values
```

错误代码：

``` go
if _, err := net.ParseCIDR(cidr); err != nil {
```

修复：

``` go
if _, _, err := net.ParseCIDR(cidr); err != nil {
```

------------------------------------------------------------------------

## 9. V3.4 登录页面 TOTP 输入框溢出

### 现象

6 个 Authenticator Code 输入框横向跑出登录 Card。

### 根因

OTP input 使用了不合适的固定宽度/布局，6 个输入框总宽度远大于登录卡片。

### 正确布局

``` css
.otp-grid {
    display: grid;
    grid-template-columns: repeat(6, minmax(0, 1fr));
    gap: 10px;
    width: 100%;
}

.otp-grid input {
    width: 100%;
    min-width: 0;
    box-sizing: border-box;
}
```

Web UI Patch 应只替换 Web UI / Audit Server 前端资源，不应修改：

``` text
MySQL
eBPF
NetBird PAT
TOTP Secret
TLS
Sessions
Access Logs
Resources
```

------------------------------------------------------------------------

## 10. Docker / nftables 安装器改进

未来 installer 不应该静默：

``` text
disable nftables
restart docker
```

因为 NetBird 与 Audit 共用 Docker daemon。

推荐增加 preflight：

``` bash
docker network create nbaudit-preflight
docker network rm nbaudit-preflight
```

失败时明确提示：

``` text
Docker firewall chains appear broken.
Restarting Docker may temporarily interrupt NetBird VPN.
```

由管理员确认后再修复。

------------------------------------------------------------------------

## 11. Internal Nginx Gateway 方案

为了获得比 eBPF 更丰富的 HTTP/HTTPS 审计，可以增加统一内部 Nginx
Gateway：

``` text
Employees
    ↓
NetBird
    ↓
Routing Peer
    ↓
Internal Nginx Gateway
    ├── JumpServer
    ├── GitLab
    ├── ArgoCD
    └── Jenkins
```

Nginx 可记录：

``` text
Time
Source IP
Host
URI
HTTP Method
Status
Bytes
User-Agent
Request Time
Upstream
```

再通过 NetBird API 将：

``` text
100.126.x.x
```

映射到：

``` text
User + Device
```

最终 Audit UI 可以显示：

``` text
Alice / MacBook
→ Jenkins
→ GET /job/backend/
→ HTTP 200
```

### 必要条件

必须防止员工绕过 Gateway 直接访问 backend。

后端 Security Group / Firewall 应仅允许 Nginx Gateway 访问对应应用端口。

### 推荐职责

``` text
HTTP / HTTPS       → Nginx application access audit
SSH / DB / TCP     → eBPF network flow audit
JumpServer         → JumpServer session/command audit
NetBird API        → identity + VPN session correlation
```

------------------------------------------------------------------------

## 12. 当前排错基线

截至当前排查，已确认：

  Layer                         Status
  ----------------------------- ------------
  NetBird Management            OK
  NetBird Signal                OK
  NetBird `wt0`                 OK
  Overlay `100.126.0.0/16`      OK
  Employee → Resource routing   OK
  Traffic visible on `wt0`      OK
  TCX Sensor attach             OK
  Audit Server HTTPS `9443`     OK
  Sensor API `127.0.0.1:9080`   OK
  eBPF `flows` map content      **待确认**
  Sensor flow flush             **待确认**
  Audit Resource matching       **待确认**
  MySQL `network_flows`         **待确认**
  Access Logs UI                Empty

## 13. 下一次开发从这里继续

不要重新安装 NetBird，也不要先改 UI。

第一步：

``` bash
sudo bpftool prog show
sudo bpftool map show
sudo bpftool net
```

Mac 制造访问：

``` bash
curl -I http://<AUDITED_RESOURCE>
```

然后：

``` bash
sudo bpftool map dump id <FLOW_MAP_ID>
```

如果 map 有记录，继续检查：

``` text
Go sensor map reader
→ flush
→ POST /api/v1/flows
→ Resource matching
→ INSERT network_flows
```

如果 map 没记录，则直接检查：

``` text
TCX ingress/egress
→ audit.bpf.c packet parsing
→ overlay matching
→ flow key construction
→ bpf_map_update_elem()
```

## 14. 安全注意事项

历史排错过程中曾经使用过 shell xtrace 等方式，存在把 Secret
打到终端/日志的风险。

后续统一要求：

``` text
Never use: bash -x setup*.sh
Never paste: config.env
Never print: PAT / TOTP_SECRET / MYSQL password / SENSOR_KEY
```

如果任何真实 Secret 曾经暴露，应立即 rotate。

------------------------------------------------------------------------

## 快速诊断命令

``` bash
# NetBird
netbird status
ip -br addr show wt0

# Audit services
systemctl status netbird-audit-server --no-pager
systemctl status netbird-audit-sensor --no-pager

# Ports
ss -lntp | grep -E ':(9080|9443)\b'

# Recent sensor errors
journalctl -u netbird-audit-sensor --since "5 minutes ago" --no-pager

# Confirm routed traffic
sudo tcpdump -ni wt0 host <RESOURCE_IP>

# eBPF
sudo bpftool prog show
sudo bpftool map show
sudo bpftool net

# Docker firewall
iptables -S | grep DOCKER
```

------------------------------------------------------------------------

**当前最重要的未解决项：确认 eBPF `flows` map 在真实 NetBird Resource
流量经过 `wt0` 时是否产生记录。**
