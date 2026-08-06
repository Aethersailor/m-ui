# R3 数据切换与上线合同

本文定义从 v0.2.3 数据库进入 R3 五协议节点模型时，哪些状态必须保留、哪些状态
有意清空，以及上线和恢复必须满足的门禁。它只描述当前迁移、Store、App 和 Publisher
已经具备的边界；不包含 R4 订阅、证书资产或其他后续能力。

相关实现：[`0006_nodes_v2.sql`](../migrations/0006_nodes_v2.sql)、
[`0007_r3_protocol_cutover.sql`](../migrations/0007_r3_protocol_cutover.sql)、
[`0008_r3_runtime_convergence.sql`](../migrations/0008_r3_runtime_convergence.sql)和
[`configuration-lifecycle.md`](configuration-lifecycle.md)。

## 1. 切换触发与前置条件

`store.Open` 在打开 SQLite 时按版本逐个执行迁移，每个迁移使用独立事务。新镜像第一次
启动就可能提交 `0006`、`0007` 和 `0008`；这不是登录后才发生的 Web 操作，也不是可在
面板中撤销的 Revision。若前一个迁移已提交而后一个失败，数据库会停留在中间版本，应用
拒绝启动，因此必须用切换前的完整冷备恢复，不能只删除 `schema_migrations` 记录重跑。

开始前必须同时满足：

- m-ui 与 Mihomo 已停止，没有进程继续写 SQLite、WAL、活动 YAML 或核心状态；
- `system_state.degraded = 0`；已有 degraded 事故必须先在旧版本修复；
- 已保存“旧镜像 + 旧 Compose/部署文件 + 未迁移数据”的同一恢复单元；
- 先在该数据副本上完成一次隔离迁移演练，原数据保持不动；
- 已记录旧面板地址、旧 Mihomo Listener 端口和镜像 digest，供切换后反向验证。

## 2. 数据处置矩阵

| 状态 | R3 切换结果 | 合同依据 |
|---|---|---|
| `admin_users` | 保留 | `0006`/`0007`/`0008` 不修改；原管理员继续使用 |
| `sessions` | 保留 | 不主动强制登出；仅未过期且 cookie 仍匹配的会话继续有效 |
| `settings` | 保留 | 包括面板偏好、Controller 密文、路径和历史上限 |
| `audit_logs` | 保留 | 旧审计不改写；切换本身不是配置 Revision |
| `core_settings`、`core_state` | 保留 | 核心渠道、自动更新和已验证核心状态不重置 |
| endpoint active/pending/last-applied | 保留 | generation 与两侧 restart requirement 原样保留 |
| `bootstrap_state` | 保留 | 已有管理员不会重新进入首次设置 |
| `system_state` | 保留 | 不借迁移清除 degraded；上线前必须已是非 degraded |
| `listeners`、`listener_users` | 删除 | `0006` 删除行并删除旧表 |
| `nodes`、`node_users`、`access_profiles` | 重建为空 | `0006` 建 V2，`0007` 重建五协议约束，`0008` 再清空 pre-0008 开发数据 |
| `config_revisions` | 清空 | `0006`、`0007` 和 `0008` 均清理旧历史；旧 Revision 无法表达新的协议所有权和加密 purpose |
| `runtime_convergence_state` | 新建为 pending | `r3_cutover_pending = 1`，`pending_reason = 'r3_protocol_cutover'` |
| Revision 文件目录 | 迁移不删除 | 已成为无数据库引用的旧制品；上线验收不能把它当作可回滚历史 |
| `/etc/mihomo/config.yaml` | 迁移不改写 | 必须由切换后的 baseline publication 接管，见下一节 |

会话保留是明确策略，不是验收捷径。升级验收要同时证明：旧浏览器中尚未过期的会话
可以继续访问；清除 cookie 后仍能用原管理员密码重新登录；页面不会回到首次创建管理员。
密码重置、密码更新和会话自然过期仍按现有认证逻辑撤销或删除会话。

