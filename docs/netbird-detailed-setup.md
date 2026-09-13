# NetBird 详细配置手册

> 场景：通过 NetBird 访问 AWS 私网资源，并让指定公网域名（例如
> `grafana.eks-tools.com`）通过 AWS 固定公网 IP 出口访问
> Cloudflare；其他公网流量仍走客户端本地网络。

## 1. 最终目标架构

### 1.1 私网资源

``` text
Mac / Developer
NetBird: 100.126.240.232
        |
        | NetBird
        v
AWS Routing Peer
ip-10-0-0-210
NetBird: 100.126.26.97
Private: 10.0.0.210
        |
        +----> 10.0.0.33/32     jumpserver
        +----> 10.0.10.189/32   jenkins-new
        +----> 10.0.20.49/32    jenkins-old
```

### 1.2 Cloudflare 公网域名固定出口

``` text
普通公网流量:
Mac ---> Local ISP ---> Internet

grafana.eks-tools.com:
Mac
 |
 | NetBird Domain Resource
 v
AWS Routing Peer: ip-10-0-0-210
 |
 | SNAT / AWS Internet Egress
 v
AWS Fixed EIP
 |
 v
Cloudflare
 |
 | IP Allowlist
 v
grafana.eks-tools.com
```

这样 Cloudflare 不需要信任开发人员家里、办公室、酒店或手机热点的动态公网
IP，只需要信任 AWS 的固定出口 IP。

------------------------------------------------------------------------

## 2. 当前环境

本文示例环境：

  项目                      值
  ------------------------- ---------------------------------
  NetBird Management        `https://vpn.eks-tools.com:443`
  Mac NetBird IP            `100.126.240.232`
  AWS Routing Peer          `ip-10-0-0-210`
  Routing Peer NetBird IP   `100.126.26.97`
  Routing Peer Private IP   `10.0.0.210`
  Grafana Domain            `grafana.eks-tools.com`
  Grafana 类型              Cloudflare 公网域名
  NetBird Client            `0.78.1`
  客户端访问组              `developers`

------------------------------------------------------------------------

## 3. 配置原则

需要严格区分以下概念：

``` text
developers
= 谁可以访问资源

Routing Peer
= 谁负责转发流量

Resource
= 客户端允许访问什么

Masquerade
= 通过 Routing Peer 做 SNAT

Cloudflare Allowlist
= Cloudflare 最终允许哪个公网出口 IP
```

不要把 Mac 客户端设置成 AWS Network 的 Routing Peer。

------------------------------------------------------------------------

## 4. 创建客户端 Group

进入：

`Access Control -> Groups`

创建：

``` text
developers
```

把需要访问资源的客户端加入该组，例如：

``` text
developers
└── vipers-Mac-Studio.local
    └── 100.126.240.232
```

建议另外建立 Routing Peer 专用 Group：

``` text
aws-egress-routing-peers
└── ip-10-0-0-210
```

客户端授权组和 Routing Peer 组不要混用。

------------------------------------------------------------------------

## 5. 配置 AWS Routing Peer

Routing Peer：

``` text
Hostname: ip-10-0-0-210
Private IP: 10.0.0.210
NetBird IP: 100.126.26.97
```

### 5.1 检查 NetBird

``` bash
netbird status --detail
```

应显示 Management、Signal 已连接。

### 5.2 开启 Linux IPv4 Forwarding

``` bash
sysctl net.ipv4.ip_forward
```

应该得到：

``` text
net.ipv4.ip_forward = 1
```

如未开启：

``` bash
sudo sysctl -w net.ipv4.ip_forward=1
```

永久配置可写入 `/etc/sysctl.d/99-netbird.conf`：

``` text
net.ipv4.ip_forward=1
```

然后：

``` bash
sudo sysctl --system
```

### 5.3 验证私网目标

例如：

``` bash
ip route get 10.0.0.33
ping -c 3 10.0.0.33
curl -vk --connect-timeout 5 http://10.0.0.33
```

