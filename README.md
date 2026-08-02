# m-ui

m-ui 是一个面向单台服务器、单个 Mihomo 实例的轻量级管理面板。它以
SQLite 中的结构化状态为事实来源，确定性生成 Mihomo YAML，并通过校验、原子
发布、健康检查和自动回滚来管理 VLESS/TCP/REALITY/XTLS Vision 节点。

## v0.1 能做什么

- 管理多个 VLESS REALITY Listener 和每个 Listener 的多个用户；
- 生成 UUID、REALITY 密钥、Short ID、分享链接、二维码和客户端 YAML；
- 管理用户启用状态和到期时间，并批量发布到期变更；
- 查看实例级版本、流量、内存、连接数和脱敏日志；
- 通过 systemd、OpenRC 或容器内监督器管理一个本地 Mihomo 进程；
- 在发布前执行固定参数的真实 `mihomo -t` 校验；
- 保存结构化 Revision，支持配置回滚和启动一致性恢复；
- 管理 Mihomo 核心的 release/alpha 检查、更新和回滚；
- 提供中英文界面、深浅色主题和移动端布局。

m-ui 不是通用 YAML 编辑器、订阅聚合器、多节点控制器或用户计费系统。v0.1
不支持其他协议/传输、多管理员 RBAC、公开订阅端点、精确用户流量、配额、限速
或任意第三方 YAML 导入。

## 支持平台与产物

正式产物仅面向 Linux `amd64` 和 `arm64`：

- Debian 12+/sid、Ubuntu 24.04+：systemd、`.deb` 或 `.tar.gz`；
- Alpine 3.20+：OpenRC、`.apk` 或 `.tar.gz`；
- OCI/Docker：非 root、双架构镜像，容器内直接监督 Mihomo。

每个正式或快照构建锁定同一个官方 Mihomo release identity，再分别在原生
amd64/arm64 Runner 下载、校验 SHA-256、执行 `mihomo -v` 和 `mihomo -t`。
归档、deb、apk、SBOM、校验和及容器镜像都来自同一 m-ui 提交。

## 原生安装

从目标 GitHub Release 下载 `manage.sh` 与 `SHA256SUMS`，先验证脚本，再安装：

```sh
version=v0.1.0
base="https://github.com/Aethersailor/m-ui/releases/download/${version}"

curl --fail --location --proto '=https' -O "${base}/manage.sh"
curl --fail --location --proto '=https' -O "${base}/SHA256SUMS"
grep ' manage.sh$' SHA256SUMS | sha256sum --check

chmod 0755 manage.sh
sudo ./manage.sh install --version "$version" --package auto
```

安装器仅接受官方 HTTPS 下载地址，校验所选包的 SHA-256，拒绝路径穿越归档，
创建独立的 `m-ui`/`mihomo` 用户，初始化已校验核心、配置、数据库目录和服务
文件，并执行健康检查。它不会修改 SSH、防火墙、反向代理、证书或 Cloudflare。
全新安装会输出一次性 `admin` 密码；自动化环境可传入权限受限的
`--admin-password-file PATH`，此时脚本不会回显密码内容。

常用生命周期命令：

```sh
sudo ./manage.sh status
sudo ./manage.sh doctor
sudo ./manage.sh update --version v0.1.1
sudo ./manage.sh reinstall --version v0.1.1
sudo ./manage.sh uninstall
sudo ./manage.sh purge --yes
```

`uninstall` 只移除程序和服务文件，保留 `/etc/m-ui`、`/etc/mihomo`、
`/var/lib/m-ui` 和 `/var/lib/mihomo`。只有显式 `purge` 才删除这些数据。

也可以使用已经下载并自行校验的本地产物：

```sh
sudo ./manage.sh install \
  --package tar \
  --archive ./m-ui_0.1.0_linux_amd64.tar.gz \
  --sha256 <64位SHA-256>
```

## 访问面板

原生部署默认仅监听 `127.0.0.1:2095`。首次访问建议使用 SSH 隧道：

```sh
ssh -L 2095:127.0.0.1:2095 user@server
```

然后打开 `http://127.0.0.1:2095/`。系统设置中可以分别配置 m-ui 面板 UI
入口和 Mihomo `external-controller` dashboard API 入口；两者不是同一个
接口。默认仍是回环地址，改为 `0.0.0.0` 或 `::` 后需要按页面提示重启对应
服务，m-ui 到 Mihomo 的内部连接目标仍限制为回环地址。长期域名访问请使用
自行维护的 HTTPS 反向代理，并将 `/etc/m-ui/config.toml` 中的
`cookie_secure` 设为 `true`。m-ui 不会修改防火墙、SSH、反向代理或
Cloudflare 配置。

反向代理示例见 [docs/reverse-proxy.md](docs/reverse-proxy.md)。

## Mihomo 核心管理

核心文件由 m-ui 管理在：

```text
/var/lib/m-ui/core/current/
/var/lib/m-ui/core/staging/
/var/lib/m-ui/core/backups/
```

面板“系统”页可查看实际运行版本、来源、渠道、上游身份、最近检查/更新结果，
并执行检查、更新或回滚。CLI 等价命令为：