## 3. 旧活动 YAML、Listener 与 baseline Revision

数据库迁移不会删除活动 YAML，也不会直接停止其中的旧 Listener。因此 `0008` 同时写入
持久化 cutover pending 标记；**完成数据库迁移不等于完成运行面切换**。

pre-runtime reconciliation 看到 pending、空节点且没有 active Revision 时，会自动发布
reason 为 `r3_protocol_cutover_baseline` 的 baseline Revision：它从迁移后保留的设置和空
节点集合编译配置，经过真实 `mihomo -t`、原子替换和 SQLite 提交，生成 Revision 1。
该 publication 设置 `RestartRequired`，不会把“YAML 已替换”误判为“旧进程已收敛”。

managed 模式随后用 baseline 启动 Mihomo；native 模式通过现有 ready-lock 顺序等待独立
Mihomo 服务启动。post-runtime reconciliation 必须 reload、读取 Controller 并通过健康
检查，才调用 `CompleteR3ProtocolCutover` 清除 pending。完成后：

- active YAML 的 `listeners` 只反映 R3 数据库；空节点时应为空列表；
- 旧 Listener 端口必须不再监听；
- Revision YAML、Revision JSON、数据库 active Revision 和 active YAML 的 SHA-256 必须一致；
- 旧 Revision 文件即使仍在磁盘，也不能出现在 API 历史或成为回滚目标。

在 baseline 和运行健康收敛完成前，普通 publication 返回 runtime-convergence 错误；不要
用设置保存、手工覆盖 `config.yaml` 或直接插入 `config_revisions` 绕过状态机。

## 4. `pending`、`ready` 与 `degraded` 的准确含义

以下状态来自实际持久化与健康检查边界：

- **cutover pending**：`runtime_convergence_state.r3_cutover_pending = 1` 且
  `pending_reason = 'r3_protocol_cutover'`。此时节点和历史已经清空，baseline 或运行健康尚未
  完成，普通 publication 被阻止，`/healthz` 与 `/api/v1/health` 返回 HTTP 503 和
  `status: not_ready`。
- **cutover ready**：`r3_cutover_pending = 0`、`pending_reason = ''`，baseline 仍是唯一
  active Revision，健康端点返回 HTTP 200 和 `status: ok`。这是运行收敛完成的机器事实；
  正式接流量前还必须通过本文的浏览器门禁。
- **degraded**：Publisher 无法证明或恢复数据库、Revision 制品和 active YAML 的一致性时写入
  `system_state` 的安全状态。cutover 重试只允许清理以 `r3_protocol_cutover:` 开头的自身
  degraded 原因；无关 degraded 会阻止 baseline/完成操作，绝不能被迁移顺带清除。

另外三种同名状态必须分开理解：

- `config_revisions.status = pending` 只属于一次尚未激活的 publication；
- `endpoint_settings_pending` 表示端点设置还需要 m-ui/Mihomo 重启，迁移时原样保留；
- `/run/m-ui/ready` 是当前 App 进程的瞬时启动锁，证明 native Mihomo 的启动顺序；只有
  convergence pending 已清除时，HTTP health 才证明 R3 运行面 ready。

## 5. 备份与旧版本恢复单元

当前标准 Docker 部署把所有持久状态放在 `/opt/m-ui/data`。拉取或第一次启动新镜像前执行
冷备；一旦先 `pull`，本地 `latest` 就不再能证明旧镜像身份：

```sh
cd /opt/m-ui
sudo docker image inspect ghcr.io/aethersailor/m-ui:latest \
  --format '{{json .RepoDigests}}' | \
  sudo tee pre-r3-image-digests.json > /dev/null
sudo docker image save -o m-ui-pre-r3-image.tar \
  ghcr.io/aethersailor/m-ui:latest
sudo gzip m-ui-pre-r3-image.tar
sudo docker compose down
sudo tar --numeric-owner -czf m-ui-pre-r3-data.tar.gz compose.yml data
sudo sha256sum pre-r3-image-digests.json m-ui-pre-r3-image.tar.gz \
  m-ui-pre-r3-data.tar.gz | sudo tee m-ui-pre-r3-SHA256SUMS > /dev/null
```

