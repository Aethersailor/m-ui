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
> m-ui 默认监听 `127.0.0.1:2095`，Mihomo Controller 默认监听 `127.0.0.1:9090`。
>
> 两个入口用途不同，并且默认都不会直接暴露到公网。

### 方式一：原生部署

适用于 Debian、Ubuntu 和 Alpine。安装器会自动识别系统与架构，并选择 `.deb`、`.apk` 或 `.tar.gz` 产物。

#### 1. 下载并校验安装器

```sh
mkdir -p ~/m-ui-install
cd ~/m-ui-install

curl --fail --location \
  --proto '=https' --proto-redir '=https' \
  -O https://github.com/Aethersailor/m-ui/releases/latest/download/manage.sh

curl --fail --location \
  --proto '=https' --proto-redir '=https' \
  -O https://github.com/Aethersailor/m-ui/releases/latest/download/SHA256SUMS

grep ' manage.sh$' SHA256SUMS | sha256sum --check
chmod 0755 manage.sh
```

只有在校验结果显示 `manage.sh: OK` 后再继续安装。

#### 2. 安装最新稳定版本

```sh
sudo ./manage.sh install \
  --version latest \
  --package auto
```

安装器将自动完成：

- 下载并校验适合当前系统和架构的软件包；
- 创建独立的 `m-ui` 与 `mihomo` 系统用户；
- 初始化配置、数据库、主密钥和 Mihomo 核心；
- 安装并启用 systemd 或 OpenRC 服务；
- 启动 m-ui 与 Mihomo；
- 执行服务、Controller 和配置健康检查。

全新安装完成后，终端会显示一次性的管理员凭据：

```text
Initial administrator: admin
One-time initial password: ...
```

请立即保存该密码，并在首次登录后修改。

<details>
<summary><strong>使用自定义初始密码</strong></summary>

先创建仅当前用户可读的密码文件：

```sh
umask 077
printf '%s\n' '请在此填写足够强的密码' > admin-password.txt
```

安装时指定该文件：

```sh
sudo ./manage.sh install \
  --version latest \
  --package auto \
  --admin-password-file ./admin-password.txt
```

安装完成后删除临时文件：

```sh
rm -f admin-password.txt
```

密码内容不会由安装器回显。

</details>

#### 3. 访问面板

推荐通过 SSH 隧道首次访问：

```sh
ssh -L 2095:127.0.0.1:2095 user@server
```

然后在本地浏览器打开：

```text
http://127.0.0.1:2095/
```

使用安装时生成的 `admin` 账号登录。

长期使用域名访问时，应配置独立的 HTTPS 反向代理，并保持 m-ui 继续监听回环地址。具体示例见：

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

#### 4. 常用管理命令

安装后可直接使用系统内置的管理脚本：

```sh
# 查看版本、目录和服务状态
sudo /usr/lib/m-ui/manage.sh status

# 执行完整诊断
sudo /usr/lib/m-ui/manage.sh doctor

# 更新到最新稳定版本
sudo /usr/lib/m-ui/manage.sh update --version latest

# 重新安装程序并保留数据
sudo /usr/lib/m-ui/manage.sh reinstall --version latest

# 删除程序和服务，保留配置与数据
sudo /usr/lib/m-ui/manage.sh uninstall

# 删除程序、配置、数据库、密钥、Revision 和核心
sudo /usr/lib/m-ui/manage.sh purge
```

`uninstall` 会保留已有数据；只有 `purge` 才会删除全部托管内容。

#### 5. 文件与数据位置

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

#### 1. 准备部署目录

```sh
sudo install -d -m 0700 /opt/m-ui
cd /opt/m-ui

sudo curl --fail --location \
  --proto '=https' --proto-redir '=https' \
  -o compose.yml \
  https://raw.githubusercontent.com/Aethersailor/m-ui/master/deploy/docker/compose.yml
```

#### 2. 选择镜像版本

使用最新稳定版本：

```sh
printf '%s\n' 'M_UI_IMAGE_TAG=latest' | sudo tee .env >/dev/null
```

生产环境建议在确认版本后固定为不可变的正式版本标签：

```env
M_UI_IMAGE_TAG=vX.Y.Z
```

`edge` 对应当前 `master` 分支的开发快照，不建议用于正式环境。

#### 3. 创建管理员密码

使用 OpenSSL 生成随机密码：

```sh
sudo sh -c 'umask 077 && openssl rand -base64 32 > admin-password.txt'
```

查看并保存密码：

```sh
sudo cat admin-password.txt
```

该密码文件会作为 Docker Secret 提供给容器，仅在首次创建数据库时用于初始化 `admin` 账号。

需要默认使用中文界面时，可将 `compose.yml` 中的：

```yaml
M_UI_LANGUAGE: en-US
```

修改为：

```yaml
M_UI_LANGUAGE: zh-CN
```

#### 4. 启动服务

```sh
sudo docker compose up -d
sudo docker compose ps
```

查看健康状态：

```sh
sudo docker inspect \
  --format '{{json .State.Health}}' \
  m-ui
```

查看日志：

```sh
sudo docker compose logs --tail=100 -f m-ui
```

#### 5. 访问面板

Docker Compose 使用 host network，但 m-ui 仍默认只监听宿主机回环地址：

```text
127.0.0.1:2095
```

通过 SSH 隧道访问：

```sh
ssh -L 2095:127.0.0.1:2095 user@server
```

然后打开：

```text
http://127.0.0.1:2095/
```

管理员账号为 `admin`，密码为 `admin-password.txt` 中保存的内容。

> [!WARNING]
> Compose 使用 `network_mode: host`，以便面板动态创建的 Mihomo Listener 直接绑定宿主机端口。
>
> 这意味着 Docker 不会为这些端口提供独立的端口映射和隔离，必须自行配置宿主机防火墙。

#### 6. 更新容器

更新 `.env` 中的镜像标签后执行：

```sh
sudo docker compose pull
sudo docker compose up -d
sudo docker compose ps
```

容器重建不会覆盖已有数据库、主密钥、配置、Revision 或托管核心。

#### 7. 持久化数据

Compose 使用四个命名卷：

| Volume | 容器路径 | 内容 |
|---|---|---|
| `m-ui-etc` | `/etc/m-ui` | m-ui 配置 |
| `mihomo-etc` | `/etc/mihomo` | Mihomo 配置 |
| `m-ui-data` | `/var/lib/m-ui` | 数据库、密钥、Revision 和核心 |
| `mihomo-data` | `/var/lib/mihomo` | Mihomo 运行数据 |

停止并删除容器：

```sh
sudo docker compose down
```

该命令默认保留命名卷。

> [!CAUTION]
> 除非已经完成备份并确认需要永久清除全部数据，否则不要执行：
>
> ```sh
> docker compose down --volumes
> ```

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
