# 第二轮成果与下一轮路线：对齐 3x-ui，落地 Mihomo Meta

> 调研与优先级以 [3x-ui 功能对齐矩阵](feature-parity-matrix.md)为准。产品能力参考
> 3x-ui，参数和可行性只认 Mihomo `Meta` 分支源码，架构坚持协议、传输/处理器与
> 安全层的受约束组合。

## 目标

第二轮先把 Node V2 建成可持续扩展的产品能力：

- 新协议、传输层或安全层通过能力声明接入，不复制整套 API 和表单；
- 后续协议、节点运营、发布状态和用户订阅能够在同一能力边界继续扩展；
- 每次变更都能明确区分草稿、已校验、已发布和运行失败；
- 继续保留 Publisher 的原子发布、健康检查和自动回滚边界。

## 已完成的第二轮基础

### R2.1 结构化能力 Schema

状态：已完成。

- `/api/v1/capabilities` 返回独立的能力 Schema 版本和 Node Schema 版本；
- 描述协议的组合层、组件、默认值、字段类型、Meta 源字段、敏感属性和依赖；
- 节点编辑器从能力契约读取协议、传输层、安全层选项和默认配置；
- 后端测试阻止重复组件、无效默认 JSON 和未标记的敏感字段。

### R2.2 Schema 驱动的节点编辑器

状态：已完成。

- 将巨型节点页面拆成通用字段渲染器和少量复杂字段组件；
- 支持 string、text、secret、boolean、integer、string-list、object-list 和 record；
- 基础/高级参数分级显示；
- 组合选择遵守 `requires`、`conflicts` 和 layer 约束；
- 新增简单协议组件时不再修改节点主页面。

实现结果：能力 Schema v3 已覆盖条件显示、复杂子项和 record 键名；VLESS
传输层、安全层、Mux 以及 Hysteria2 的 TLS、QUIC、Realm 均复用通用渲染器。
锁定层、单选/多选层、`requires` 和 `conflicts` 在后端契约校验与前端选择器中
使用同一组 `group:kind` 组件标识。

## R3：协议覆盖扩张与通用节点管理

状态：**已完成（2026-08-06）**。

### R3.1 VMess、Trojan 与 Shadowsocks

状态：已完成。

- 按固定 Mihomo Meta 源码分别完成领域规格、校验、加密字段和协议模块；
- 同一模块输出服务端 Listener、客户端 YAML、分享 URI 和能力 Schema；
- 前端协议选项完全来自 `/api/v1/capabilities`，普通新协议不修改共享页面判断；
- VMess 已覆盖 raw、WebSocket、gRPC 和 mKCP；Mekya、TLSMirror 保留为后续独立验收；
- Trojan 保留 Meta 的多用户、传输和安全组合语义；
- Shadowsocks 按 Meta 单 Listener `password`/`cipher` 语义强制恰好一个有效用户，
  覆盖传统 AEAD、Shadowsocks 2022 密钥规则和 simple-obfs，不伪造每用户统计。

### R3.2 节点与用户批量操作

状态：已完成。

- 复制节点时必须提供新名称和新端口，副本默认禁用，可选择是否复制用户；
- 批量启用/禁用节点；
- 批量创建、启用和禁用用户；
- 批量变更走统一服务层、审计和 Publisher，不新增旁路写入；
- API 与桌面/移动 Web 操作保持一致，并覆盖冲突与回滚测试。

### R3 验收结果

- Go 全量测试、race detector、`go vet`、`webembed` 构建标签测试，以及 Web 单元测试、
  Lint、类型检查和生产构建通过；
- Debian WSL 上使用 Mihomo Meta `v1.19.29`，验证五 Listener 服务端配置及其五份
  客户端配置全部通过真实 `mihomo -t`；覆盖 VMess raw/mKCP、Trojan TLS、
  Shadowsocks 2022 和 Shadowsocks simple-obfs；
- VMess + mKCP、Trojan + TLS、Shadowsocks 2022 均启动真实服务端和客户端核心，
  通过 HTTP 代理完成实际请求/响应数据传输；
- Playwright 在 1440 px 与 390 px 两种宽度完成三种新增协议展示、节点复制、节点批量
  操作、用户批量操作和 VMess mKCP 编辑路径，控制台零 error、零 warning。

## R4：用户订阅与发布闭环

### 草稿与发布状态

- 节点编辑先形成草稿，不立即改变运行配置；
- 支持结构化差异和脱敏 YAML 差异预览；
- 明确展示未保存、待校验、待发布、已生效和失败状态；
- 发布继续复用现有 Publisher，不引入第二条不安全写入路径；
- 节点页面可查看最后生效 Revision 和失败原因。

### 用户与订阅运营

- 继续增加批量延期、删除和重新生成凭据；
- 可撤销的分享令牌和单用户订阅地址；
- 配额、重置周期、累计用量和到期策略使用独立领域模型；
- 在 Mihomo 无法提供可靠用户归因时明确标记“不可测”，不伪造统计。

## 横向依赖：TLS 证书管理

TLS 证书管理不再单独占据下一轮。它随真正需要 TLS 文件的协议逐步交付：

- Web 上传或粘贴证书链与私钥，校验匹配、SAN、用途和有效期；
- 私钥加密保存，证书文件原子物化到 m-ui 管理目录；
- 节点通过证书引用解除日常 SSH 文件操作；
- ACME Provider 和自动续期在节点/订阅主线稳定后进入后续轮次。

证书能力的优先级由它所解锁的节点功能决定，不能再次成为脱离 3x-ui 功能对齐的
独立产品主线。

## 所有轮次的真实运行验收

- 所有组合继续执行真实 `mihomo -t`；
- 增加协议握手和实际数据传输测试；
- 覆盖 Reload、新旧连接、端口占用、证书失效和自动回滚；
- 完成 Hysteria2 Realm 控制服务联调；
- 浏览器验证桌面和移动端的完整管理员路径。

## 后续顺序

组合 Schema 和通用编辑器已经完成，不再以“基础尚未完成”为理由延后协议覆盖。
R3 之后按对齐矩阵推进：

- R4：可撤销订阅、批量用户生命周期、草稿/差异/发布状态；
- R5：TUIC/AnyTLS 候选、节点级可验证观测，以及有证据支撑的配额策略；
- R6：备份恢复、出站路由、结构化管理 API、Tunnel/TUN 和按需 ACME；
- R7：多主机控制、通知 Provider 和经成熟度验证的 Meta 增强 Listener。

多主机、出站路由等顺序可随用户价值调整，但任何一轮都必须说明对应的 3x-ui 能力、
Meta 源码契约和真实降级边界。未经 Meta 源码与真实内核验证的“兼容”选项、伪造的
每用户流量数据和通用 YAML 旁路不得进入实现。