```sh
sudo -u m-ui /usr/bin/m-ui core status \
  --config /etc/m-ui/config.toml --json
sudo -u m-ui /usr/bin/m-ui core check \
  --config /etc/m-ui/config.toml
sudo -u m-ui /usr/bin/m-ui core update \
  --config /etc/m-ui/config.toml
sudo -u m-ui /usr/bin/m-ui core rollback \
  --config /etc/m-ui/config.toml
```

支持 `release` 和滚动的 `alpha` 渠道。alpha 以 release ID、资产 ID 和摘要
识别，而不是只比较固定 tag。更新器只访问固定的官方 GitHub API/下载主机，
要求资产元数据提供可信 SHA-256，限制响应、下载和解压大小，拒绝符号链接和
异常所有者/权限，并在候选上依次执行版本与配置校验。激活、运行验证或持久化
失败会恢复上一核心；恢复失败才进入 degraded 状态。

配置发布、核心更新/回滚和运行时动作共用一个协调器，冲突操作返回忙碌，不会
并发替换核心或配置。degraded 状态下禁止写操作和自动核心更新。

## Docker/Compose

Compose 示例位于 [deploy/docker/compose.yml](deploy/docker/compose.yml)：

```sh
cd deploy/docker
printf '%s\n' 'replace-with-a-strong-password' > admin-password.txt
chmod 0600 admin-password.txt
docker compose up -d
docker compose ps
```

镜像使用 UID/GID `10001:10001`，丢弃全部 capability 后只添加
`NET_BIND_SERVICE`，启用 `no-new-privileges`，不包含 Go/Node 构建工具链或
源码。Compose 使用 host network，以便动态 Listener 直接绑定宿主端口；这也
意味着必须自行维护宿主机防火墙。Controller 始终位于容器网络命名空间回环。

以下四个目录必须持久化：

```text
/etc/m-ui
/etc/mihomo
/var/lib/m-ui
/var/lib/mihomo
```

详情见 [deploy/docker/README.md](deploy/docker/README.md)。

## 配置发布与故障闭锁

所有 Listener、用户、设置和到期变更都进入同一个发布事务：

1. 从 SQLite 读取并变更类型化状态；
2. 生成同文件系统候选 YAML 并 fsync；
3. 以固定参数运行真实 `mihomo -t -f <candidate>`；
4. 原子替换、重载、健康检查并写入 Revision；
5. 提交 SQLite；失败时同时恢复活动文件和结构化状态。

如果 SQLite `COMMIT` 返回不确定结果，m-ui 会使用独立、有限时恢复上下文重新
读取持久化状态并分类处理。若恢复动作失败，必须先成功持久化 degraded 状态；
若 degraded 持久化也失败，进程启动会直接失败，绝不会在内存中假装 degraded
并继续提供写服务。

## 服务、诊断和日志

systemd：

```sh
sudo systemctl status m-ui mihomo
sudo journalctl -u m-ui -u mihomo --since today
sudo -u m-ui /usr/bin/m-ui doctor --config /etc/m-ui/config.toml
```

OpenRC：

```sh
sudo rc-service m-ui status
sudo rc-service mihomo status
sudo tail -n 200 /var/log/m-ui.log /var/log/mihomo.log
sudo -u m-ui /usr/bin/m-ui doctor --config /etc/m-ui/config.toml
```

配置历史回滚：

```sh
sudo -u m-ui /usr/bin/m-ui config rollback \
  --config /etc/m-ui/config.toml REVISION_ID
```

管理员密码重置建议使用权限为 `0600` 的临时密码文件，避免进入 Shell 历史：

```sh
sudo install -o m-ui -g m-ui -m 0600 /dev/null /var/lib/m-ui/new-password
sudoedit /var/lib/m-ui/new-password
sudo -u m-ui /usr/bin/m-ui admin reset-password \
  --config /etc/m-ui/config.toml \
  --username admin \
  --password-file /var/lib/m-ui/new-password
sudo rm -f /var/lib/m-ui/new-password
```

更多诊断见 [docs/troubleshooting.md](docs/troubleshooting.md)。

## 备份

数据库和 `master.key` 必须作为一个一致性集合备份：

```sh
sudo systemctl stop m-ui mihomo
sudo tar -C / -czf m-ui-backup.tar.gz \
  etc/m-ui etc/mihomo var/lib/m-ui var/lib/mihomo
sudo systemctl start mihomo m-ui
```

备份包含密码学密钥、节点配置和用户标识，应离线保存并限制权限。缺失原
`master.key` 时，数据库中的 Controller Secret 和 REALITY 私钥无法解密。

## 开发与验证

依赖 Go 1.26.5、Node.js 24.18.0、npm、GNU Make 和 Linux：

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

`make smoke` 会锁定一个官方 Mihomo identity，校验下载摘要，并使用真实核心
验证服务端/客户端生成配置。快照工作流还构建并验收 tar/deb/apk、SPDX SBOM
和原生双架构容器。正式 Release 只能通过手工工作流、显式或自动递增版本模式、
远端 target ref、prerelease 选择和精确 `RELEASE` 确认触发；常规 push 不会创建
Tag 或 Release。

## 文档

- [架构说明](docs/architecture.md)
- [配置与核心生命周期](docs/configuration-lifecycle.md)
- [安全模型](docs/security.md)
- [故障排除](docs/troubleshooting.md)
- [Docker 部署](deploy/docker/README.md)
- [构建与发行策略](docs/release.md)

## License

本项目采用 [GNU General Public License v3.0](LICENSE)。
