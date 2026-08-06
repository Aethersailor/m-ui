# 3x-ui 功能对齐矩阵与实现基线

## 三条不可偏离的产品原则

1. **产品能力基准是 3x-ui。** 节点管理、用户运营、订阅、运行观测、备份恢复、
   出站路由和多主机等能力是否值得建设，优先参考 3x-ui 的完整产品面，而不是把
   m-ui 永久限制成一个表单生成器。
2. **参数与可行性真相是 Mihomo `Meta` 分支源码。** 不从 Mihomo 默认分支、3x-ui
   的 Xray JSON 或其他面板字段反推 Mihomo 参数；3x-ui 有但当前 Mihomo 没有可靠
   内核契约的能力，必须明确降级、改为面板侧实现或标记暂不可测。
3. **扩展架构是协议、传输/处理器与安全层的受约束组合。** 新协议通过领域模型、
   `protocol.Module`、能力 Schema、编译/分享实现和契约测试接入；不能重新退化为
   一个协议一套页面、在共享页面中堆协议判断。

证书不是产品主线。证书链、私钥、校验和续期只是 TLS 类节点共享的横向依赖；
仅在它能解除节点配置的 SSH 文件路径依赖时随对应协议交付，完整 ACME 自动化后置。

## 固定调研快照

本矩阵不是凭印象编写。后续每轮开始前必须刷新调研日期和两个提交，发现上游变化时
先更新矩阵，再决定是否调整实现。

