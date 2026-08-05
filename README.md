<div align="center">

# m-ui

**面向单台服务器与单个 Mihomo 实例的轻量级管理面板**

通过结构化配置、安全发布和自动回滚，简化  
**VLESS · TCP · REALITY · XTLS Vision** 节点的部署与维护。

<p>
  <a href="https://github.com/Aethersailor/m-ui/releases">
    <img src="https://img.shields.io/github/v/release/Aethersailor/m-ui?include_prereleases&sort=semver" alt="Release">
  </a>
  <a href="https://github.com/Aethersailor/m-ui/blob/master/LICENSE">
    <img src="https://img.shields.io/github/license/Aethersailor/m-ui" alt="License">
  </a>
  <a href="https://github.com/Aethersailor/m-ui/pkgs/container/m-ui">
    <img src="https://img.shields.io/badge/container-GHCR-2496ED?logo=docker&logoColor=white" alt="GHCR">
  </a>
  <a href="https://github.com/Aethersailor/m-ui/actions/workflows/build-release.yml">
    <img src="https://github.com/Aethersailor/m-ui/actions/workflows/build-release.yml/badge.svg?branch=master" alt="Build">
  </a>
  <a href="https://github.com/Aethersailor/m-ui/security/code-scanning">
    <img src="https://github.com/Aethersailor/m-ui/actions/workflows/github-code-scanning/codeql/badge.svg?branch=master" alt="CodeQL">
  </a>
  <img src="https://img.shields.io/badge/platform-Linux-FCC624?logo=linux&logoColor=black" alt="Linux">
  <img src="https://img.shields.io/badge/arch-amd64%20%7C%20arm64-555555" alt="amd64 and arm64">
</p>

