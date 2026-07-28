# m-ui

一个用于管理 Mihomo 服务端节点的轻量级 Web 面板。

m-ui 面向希望使用 Mihomo 搭建和管理自建节点的用户，提供 Listener、用户、到期时间、分享链接、二维码、运行状态和配置回滚等常用功能。

它不是通用 Mihomo 配置编辑器，也不是多节点集群面板。m-ui v0.1 专注于把单台服务器上的节点管理做得简单、可靠并且可恢复。

## 主要功能

- 管理多个 VLESS REALITY Listener，未来增加更多协议
- 每个 Listener 可添加多个用户
- 支持用户启用、停用和到期时间
- 自动生成 UUID、REALITY 密钥和 Short ID
- 生成 VLESS 分享链接和二维码
- 生成 Mihomo 客户端 YAML
- 查看 Mihomo 服务状态、版本、内存、流量和连接数
- 在面板中启动、停止、重启或重载 Mihomo
- 配置发布前自动执行真实的 `mihomo -t` 校验
- 保存配置历史，并支持一键回滚
- 记录登录、配置修改和服务操作日志
- 支持简体中文、英文、浅色和深色主题
- 支持桌面端和移动端浏览器

## 当前支持范围

m-ui v0.1 当前支持：

```text
VLESS
└── TCP
    └── REALITY
        └── XTLS Vision
```

运行环境：

- Debian 12 或更高版本
- Ubuntu 24.04 或更高版本
- systemd
- amd64
- arm64

m-ui 和 Mihomo 以两个独立的 systemd 服务运行。

## 快速安装

从 Release 页面选择需要安装的版本，将下面的 `v0.1.0` 替换为对应版本号：

```sh
version=v0.1.0

curl -fLO "https://github.com/Aethersailor/m-ui/releases/download/${version}/install.sh"
curl -fLO "https://github.com/Aethersailor/m-ui/releases/download/${version}/SHA256SUMS"

grep ' install.sh$' SHA256SUMS | sha256sum -c -
sudo sh install.sh --version "$version"
```

安装脚本会自动完成：

- 安装 m-ui
- 安装或复用 Mihomo
- 创建独立的系统用户
- 安装并启用 systemd 服务
- 生成管理员初始密码
- 生成 Mihomo Controller 密钥
- 校验初始 Mihomo 配置
- 启动并检查两个服务

安装完成后，终端会显示：

```text
Panel: http://127.0.0.1:2095/
Username: admin
One-time initial password: <初始密码>
```

初始密码只显示一次，请及时保存。

> [!NOTE]
> 安装脚本不会修改 SSH、防火墙、反向代理、TLS 证书或 Cloudflare 配置。

## 访问面板

m-ui 默认只监听：

```text
127.0.0.1:2095
```

首次使用建议通过 SSH 隧道访问：

```sh
ssh -L 2095:127.0.0.1:2095 user@server
```

然后在本机浏览器打开：

```text
http://127.0.0.1:2095/
```

需要通过域名长期访问时，请自行配置 Caddy 或 Nginx HTTPS 反向代理，并保持 m-ui 继续监听本机回环地址。

启用 HTTPS 后，应修改：

```text
/etc/m-ui/config.toml
```

将以下配置设为：

```toml
[security]
cookie_secure = true
```

随后重启 m-ui：

```sh
sudo systemctl restart m-ui
```

反向代理示例见：

[docs/reverse-proxy.md](docs/reverse-proxy.md)

> [!WARNING]
> 不要将 Mihomo Controller 的 `9090` 端口暴露到公网。

## 基本使用流程

登录面板后，通常按照以下顺序使用：

1. 新建 Listener
2. 设置监听端口、Server Name、目标地址等 REALITY 参数
3. 新建用户并设置名称、UUID 和到期时间
4. 保存配置
5. 复制分享链接、二维码或 Mihomo 客户端 YAML
6. 在客户端导入并连接

保存 Listener 或用户时，m-ui 会先生成候选配置，再调用 Mihomo 对配置进行校验。

只有校验和服务健康检查全部通过后，新配置才会正式生效。失败时会自动恢复上一份可用配置。

## 用户到期

m-ui 会定期检查用户到期时间。

用户到期后会被自动停用。若某个已启用 Listener 的最后一个有效用户因本次到期检查被停用，该 Listener 也会自动停用。

为已经停用的 Listener 新增用户时，Listener 不会自动重新启用，需要管理员手动确认并启用。

## 服务管理

查看服务状态：

```sh
sudo systemctl status m-ui mihomo
```

查看日志：

```sh
sudo journalctl -u m-ui -u mihomo --since today
```

重启 m-ui：

```sh
sudo systemctl restart m-ui
```

重启 Mihomo：

```sh
sudo systemctl restart mihomo
```

运行诊断：

```sh
sudo -u m-ui /usr/local/bin/m-ui doctor \
  --config /etc/m-ui/config.toml
```

校验当前数据库生成的配置：

```sh
sudo -u m-ui /usr/local/bin/m-ui config validate \
  --config /etc/m-ui/config.toml
```

## 重置管理员密码