这三个文件共同构成恢复单元：旧镜像归档/摘要、旧 Release 的 Compose 或部署文件、包含
数据库、WAL、master key、Revision、核心和 Mihomo 配置的完整旧数据。任何一个缺失都不算
可接受的回滚证据。

恢复时必须先停止失败的新实例，将迁移后的数据整体移出运行路径，再从冷备恢复完整旧
数据，并加载旧镜像。例如在确认 `/opt/m-ui/data.failed-r3` 不存在后：

```sh
cd /opt/m-ui
sudo docker compose down
sha256sum -c m-ui-pre-r3-SHA256SUMS
sudo mv data data.failed-r3
sudo tar -xzf m-ui-pre-r3-data.tar.gz
gunzip -c m-ui-pre-r3-image.tar.gz | sudo docker image load
sudo docker compose up -d --pull never
```

**不得用旧镜像直接打开已经执行 `0006`/`0007`/`0008` 的 volume，也不得把旧数据库与
新 master key、Revision 或 YAML 拼接。** Compose 的正式发布文件虽然使用 `latest`，
灾难恢复必须锁定记录下来的旧 digest/本地镜像，并使用那个旧 Release 随附的部署文件。
详细 volume 形状见 [Docker 部署文档](../deploy/docker/README.md)。

## 6. 浏览器验收门禁

最终验收必须使用真实浏览器、真实 m-ui HTTP API、真实 SQLite、真实 Publisher 和真实
Mihomo；API mock 只能作为前端单元测试，不能签发上线结论。

### Fresh 数据目录

1. 用全新的空数据目录启动候选镜像，打开面板并在 Web 创建首位管理员；不得要求 SSH
   setup token。
2. 刷新和重新登录后确认 setup 不再出现，Capabilities 展示五种协议。
3. 等待自动 baseline 收敛并确认健康端点为 200；pending 期间健康端点不得返回 200
   （managed 模式通常在 HTTP bind 前完成，native 模式可能短暂返回 503）。Revision 1
   的 reason 必须为 `r3_protocol_cutover_baseline`，active YAML 的 `listeners` 为空。
4. 从 Web 创建一个有效节点和用户、启用节点，确认产生后续 active Revision、真实
   Listener 端口和分享内容；编辑后再次发布。
5. 重启容器，确认管理员、节点、Revision、活动 Listener 和 runtime health 均保持。
6. 桌面与移动宽度均完成上述主路径，浏览器 console 无 error/warning。

### v0.2.3 数据副本升级

1. 首次候选启动后轮询 health；pending 期间只能是未监听或 503 `not_ready`，自动 baseline
   和运行健康完成后必须为 200 `ok`。应用没有 setup 回退，原管理员、未过期 session、
   面板设置、endpoint pending/last-applied、审计与核心设置仍在。
2. 确认节点、用户和访问配置为空，Revision API 只包含新的 baseline；旧记录没有被错误
   “转换”。
3. 确认 baseline 是 reason 为 `r3_protocol_cutover_baseline` 的 Revision 1、convergence
   pending 已清除、active YAML 四方一致，旧 Listener 端口关闭且 Controller 仍只能通过
   配置的管理连接访问。
4. 仅通过 Web 重建至少一个节点/用户，执行保存、启用、分享、禁用与重启；全过程不直接
   编辑 SQLite/YAML。
5. 清除 cookie 后用原管理员密码重新登录；确认保留 session 不是唯一可用登录路径。

任一保留项丢失、旧 Listener 仍监听、baseline 不 active、出现 degraded、真实
`mihomo -t`/健康检查失败、或只能依靠 mock/SSH 完成日常路径，均为 **no-go**。此时停止
候选实例并按完整旧版本恢复单元回滚；不得进入 R4。