[项目简介](#项目简介) · [部署](#部署) · [致谢](#致谢) · [许可证](#许可证)

</div>

---

## 项目简介

m-ui 是一个专注于 **单机 Mihomo 服务端管理** 的 Web 面板。

项目使用 SQLite 保存管理员、Listener、用户、设置和配置历史等结构化状态，并据此确定性生成 Mihomo YAML。每次配置变更都会经过真实的 `mihomo -t` 校验、原子发布、运行状态检查和失败回滚，避免将不完整或无效的配置直接投入运行。

后端采用 Go 编写，Vue 前端被嵌入单一可执行文件中，无需额外部署 Web 服务或数据库。

### 主要功能

| 能力 | 说明 |
|---|---|
| Listener 管理 | 创建和管理多个 VLESS + TCP + REALITY + XTLS Vision Listener |
| 用户管理 | 为每个 Listener 管理多个用户、启用状态和到期时间 |
| 连接分享 | 生成 UUID、REALITY 密钥、Short ID、VLESS 链接、二维码和客户端 YAML |
| 安全发布 | 配置生成、真实核心校验、原子替换、健康检查和自动回滚 |
| 配置历史 | 保存结构化 Revision，并支持经过重新校验的配置回滚 |
| 核心管理 | 查看、检查、更新和回滚 Mihomo Release 或 Alpha 核心 |
| 运行状态 | 查看实例级流量、内存、连接数、核心版本和脱敏日志 |
| 管理界面 | 中文与英文界面、深浅色主题及移动端布局 |

> [!IMPORTANT]
> m-ui 当前专注于单台服务器上的一个 Mihomo 实例。
>
> 它不是通用 YAML 编辑器、Mihomo Dashboard、多节点控制器、订阅聚合器或用户计费系统，也不提供任意第三方配置导入、精确用户流量统计、配额和限速功能。

### 支持环境

| 部署方式 | 支持环境 | 架构 |
|---|---|---|
| 原生部署 | Debian 12+/sid、Ubuntu 24.04+、Alpine 3.20+ | amd64、arm64 |
| Docker | 支持 Docker Engine 与 Docker Compose 的 Linux | amd64、arm64 |

原生部署在 Debian、Ubuntu 上使用 systemd，在 Alpine 上使用 OpenRC。m-ui 与 Mihomo 默认以独立的非特权用户运行。

---

## 部署

m-ui 的正式 Release 同时提供原生安装包、压缩包和多架构容器镜像，并包含经过版本、摘要和配置校验的 Mihomo 核心。

无论采用哪种部署方式，均无需另外安装 Mihomo。

> [!NOTE]
> m-ui 面板默认监听 `0.0.0.0:2095`，可直接通过服务器 IP 访问；Mihomo Controller 仍默认监听 `127.0.0.1:9090`。
>
> 面板入口同时承载完整的 `/api/v1` 管理 API。m-ui 不会替用户配置防火墙、HTTPS、VPN 或访问控制；公网部署前应自行完成这些保护，也可以在 Web 系统设置中把面板改回 `127.0.0.1`。

### 方式一：原生部署

适用于 Debian、Ubuntu 和 Alpine。安装器会自动识别系统与架构，并选择 `.deb`、`.apk` 或 `.tar.gz` 产物。

#### 快速安装

```sh
curl -fsSL https://github.com/Aethersailor/m-ui/releases/latest/download/install.sh | sudo sh
```

安装脚本会自动下载并校验正式 Release，然后安装最新稳定版本。需要固定版本时：

```sh
curl -fsSL https://github.com/Aethersailor/m-ui/releases/latest/download/install.sh | sudo sh -s -- --version vX.Y.Z
```

安装器将自动完成：

- 下载并校验适合当前系统和架构的软件包；
- 创建独立的 `m-ui` 与 `mihomo` 系统用户；
- 初始化配置、数据库、主密钥和 Mihomo 核心；
- 安装并启用 systemd 或 OpenRC 服务；
- 启动 m-ui 与 Mihomo；
- 执行服务、Controller 和配置健康检查。

全新安装不会生成、打印或要求管理员密码。安装完成后直接打开
`http://SERVER_IP:2095/`，在网页中创建管理员账号和密码即可，不需要再登录 SSH、执行
容器命令或粘贴设置码。首次创建以单个原子事务完成，成功后初始化入口永久关闭。

`m-ui admin reset-password` 仅用于已有管理员的本机恢复，不能创建首个管理员。请在将
全新面板暴露给不受信任的网络前完成初始化。

#### 访问面板

直接在浏览器访问（请把 `SERVER_IP` 替换为服务器地址）：

```text
http://SERVER_IP:2095/
```

页面检测到尚无管理员时会直接显示创建表单，不需要 setup link、token 或服务器命令。
首次设置完成后，页面会自动进入节点创建向导，之后 `/setup` 不再允许重复初始化。

使用刚刚在首次设置页面中创建的管理员账号登录。

> [!WARNING]
> 默认的 `0.0.0.0:2095` 会在所有 IPv4 接口暴露面板和管理 API。若端口可从公网到达，请自行配置 HTTPS、VPN、防火墙或访问控制。

长期使用域名访问时，应配置独立的 HTTPS 反向代理，并在 Web 系统设置中把面板入口改为回环地址。具体示例见：

- [反向代理配置](docs/reverse-proxy.md)
- [安全模型](docs/security.md)

使用 HTTPS 后，需要修改：

```toml
# /etc/m-ui/config.toml

[security]
cookie_secure = true
```

然后重启 m-ui：

```sh
sudo systemctl restart m-ui
```

> [!WARNING]
> 不要直接将管理面板暴露到公网，也不要反向代理或公开 Mihomo Controller 的 `9090` 端口。

#### 常用管理命令

安装后不需要记住 `/usr/lib/m-ui/manage.sh` 路径，直接使用 `m-ui`：

```sh
# 查看版本、目录和服务状态
m-ui status

# 执行完整诊断
sudo m-ui doctor

# 更新到最新稳定版本
sudo m-ui update

# 重新安装程序并保留数据
sudo m-ui reinstall

# 删除程序和服务，保留配置与数据
sudo m-ui uninstall

# 删除程序、配置、数据库、密钥、Revision 和核心
sudo m-ui purge
```

`status`、`update`、`reinstall`、`uninstall` 和 `purge` 会安全转交给已安装的生命周期脚本；
`uninstall` 会保留已有数据，只有 `purge` 才会删除全部托管内容。

#### 文件与数据位置

| 路径 | 内容 |
|---|---|
| `/etc/m-ui` | m-ui 配置 |
| `/etc/mihomo` | m-ui 生成并管理的 Mihomo 配置 |
| `/var/lib/m-ui` | SQLite 数据库、主密钥、Revision 和托管核心 |
| `/var/lib/mihomo` | Mihomo 运行数据 |

其中 `/var/lib/m-ui/master.key` 与 `m-ui.db` 必须作为同一个一致性集合备份。丢失原主密钥后，数据库中加密保存的 Controller Secret 和 REALITY 私钥将无法恢复。

---

### 方式二：Docker Compose 部署

Docker 镜像通过 GHCR 发布：

```text
ghcr.io/aethersailor/m-ui
```

容器以 UID/GID `10001:10001` 非 root 身份运行，内部直接监督 Mihomo，不运行 systemd 或 OpenRC。

#### 快速部署

确认 Docker Engine 与 Docker Compose v2 已安装后，一条命令即可完成目录、
Compose、镜像、健康检查和首次设置链接的准备：

```sh
curl -fsSL https://github.com/Aethersailor/m-ui/releases/latest/download/install-docker.sh | sudo sh
```

需要让安装器输出域名形式的链接时：

```sh
curl -fsSL https://github.com/Aethersailor/m-ui/releases/latest/download/install-docker.sh | \
  sudo sh -s -- --base-url https://m-ui.example.com
```

默认 Compose 只有一个服务和一个 `/opt/m-ui/data:/data` 映射。长期运行的
m-ui/Mihomo 始终使用 UID/GID `10001:10001`；数据目录必须预先交给该 UID，
容器不会以 root 修复宿主机权限。无需 `.env`、密码文件、Docker Secret 或
初始化容器。

仓库和所有正式 Release 附带的 Compose 永远使用
`ghcr.io/aethersailor/m-ui:latest`。面板地址、Public Host、Mihomo Controller、
CORS、核心通道、自动更新和检查周期均使用安全初始值，首次登录后在 Web 的
系统设置中管理。若要改用其他宿主目录，直接修改 Compose 中唯一的 volume
源路径，并以 `10001:10001`、`0700` 权限提前创建它。

查看健康状态：

```sh
sudo docker inspect \
  --format '{{json .State.Health}}' \
  m-ui
```

查看日志：

```sh
sudo docker compose -f /opt/m-ui/compose.yml logs --tail=100 -f m-ui
```

#### 访问面板

Docker Compose 使用 host network，m-ui 默认监听宿主机所有 IPv4 接口：

```text
0.0.0.0:2095
```

直接打开 `http://SERVER_IP:2095/`，网页会在没有管理员时自动进入首次设置。无需 SSH、
setup link 或设置码。需要限制到本机时，可在 Web 系统设置中改为 `127.0.0.1` 并重启容器。

> [!WARNING]
> Compose 使用 `network_mode: host`，以便面板动态创建的 Mihomo Listener 直接绑定宿主机端口。
>
> 这意味着 Docker 不会为面板和 Listener 端口提供独立的端口映射和隔离，必须自行配置 HTTPS、VPN、访问控制和宿主机防火墙。

#### 更新与数据

更新到最新稳定 Release：

```sh
cd /opt/m-ui
sudo docker compose pull
sudo docker compose up -d
```

容器重建不会覆盖已有数据库、主密钥、配置、Revision 或托管核心。

旧版本使用四个 bind mount 或四个 named volumes 的用户，请先停止旧项目，
按照 `deploy/docker/README.md` 使用 `deploy/docker/migrate-volumes.sh` 显式
迁移。迁移拒绝非空目标目录，不删除或修改源数据。

容器内仍保留标准路径，它们由镜像映射到单一数据根目录：

| 宿主目录 | 容器内数据路径 | 标准访问路径 |
|---|---|---|
| `/opt/m-ui/data/etc/m-ui` | `/data/etc/m-ui` | `/etc/m-ui` |
| `/opt/m-ui/data/etc/mihomo` | `/data/etc/mihomo` | `/etc/mihomo` |
| `/opt/m-ui/data/var/lib/m-ui` | `/data/var/lib/m-ui` | `/var/lib/m-ui` |
| `/opt/m-ui/data/var/lib/mihomo` | `/data/var/lib/mihomo` | `/var/lib/mihomo` |

停止并删除容器：

```sh
sudo docker compose -f /opt/m-ui/compose.yml down
```

该命令保留 `/opt/m-ui/data` 中的持久化数据。

> [!CAUTION]
> `docker compose down`（包括带 `--volumes`）不会删除 bind mount 对应的
> `/opt/m-ui/data` 宿主目录。只有在完成备份并再次确认路径后，才手动删除整个
> 持久化根目录；不要把它与 Compose 的容器删除混为一谈。

更多 Docker 运维说明见 [Docker 部署文档](deploy/docker/README.md)。

---

## 致谢

m-ui 的运行、配置校验和节点能力由 [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo) 提供。

正式 Release 软件包和容器镜像包含一个经过校验的 Mihomo 二进制文件。其上游仓库、Release ID、Tag、Asset ID、文件名、发布时间和 SHA-256 摘要均记录在随附的 `manifest.json` 中。

感谢 Mihomo 项目及其贡献者提供高性能、功能完整且持续维护的开源网络代理核心。

m-ui 是独立的社区项目，并非 MetaCubeX 官方管理面板。

第三方组件说明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

---

## 许可证

m-ui 使用 [GNU General Public License v3.0](LICENSE) 发布。

你可以在 GPL-3.0 条款允许的范围内使用、研究、修改和重新分发本项目。分发修改版本或包含本项目代码的衍生作品时，应继续遵守 GPL-3.0 的源代码开放及许可证保留要求。

项目随附的 Mihomo 核心同样采用 GPL-3.0，其对应源代码可从 `manifest.json` 所记录的上游版本获取。