只有 Routing Peer 自身能够访问目标，才继续配置 NetBird。

------------------------------------------------------------------------

## 6. 配置私网 Network Resource

进入：

`Network Routing -> Networks`

例如创建：

``` text
Name: jumpserver
Description: Production jumpserver network
```

添加 Resource：

``` text
Name: jumpserver-server
Type: IP / CIDR
Address: 10.0.0.33/32
```

Routing Peer 只选择：

``` text
ip-10-0-0-210
```

如果存在 Masquerade：

``` text
Masquerade: Enabled
```

### Policy

排障阶段：

``` text
Source: developers
Destination: jumpserver-server
Protocol: ALL
Enabled: ON
```

确认工作后，再限制实际所需端口。

其他私网资源可使用同样方式配置，例如：

``` text
jenkins-new -> 10.0.10.189/32
jenkins-old -> 10.0.20.49/32
```

> 不要为了这种新式 Network Resource 配置去创建旧版
> `Network Routing -> Routes` 条目。

------------------------------------------------------------------------

## 7. 配置 Cloudflare 指定域名固定出口

这是本方案最关键的部分。

目标：

``` text
grafana.eks-tools.com
```

保持公网 DNS 和 Cloudflare Proxy 不变。

不要设置：

``` text
grafana.eks-tools.com -> 10.0.0.33
```

也不要用 NetBird Split DNS 把这个公网域名改成私网地址，否则会绕过
Cloudflare。

### 7.1 创建独立 Network

进入：

`Network Routing -> Networks -> Add Network`

创建：

``` text
Name: cloudflare-fixed-egress
Description: Route selected Cloudflare domains through AWS fixed EIP
```

建议公网 Domain Resource 和私网 IP Resource 使用独立
Network，便于维护和排障。

### 7.2 添加 Domain Resource

进入：

`cloudflare-fixed-egress -> Resources -> Add`

填写：

``` text
Name: grafana-prod
Type: Domain
Address: grafana.eks-tools.com
```

注意：正确域名是：

``` text
grafana.eks-tools.com
```

不是：

``` text
grafana-prod.eks-tools.com
```

### 7.3 设置 Routing Peer

该 Network 的 Routing Peer 只使用：

``` text
ip-10-0-0-210
```

不要选择 Mac。

如果 UI 支持 Masquerade：

``` text
Masquerade: Enabled
```

### 7.4 创建 Access Control Policy

配置：

``` text
Protocol: ALL
Source: developers
Direction: ->
Destination: grafana-prod
Enable Policy: ON
```

排障完成后，Grafana 只使用 HTTPS 时可以收紧为：

``` text
Protocol: TCP
Port: 443
Source: developers
Destination: grafana-prod
```

------------------------------------------------------------------------

## 8. Routing Peer DNS Resolution

Domain Resource 需要正确的域名解析能力。

在 NetBird Dashboard 中检查 Network/DNS 相关设置，确保 Routing Peer DNS
Resolution 已启用（具体菜单名称可能随版本变化）。

Routing Peer 上验证：

``` bash
dig +short grafana.eks-tools.com
```

应该解析为 Cloudflare 公网 IP，例如：

``` text
172.67.145.143
104.21.79.120
```

不能解析为内部 `10.x.x.x` 地址，否则流量不会经过预期的 Cloudflare
公网入口。

------------------------------------------------------------------------

## 9. AWS 固定公网出口

在 `ip-10-0-0-210` 上执行：

``` bash
curl -4 https://ifconfig.me
echo
```

记录公网出口 IP。

该 IP 必须是长期稳定的，例如：

-   EC2 绑定 Elastic IP；或
-   Private EC2 通过带固定 Elastic IP 的 NAT Gateway 出口。

不要长期使用会随实例停止/启动发生变化的临时 Public IPv4。

------------------------------------------------------------------------

## 10. Cloudflare IP Allowlist

Cloudflare 必须允许 AWS 固定出口 IP。