| 项目 | 调研日期 | 分支与提交 | 本轮直接证据 |
|---|---|---|---|
| 3x-ui | 2026-08-06 | `main` @ `3883882726b708e8b3984b14152db8595d628c01` | [`README.md`](https://github.com/MHSanaei/3x-ui/blob/3883882726b708e8b3984b14152db8595d628c01/README.md)、[`docs/architecture.md`](https://github.com/MHSanaei/3x-ui/blob/3883882726b708e8b3984b14152db8595d628c01/docs/architecture.md)、`docs/content/docs/en/config/{subscription,ssl-certificates}.mdx`、`docs/content/docs/en/operations/backup-restore.mdx`、`internal/web/service/client_bulk.go`、`internal/sub/`、`internal/web/service/node.go`、`internal/database/dump_sqlite.go` |
| Mihomo | 2026-08-06 | `Meta` @ `e26714a181ac0e2fa803453c0a8e9a9ce94e31cb` | [`listener/parse.go`](https://github.com/MetaCubeX/mihomo/blob/e26714a181ac0e2fa803453c0a8e9a9ce94e31cb/listener/parse.go)、[`listener/config/`](https://github.com/MetaCubeX/mihomo/tree/e26714a181ac0e2fa803453c0a8e9a9ce94e31cb/listener/config)、`listener/inbound/` |

3x-ui 当前 `README.md` 明确列出多协议入站、现代传输与安全层、按用户运营、分享与
订阅、入站/用户/出站流量、多节点、出站路由、API 和通知等产品能力；备份恢复由
`internal/database/dump_sqlite.go` 和面板入口实现。其 `docs/architecture.md` 进一步
给出订阅服务、批量用户服务、流量任务和本地/远端 Runtime 的实际调用链。本矩阵只
把这些当作**产品需求候选**，不会把 Xray 专属实现照搬到 Mihomo。

Mihomo 的 `listener/parse.go` 是服务端 Listener 类型入口；每种类型的真实字段继续
下钻 `listener/config/*.go` 与 `listener/inbound/*.go`。当前 m-ui 的能力清单同时通过
`internal/domain/model.go` 中的 `MihomoSourceCommit` 和 `/api/v1/capabilities` 暴露该
Meta 提交，避免文档、表单和编译器各自漂移。

## 协议与 Listener 覆盖

状态说明：“当前”按本文件调研时共享工作区代码判断；“目标轮次”不是承诺 Mihomo
具备 3x-ui/Xray 的全部语义，实际字段仍须经过对应 Meta 源文件、`mihomo -t` 和真实
握手验证。

| 能力 | 3x-ui 产品基准 | m-ui 当前状态 | Mihomo `Meta` 可行性与约束 | 目标轮次 |
|---|---|---|---|---|
| VLESS | 支持多用户、分享、常见传输和 TLS/REALITY | 已覆盖；raw、WebSocket、gRPC、XHTTP 和多种安全层已进入 Schema | `listener/config/vless.go`、`listener/inbound/vless.go` 有直接 Listener 契约 | 已有；持续补参数 |
| Hysteria2 | 支持入站与用户运营 | 已覆盖 TLS、混淆、带宽、QUIC 和 Realm 参数 | `listener/config/hysteria2.go` 与 `hysteria2_realm.go`；用户名到密码映射可表达多用户 | 已有；持续补参数 |
| VMess | 3x-ui 基础协议之一 | **R3 已完成**：多用户、raw、WebSocket、gRPC、mKCP、Mux、分享 URI 和客户端 YAML | `listener/config/vmess.go` 提供 `Users`、WS、gRPC、mKCP、证书及多种安全字段 | 已有；持续补参数 |
| VMess Mekya / TLSMirror | 3x-ui 当前主功能不要求这两个 Meta 增强传输 | 本轮未实现，能力 Schema 明确标记源码仅部分覆盖 | `listener/config/vmess.go` 有对应字段，但服务端、客户端及互操作验收仍需独立完成，不能因 VMess 已支持而宣称同步支持 | 后续独立验收 |
| Trojan | 3x-ui 基础协议之一 | **R3 已完成**：多用户、raw、WebSocket、gRPC、TLS/REALITY 等安全组合、Trojan-Go Shadowsocks 包装、分享 URI 和客户端 YAML | `listener/config/trojan.go` 提供多用户、WS、gRPC、TLS/REALITY 等字段 | 已有；持续补参数 |
| Shadowsocks | 3x-ui 基础协议之一 | **R3 已完成**：严格单有效用户、传统 AEAD 与 Shadowsocks 2022、UDP、Mux、simple-obfs、分享 URI 和客户端 YAML | `listener/config/shadowsocks.go` 只有 Listener 级 `Password`/`Cipher`，因此强制恰好一个有效凭据；2022 密钥按算法校验 Base64 解码长度，不伪装成多用户 | 已有；单凭据语义 |
| HTTP / SOCKS / Mixed | 3x-ui 支持 HTTP 与 SOCKS(Mixed) | 未支持 | `listener/parse.go` 有直接入口，认证由 `listener/config/auth.go` 承载；公网暴露前需单独安全设计 | R5 |
| Tunnel / TUN | 3x-ui 支持 Dokodemo/Tunnel 与 TUN | 未支持 | Meta 有 `tunnel`/`tun`，但它们是系统接入而非“用户分享节点”；需要独立用途模型和宿主权限验收 | R6 |
| WireGuard | 3x-ui/Xray 能力 | 未支持 | 当前 `listener/parse.go` 没有 WireGuard Listener 类型，不能制造同名假对齐 | 暂不可直接对齐 |
| TUIC / AnyTLS | 不属于当前 3x-ui README 主协议列表 | 未支持 | Meta 有直接 Listener 和配置结构；应在 3x-ui 交集协议稳定后作为 Mihomo 增强能力接入 | R5 |
| Snell / ShadowQUIC / Mieru / Sudoku / TrustTunnel | 不属于当前 3x-ui README 主协议列表 | 未支持 | 当前 `listener/parse.go` 有入口，成熟度和客户端分享格式须逐项验证 | R7+ 候选 |
| TProxy / Redir | 不是 3x-ui 面向最终用户的分享节点主线 | 未支持 | Meta 有直接入口，但属于透明代理/系统网络接入，不能复用普通节点用户模型 | R7+ 候选 |

## 面板功能覆盖

| 功能域 | 3x-ui 产品基准 | m-ui 当前状态 | Mihomo 可行性/边界 | 优先轮次 |
|---|---|---|---|---|
| 节点 CRUD 与筛选 | 入站搜索、创建、编辑、启停 | 已有单节点 CRUD、搜索/筛选、启停 | 面板侧能力；所有变更继续走唯一 Publisher | 已有 |
| 节点复制与批量操作 | 丰富的批量管理 | **R3 已完成**：复制、批量启用/禁用；副本要求新名称/端口、默认禁用并显式选择是否复制用户 | 面板侧实现；所有批量变更仍走一个服务事务和一次 Publisher 发布 | 已有 |
| 通用协议表单 | 3x-ui 为各协议提供完整编辑体验 | **R3 已完成收口**：协议标识为开放的注册表数据，协议选项、默认值和字段均来自 Schema | Meta 字段进入能力声明；复杂操作允许显式扩展组件，普通协议不得修改共享页面判断 | 已有；持续守门 |
| 单用户生命周期 | 到期、启停、凭据和分享 | 已有创建/修改/删除、启停、到期和单用户分享 | 到期由面板调度；凭据形状必须按协议定义 | 已有 |
| 批量用户运营 | 批量创建、调整、启停等 | **R3 已完成第一批**：批量创建、启用和禁用；延期、删除、凭据轮换仍属 R4 | 面板侧实现；当前操作单事务、单次发布，任一非法目标使整批失败 | 已有；R4 继续扩展 |
| 永久订阅地址 | 内建独立订阅服务，多种输出格式和模板 | 只有登录后按节点/用户生成分享 URI、二维码和单份客户端 YAML | 面板侧可实现可撤销令牌；不要把现有管理员 API 临时分享响应称为订阅服务 | R4 |
| 配额、重置周期 | 按客户端配额、到期和周期重置 | 仅有到期策略；没有配额模型 | 可先实现面板策略字段；没有可靠内核归因时不能宣称已执行按用户流量配额 | R5，取决于归因证据 |
| 每用户流量与在线状态 | 3x-ui 基于 Xray API 提供用户级统计、IP 和在线状态 | 仅有 Mihomo 实例级流量、内存和连接观测 | 当前固定 Meta Listener/Controller 契约尚无 m-ui 可依赖的每用户归因证据；必须显示“不可测”，不得按比例估算或用于计费 | 研究项，不设虚假交付日期 |
| 入站/实例流量 | 入站和出站统计与重置 | 已有实例级 Controller 快照 | 可继续增加节点级聚合，但必须用真实 Listener/连接标识验证归因 | R5 |
| 草稿、差异与发布 | 3x-ui 强调易用操作；m-ui 另有更强事务发布边界 | 已有预览、校验、Revision 和回滚，但编辑通常直接进入发布事务 | 完全由面板侧实现；不能绕过现有 Publisher | R4 |
| TLS 证书管理 | 3x-ui 可配置面板证书并提供证书获取入口 | 节点目前直接填写 Meta 的证书/私钥字段（通常是宿主机文件路径）；没有复用资产、Web 物化或 ACME | Meta TLS Listener 接受证书/私钥路径；资产引用、加密保存和原子物化可行 | 随 R3/R4 TLS 协议按需交付；ACME R6+ |
| 数据库备份/恢复 | 面板内导入导出数据库 | 文档提供冷备命令，Web 尚无完整备份恢复闭环 | 面板侧可实现；数据库与 `master.key` 必须作为一致性集合，恢复必须停写并重新验证 | R6 |
| 核心与运行维护 | 系统状态、日志、核心控制 | 已有 Mihomo 状态、脱敏日志、重启、校验、更新和回滚 | 已具备受限运行适配器；继续坚持日常零 SSH | 已有；持续增强 |
| 出站与路由 | 自定义路由、负载均衡、出站链路及 WARP 等 | 尚未作为结构化产品能力提供 | Mihomo 本身有出站/规则能力，但需另行以 Meta `config/` 源码建模；不能塞进 Listener Schema | R6 |
| 多主机控制 | 3x-ui 通过 Runtime 抽象管理远端节点 | 当前单机单 Mihomo | 面板侧可行，但需要主机身份、证书信任、离线收敛、权限和审计模型；与本项目“节点=Listener”术语必须消歧 | R7 |
| 通知与自动化 | Telegram 等状态、到期、流量和备份通知 | 尚无 | 面板侧可行；先建立领域事件与通知 Provider，不把 Telegram 写进核心业务 | R7 |
| 管理 API | 3x-ui 有 REST API 与 Swagger | 已有同源 `/api/v1`，但未作为公开稳定集成契约 | 面板侧可行；公开前需要版本、权限、速率和 OpenAPI 契约 | R6 |

## R3 本轮完成范围

状态：**已完成（2026-08-06）**。

### R3.1 协议覆盖扩张

- VMess、Trojan、Shadowsocks 已有独立领域规格、校验器和 `protocol.Module`；
- 同一模块负责服务端 YAML、客户端 YAML、分享 URI 和能力 Schema；
- VMess 已覆盖 raw、WebSocket、gRPC 与 mKCP；Mekya、TLSMirror 明确留待后续；
- Shadowsocks 明确使用 Meta 的单 Listener/单有效凭据语义，并覆盖 2022 密钥校验与
  simple-obfs，不伪造多用户支持；
- 前端协议选择来自 `/api/v1/capabilities`，共享节点页面没有新增五协议枚举判断。

### R3.2 通用节点与用户操作

- 节点复制要求新名称和新端口，默认禁用，可显式选择是否复制用户；
- 节点批量启用/禁用和用户批量创建/启用/禁用经过统一服务层；
- 一次批量操作使用一个受控事务与一次安全发布，任一目标无效时整批失败；
- API、审计、Web 桌面/移动端和冲突/回滚测试同步交付。

### R3 验收证据

- Go 全量测试、race detector、`go vet`、`webembed` 构建标签测试，以及 Web 单元测试、
  Lint、类型检查和生产构建均已通过；
- Debian WSL 使用 Mihomo Meta `v1.19.29`：由同一结构化状态生成的
  `vmess-raw`、`vmess-mkcp`、`trojan-tls`、`ss-2022`、`ss-simple-obfs` 五个服务端
  Listener，以及各自生成的五份客户端 YAML，全部通过真实 `mihomo -t`；
- 同一真实核心分别完成 VMess + mKCP、Trojan + TLS、Shadowsocks 2022 的服务端与
  客户端进程启动，并通过本地 HTTP 代理完成请求和响应数据传输；
- Playwright 以 1440 px 桌面宽度和 390 px 移动宽度走通三种新增协议展示、节点复制、
  节点批量操作、用户批量操作和 VMess mKCP 编辑路径，控制台均为零 error、零 warning。

TLS 证书输入可以为 R3 协议提供最小可用支撑，但“证书资产中心”不再是 R3 的独立
里程碑，也不能阻塞无 TLS 依赖的协议和节点操作。

## 每轮启动和结束门禁

每一轮计划必须回答以下问题，否则不能进入实施：

1. 本轮对应 3x-ui 哪个已证实的用户能力，证据文件和固定 SHA 是什么？
2. 相关参数在 Mihomo `Meta` 哪个源文件，固定 SHA 是什么？
3. 3x-ui 能力若依赖 Xray 专属 API，m-ui 的真实降级语义是什么？
4. 新能力是否通过协议/传输/安全组合边界接入，还是又把协议写死进共享页面？
5. 是否完成 API、数据库、Web、编译、分享、真实 `mihomo -t` 和必要握手的闭环？
6. 无法可靠观测的值是否明确显示“不可测”，而不是估算、伪造或用于计费？

每轮结束后更新本矩阵的“当前状态”和证据；规划项不得提前写成已支持功能。