为避免新密码进入 Shell 历史，可以使用临时文件：

```sh
sudo install -o m-ui -g m-ui -m 0600 /dev/null /var/lib/m-ui/new-password
sudoedit /var/lib/m-ui/new-password

sudo -u m-ui /usr/local/bin/m-ui admin reset-password \
  --config /etc/m-ui/config.toml \
  --password-file /var/lib/m-ui/new-password

sudo rm -f /var/lib/m-ui/new-password
sudo systemctl restart m-ui
```

重置密码后，已有登录会话将失效。

## 配置历史与回滚

m-ui 会为成功发布的配置保留历史版本。

当新配置出现问题时，可以在面板中选择历史版本进行回滚。

也可以使用命令行执行紧急回滚：

```sh
sudo -u m-ui /usr/local/bin/m-ui config rollback \
  --config /etc/m-ui/config.toml REVISION_ID
```

回滚时，m-ui 会重新生成并校验配置，而不是直接把旧 YAML 文件覆盖到当前配置。

## 数据备份

建议一起备份以下目录：

```text
/etc/m-ui/
/etc/mihomo/
/var/lib/m-ui/
```

冷备份示例：

```sh
sudo systemctl stop m-ui mihomo

sudo tar -C / -czf m-ui-backup.tar.gz \
  etc/m-ui etc/mihomo var/lib/m-ui

sudo systemctl start mihomo m-ui
```

> [!IMPORTANT]
> `/var/lib/m-ui/master.key` 必须与数据库一起备份。
>
> 缺少对应的 Master Key 时，数据库中保存的 Mihomo Controller 密钥和 REALITY 私钥将无法解密。

备份文件包含密码学密钥和节点信息，请妥善保存。

## 升级

升级前建议先备份：

```text
/etc/m-ui/
/etc/mihomo/
/var/lib/m-ui/
```

然后下载目标版本的安装脚本和校验文件，重新执行安装命令。

安装器会保留现有数据库、管理员密码和已有配置。

## 卸载

保留配置和数据，仅移除程序及服务：

```sh
sudo sh scripts/uninstall.sh
```

永久删除程序、配置、数据库和密钥：

```sh
sudo sh scripts/uninstall.sh --purge
```

执行 `--purge` 前请先确认备份有效。

## 当前限制

m-ui v0.1 暂不支持：

- VMess、Trojan、Shadowsocks、Hysteria、TUIC 等其他协议
- WebSocket、gRPC、XHTTP 等其他传输方式
- VLESS Encryption、ML-KEM 等扩展能力
- 多服务器和多节点集中管理
- 多管理员和权限分级
- 用户级流量统计、流量配额、限速或设备数限制
- 公共订阅地址和订阅聚合
- 导入或编辑任意 Mihomo YAML
- Docker 部署
- 自动配置防火墙、域名、证书或反向代理

面板中显示的流量和连接数属于整个 Mihomo 实例，不能作为用户计费数据。

## 常见问题

### 可以导入现有 Mihomo 配置吗？

不可以。

m-ui 只管理它自己创建的结构化配置，不支持无损导入或接管任意第三方 YAML。

### 可以同时用其他面板修改 Mihomo 配置吗？

不建议。

`/etc/mihomo/config.yaml` 由 m-ui 自动生成，下一次保存配置时会覆盖外部修改。

### 支持 Docker 吗？

v0.1 不支持。当前版本采用 systemd 原生部署。

### 支持多个管理员吗？

v0.1 只有一个管理员账户，不提供 RBAC。

### 能统计每个用户用了多少流量吗？

不能。

Mihomo Controller 提供的是实例级运行数据，m-ui 不会将其伪装成精确的用户级流量统计。

## 安全说明

m-ui 默认将面板和 Mihomo Controller 绑定在本机回环地址。

项目还包括：

- Argon2id 管理员密码哈希
- Session 与 CSRF 防护
- 登录限速
- REALITY 私钥和 Controller 密钥加密存储
- 独立非 root 服务用户
- 受限的 sudoers 权限
- 配置发布前真实 Mihomo 校验
- 配置失败自动恢复
- 敏感日志和审计信息脱敏

不要在公开 Issue 中提交：

- 管理员密码
- `master.key`
- REALITY 私钥
- Controller Secret
- 完整 UUID
- 完整分享链接

安全漏洞请私下联系项目维护者。

## 开发

依赖：

- Go 1.26.5
- Node.js 24.18.0
- npm
- GNU Make
- Linux

运行检查：

```sh
npm --prefix web ci

go test ./...
go vet ./...
go test -race ./...

npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build

make build
make smoke
```

前端使用 Vue 3 和 TypeScript，构建结果会嵌入 Go 二进制，正式部署时不需要单独运行 Node.js 服务。

## 相关文档

- [架构说明](docs/architecture.md)
- [安全模型](docs/security.md)
- [配置生命周期](docs/configuration-lifecycle.md)
- [反向代理](docs/reverse-proxy.md)
- [故障排除](docs/troubleshooting.md)

## License

本项目采用 [GNU General Public License v3.0](LICENSE) 开源。