例如 AWS 固定出口为：

``` text
43.199.207.236
```

则 Cloudflare Allowlist / WAF Custom Rule 中加入：

``` text
43.199.207.236/32
```

最终 Cloudflare 应该看到：

``` text
Client visible to Cloudflare = AWS Fixed EIP
```

而不是：

``` text
100.126.240.232  # Mac NetBird IP
100.126.26.97    # Routing Peer NetBird IP
10.0.0.210       # AWS private IP
```

------------------------------------------------------------------------

## 11. Mac 客户端验证

重新加载 NetBird：

``` bash
sudo netbird service restart
```

检查：

``` bash
netbird status --detail
netbird networks ls
```

Domain Resource 正常时应类似：

``` text
Available Networks:

- ID: grafana-prod
  Domains: grafana.eks-tools.com
  Status: Selected
  Resolved IPs:
    [grafana.eks-tools.com.]: 104.21.79.120, 172.67.145.143
```

这说明：

1.  Domain Resource 已下发；
2.  客户端已选择该 Network；
3.  域名已解析；
4.  NetBird 已掌握需要动态选路的 Cloudflare IP。

macOS 可触发系统解析：

``` bash
dscacheutil -q host -a name grafana.eks-tools.com
```

然后再次：

``` bash
netbird networks ls
```

------------------------------------------------------------------------

## 12. 验证 Grafana 流量

执行：

``` bash
curl -vk --connect-timeout 10 https://grafana.eks-tools.com/
```

如果 Cloudflare 返回类似：

``` text
HTTP/2 403
server: cloudflare
Sorry, you have been blocked
Your IP: 43.199.207.236
```

这并不代表 NetBird 失败。

恰恰说明：

``` text
Mac
 -> NetBird Domain Resource
 -> AWS Routing Peer
 -> AWS Internet Egress
 -> 43.199.207.236
 -> Cloudflare
```

链路已经成功。

此时只需要把 `43.199.207.236/32` 加入 Cloudflare 白名单。

配置完成后再次：

``` bash
curl -vk https://grafana.eks-tools.com/
```

应该进入 Grafana/Origin，而不再出现 Cloudflare IP Block 页面。

------------------------------------------------------------------------

## 13. 验证普通公网流量没有走 NetBird

本方案不是 Full Tunnel。

Mac 执行：

``` bash
curl -4 https://ifconfig.me
echo
```

这里正常情况下仍然应该显示 Mac 当前 ISP 的公网 IP。

例如：

``` text
Mac normal Internet
-> Local ISP IP

grafana.eks-tools.com
-> NetBird
-> AWS Fixed EIP
-> Cloudflare
```

因此 GitHub、Google 等普通网站不会经过 AWS Routing Peer。

------------------------------------------------------------------------

## 14. Wildcard 域名

确认单域名稳定后，如果需要让多个 `eks-tools.com` 子域统一走 AWS
出口，可以增加 Domain Resource：

``` text
*.eks-tools.com
```

建议先验证：

``` text
grafana.eks-tools.com
```

成功后再扩大范围，避免一次性影响其他生产域名。

注意 wildcard 和根域应视具体 NetBird 版本和匹配规则单独验证。若根域
`eks-tools.com` 本身也需要固定出口，建议单独创建并测试 Resource。

------------------------------------------------------------------------

## 15. 常用排障命令

### Mac

``` bash
netbird status --detail
netbird networks ls

dscacheutil -q host -a name grafana.eks-tools.com

curl -vk --connect-timeout 10 https://grafana.eks-tools.com/
```

### AWS Routing Peer

``` bash
netbird status --detail

sysctl net.ipv4.ip_forward

dig +short grafana.eks-tools.com

curl -4 https://ifconfig.me
echo

curl -vk --connect-timeout 10 https://grafana.eks-tools.com/
```

### NetBird nftables

``` bash
sudo nft list ruleset
```

私网 Resource 排障时：

``` bash
sudo nft list chain ip netbird netbird-rt-fwd
```

如果客户端访问资源后计数器增加，说明流量已经到达 Routing Peer。

------------------------------------------------------------------------

## 16. 判断问题在哪一层

### Domain Resource 没出现

``` bash
netbird networks ls
```

没有 `grafana-prod`：

检查：

-   Policy 是否启用；
-   Source 是否为 `developers`；
-   Mac 是否属于 `developers`；
-   Resource 是否为 `grafana.eks-tools.com`；
-   Routing Peer 是否正确。

### Domain 出现但 Resolved IPs 为空

检查域名解析和 Routing Peer DNS Resolution。

触发：

``` bash
dscacheutil -q host -a name grafana.eks-tools.com
```

然后：

``` bash
netbird networks ls
```

### Cloudflare 返回 403 并显示 AWS EIP

例如：

``` text
Your IP: 43.199.207.236
```

说明 NetBird 已成功，检查 Cloudflare Allowlist/WAF。

### Cloudflare 显示 Mac 本地公网 IP

说明请求没有通过 NetBird Domain Resource，需要检查：

``` bash
netbird networks ls
```

以及 Domain Resource、Policy、Routing Peer 和 DNS Resolution。

### Routing Peer 自己访问 Grafana 都失败

先不要排查 Mac。

在 Routing Peer：

``` bash
curl -vk https://grafana.eks-tools.com/
```

先解决 AWS 出口、DNS、Security Group/NACL、Cloudflare Allowlist 等问题。

------------------------------------------------------------------------

## 17. 推荐生产配置

最终建议：

``` text
Groups
├── developers
│   └── Developer Macs
└── aws-egress-routing-peers
    └── ip-10-0-0-210

Networks
├── jumpserver
│   ├── Resource: 10.0.0.33/32
│   ├── Routing Peer: ip-10-0-0-210
│   └── Policy: developers -> jumpserver
│
└── cloudflare-fixed-egress
    ├── Resource: grafana.eks-tools.com
    ├── Routing Peer: ip-10-0-0-210
    ├── Masquerade: ON
    └── Policy: developers -> grafana-prod TCP/443

Cloudflare
└── Allowlist
    └── AWS Fixed EIP /32
```

------------------------------------------------------------------------

## 18. 上线检查清单

-   [ ] Mac 在 `developers` Group
-   [ ] Routing Peer 只有 `ip-10-0-0-210`
-   [ ] Linux `net.ipv4.ip_forward=1`
-   [ ] 私网资源从 Routing Peer 可达
-   [ ] `grafana-prod` Address 为 `grafana.eks-tools.com`
-   [ ] `grafana-prod` Policy 已启用
-   [ ] Routing Peer DNS Resolution 正常
-   [ ] `netbird networks ls` 显示 `Status: Selected`
-   [ ] `Resolved IPs` 显示 Cloudflare IP
-   [ ] AWS 出口使用固定 EIP
-   [ ] Cloudflare Allowlist 包含 AWS EIP `/32`
-   [ ] Grafana 访问通过 Cloudflare
-   [ ] 普通公网流量仍使用客户端本地 ISP

------------------------------------------------------------------------

## 19. 当前已验证结果

当前 Mac 已成功得到：

``` text
grafana.eks-tools.com
-> 172.67.145.143
-> 104.21.79.120
```

NetBird：

``` text
ID: grafana-prod
Domains: grafana.eks-tools.com
Status: Selected
Resolved IPs:
  104.21.79.120
  172.67.145.143
```

Cloudflare 请求中已经观察到：

``` text
Your IP: 43.199.207.236
```

因此已经证明：

``` text
Mac
 -> NetBird
 -> AWS Routing Peer
 -> AWS public egress
 -> Cloudflare
```

这条选择性公网出口链路工作正常。

剩余生产化重点是确保该公网出口为固定 EIP，并将其正确加入 Cloudflare IP
Allowlist。
