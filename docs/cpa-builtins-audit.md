# CPA 内置能力清单：插件已用 / 未用

> 基线：CPA **v7.3.12** / workbuddy **0.9.37**
>
> **0.9.37 已接入推荐的三项**（`host.auth.get_runtime`、
> `RequestLifecyclePlugin`、`host.log`）。下方 §三/§五 的"未用"标注已相应更新；
> 接入实现与真机验证结果见 §八。
> 用途：盘点 CPA 宿主提供给插件的**全部**内置能力，标注插件的使用状态，
> 供判断"哪些还值得接"。
> 数据来源：`sdk/pluginapi/types.go`（Capabilities）+ `sdk/pluginabi/types.go`（method 表），
> 逐项对照插件源码核过。

---

## 一、总览

CPA 通过两个面暴露能力：

| 面 | 数量 | 含义 |
|---|---|---|
| **Capabilities**（插件声明） | 27 项 | 插件能"对外提供"什么，宿主据此调用插件 |
| **pluginabi method**（RPC） | 58 个 | 完整协议面，含插件侧 method 与宿主回调 `host.*` |

workbuddy 当前声明 **11 项**能力（+1 项显式关闭），实现 **25 个** RPC method，调用 **10 个**宿主回调。

---

## 二、Capabilities：插件已用的 10 项

| 能力 | 插件值 | 实现文件 | 说明 |
|---|---|---|---|
| `ModelProvider` | true | `models.go` | 静态 + 动态模型目录、别名反解、excluded 过滤 |
| `AuthProvider` | true | `oauth.go`, `authfile.go` | OAuth 登录、token 刷新、auth 文件解析 |
| `Executor` | true | `main.go`, `stream.go` | 聊天执行（流式 + 非流式 + count_tokens） |
| `ExecutorModelScope` | `OAuth` | — | 只为 OAuth 账号供模型 |
| `ExecutorInputFormats` | `["chat-completions"]` | — | 只接 openai 格式 |
| `ExecutorOutputFormats` | `["chat-completions"]` | — | 只吐 openai 格式（**决定了 count_tokens 线形**） |
| `Scheduler` | true | `scheduler.go` | 默认委托内置；可选插件自选 |
| `ManagementAPI` | true | `management.go` 等 | 19 路由 + 2 面板资源 |
| `QuotaProvider` | true | `quota.go`, `billing.go` | 付费积分原生额度 |
| `UsagePlugin` | true | `daily_quota.go` | 每日免费额度统计 |
| `RequestLifecyclePlugin` | true | `request_lifecycle.go` | 请求终态事件（补 usage 覆盖不到的盲区） |

（`FrontendAuthProvider` 显式声明 `false`。）

---

## 三、Capabilities：**未用**的 16 项 —— 按价值分组

> A 组三项已于 0.9.37 接入，见 §八。

### 🟢 A 组：值得考虑接入（与现有功能互补）

#### A1. `RequestLifecyclePlugin` — 请求终态事件

```go
HandleRequestComplete(context.Context, RequestCompletion) error
```

**作用**：每个走到请求拦截阶段的请求，**终结时**（成功/失败/拒绝/取消）异步收到一个事件。

**与现有 `UsagePlugin` 的区别**（这是关键）：

| | `UsagePlugin`（已用） | `RequestLifecyclePlugin`（未用） |
|---|---|---|
| 触发时机 | 执行器返回后 | 请求**完全终结**时 |
| 覆盖范围 | 只覆盖本插件执行器处理的请求 | 覆盖**所有** provider 的请求 |
| 字段 | tokens / latency / TTFT | 终态 + 取消原因 + 拒绝原因 |
| 用途 | 用量统计 | 审计、失败归因、取消追踪 |

**对 workbuddy 的价值**：可以区分"被 CPA 拒绝（无可用账号）"和"上游失败" ——
现在插件只能看到自己执行器收到的请求，看不到**因为账号全被禁用而根本没到执行器**的请求。
后者恰恰是"额度耗尽"最直接的信号，但当前架构下插件看不见。

**代价**：低。已有一个 `usage.handle` 的接收模式可复用。

**建议**：⭐⭐⭐ 值得做。能补上"账号全禁用导致请求未到达"这个观测盲区。

---

#### A2. `CommandLinePlugin` — 插件自有命令行参数

```go
// 声明并处理插件自己的 CLI flag
```

**作用**：插件可以在 CPA 启动命令行上注册自己的 flag。

**对 workbuddy 的价值**：目前所有配置都走 YAML（`plugins.configs.workbuddy.*`）。
若加上 CLI，可以做 `--workbuddy-region=intl` 这类启动期覆盖，便于容器化部署
（环境变量 → CLI 参数比改 YAML 更自然）。

**代价**：低-中。需要定义 flag 集合与优先级（CLI vs YAML 谁赢）。

**建议**：⭐⭐ 看部署方式。若你们用容器编排且不想挂 YAML，价值上升。

---

#### A3. `SchedulerAcrossPriorities` — 跨优先级候选

```go
SchedulerAcrossPriorities bool
```

**作用**：默认情况下，插件的 `scheduler.pick` **只收到最高优先级层的候选**。
打开后，`Candidates` 包含**所有优先级层**的凭据。

**对 workbuddy 的价值**：`scheduler_mode: credits` 模式下，插件当前只能在
"最高优先级层"里挑。如果运维用 CPA 的 priority 字段给账号分层（例如"主号优先、
备用号兜底"），插件看不到备用号 —— 主号耗尽时只能回退到内置调度。
打开这个开关后，插件可以在跨层候选里按积分挑，实现"主号没了自动用备用号"。

**代价**：低（一个 bool），但要改 `pickActiveAuth` 的选择逻辑并测试。

**建议**：⭐⭐ 仅在用 CPA priority 分层时才有价值。

---

### 🟡 B 组：理论可用，但与本插件场景不符

| 能力 | 作用 | 为什么 workbuddy 不需要 |
|---|---|---|
| `ModelRouter` | 请求按模型路由到指定插件执行器 | 插件只有一个上游，无需按模型分流。**注意**：这是 `countWithPluginExecutor` 的唯一入口，但 workbuddy 走 auth manager 路径已能到达 count_tokens，不需要它 |
| `RequestTranslator` | 把规范请求转成 provider 专用格式 | 插件在 `payload.go` 里自己做了（forceStream/normalizeTools/rewriteSystem），且需要单次 marshal 优化。改用宿主接口会多一次序列化 |
| `RequestNormalizer` | provider 请求 → 规范格式 | 同上，方向相反，插件不需要 |
| `ResponseTranslator` | 规范响应 → provider 格式 | 插件直接吐 openai 格式，由宿主翻译给其他协议客户端 |
| `ResponseBeforeTranslator` | 上游响应在原生翻译**前**归一化 | 插件在 `aggregateCompletion`/`pumpUpstreamStream` 里已处理 |
| `ResponseAfterTranslator` | 翻译**后**、下发前归一化 | 同上 |
| `ThinkingApplier` | 应用 thinking 配置到 payload | 上游 CodeBuddy 的 reasoning 由模型自身控制，插件不注入 thinking 参数 |
| `WebSocketResponseObserver` | 观察上游 WebSocket 响应事件 | 上游是 HTTP SSE，无 WebSocket |
| `FrontendAuthProvider` | 代理处理**前**认证前端请求 | 插件用的是 CPA 自己的 api-key 认证，不需要插件层认证 |
| `FrontendAuthProviderExclusive` | 让插件成为唯一前端认证方 | 同上 |

---

### 🔴 C 组：明确不适用

| 能力 | 作用 | 不适用原因 |
|---|---|---|
| `ModelRegistrar` | 贡献**开发期**模型元数据到宿主 registry | 这是给 CPA 官方模型库用的开发期机制；插件用 `ModelProvider` 的运行时目录 |
| `RequestInterceptor` | 在凭据选择**前后**改写请求 | 这是**横切**能力（如全局注入 header/改写模型），属于运维策略插件（如 account-concurrency）的领域，不是 provider 插件该做的 |
| `ResponseInterceptor` | 改写成功响应（非流式） | 同上 |
| `StreamChunkInterceptor` | 改写成功流式 chunk | 同上 |

> C 组不是"能力不够"，而是**职责边界**：provider 插件负责"把某个上游接进来"，
> 横切改写属于独立插件。混在一起会让 provider 插件变得不可预测。

---

## 四、宿主回调（`host.*`）：已用 9 个

| 回调 | 用途 | 使用处 |
|---|---|---|
| `host.auth.list` | 列出本 provider 凭据 | `host_auth.go` |
| `host.auth.get` | 读单个凭据（含物理文件名） | `host_auth.go` |
| `host.auth.save` | 持久化凭据 JSON | `authfile.go` |
| `host.http.do` | 宿主 HTTP（请求日志可捕获） | `host_bridge.go` |
| `host.http.do_stream` | 宿主流式 HTTP | `host_bridge.go` |
| `host.http.stream_read` | 读流式响应块 | `host_bridge.go` |
| `host.http.stream_close` | 关流 | `host_bridge.go` |
| `host.stream.emit` | 向客户端推流块 | `stream.go` |
| `host.stream.close` | 关闭客户端流 | `stream.go` |

---

## 五、宿主回调：**未用**的 8 个

### 🟢 值得考虑

#### 5.1 `host.auth.get_runtime` — 凭据**运行时**状态

**作用**：按 `auth_index` 取凭据的**运行时**信息，比 `host.auth.get` 多出：

```
Status / StatusMessage     当前状态与原因
Unavailable                是否不可用（冷却中）
NextRetryAfter             下次可重试时间
Priority                   优先级
Success / Failed           成功/失败计数
RecentRequests             最近请求记录
Email / ProjectID / AccountType / Account / Note / BaseURL / Websockets
LastRefresh / UpdatedAt / CreatedAt / ModTime / Path / Size
```

**对 workbuddy 的价值**：**这是最实用的一项**。当前插件自己维护：
- 面板的"耗尽/禁用"判断（用 `cachedCreditsScore` + 本地缓存）
- 生命周期状态（`lifecycleState`）
- 活跃账号粘性（`activeAuthID`）

但这些**都是插件自己的视图**，与 CPA 的真实调度状态可能不一致。
接了 `get_runtime` 后，面板能显示 CPA 侧的真实状态：冷却到几点、是否被熔断、
最近请求成功率。**能直接解决"面板显示的和我实际被路由到的不一致"这类困惑。**

**代价**：低。已有 `hostAuthGetBundle` 模式，加一个方法即可。

**建议**：⭐⭐⭐ 强烈建议。

#### 5.2 `host.model.execute` / `host.model.execute_stream`

**作用**：插件可以**反向调用宿主**执行一次模型请求（走 CPA 的完整路由/凭据选择/翻译链）。

**对 workbuddy 的价值**：可以做"用某个模型给自己的请求做预处理"（如自动摘要、
意图分类）。但这是**产品功能**，不是链路完整性需求。

**代价**：中（要设计用途、错误处理、递归保护 —— 插件调宿主再路由回插件会造成循环）。

**建议**：⭐ 除非有明确产品需求，否则不接。**注意递归风险**。

#### 5.3 `host.log`

**作用**：插件日志走宿主日志管道（带 plugin_id 标记、统一级别控制）。

**对 workbuddy 的价值**：插件现在用标准 `log.Printf`，输出会混在宿主 stdout 里，
没有级别控制、不能按插件过滤。接了之后 CPA 的日志管理界面能单独看插件日志。

**代价**：极低。

**建议**：⭐⭐ 低垂果实，纯粹的可观测性改善。

#### 5.4 `host.affinity.lookup`

**作用**：查询会话亲和性（同一会话粘到同一凭据）。

**对 workbuddy 的价值**：上游是无状态聊天，但**同一账号连续服务同一会话**能提高
上游 prompt cache 命中率，间接省 token（对每日免费额度是实打实的收益）。

**代价**：中（要理解 CPA 的 affinity 语义，并验证与 `scheduler_mode` 的交互）。

**建议**：⭐⭐ 若上游确有 prompt cache 收益则值得。

### 🟡 / 🔴 不适用

| 回调 | 不适用原因 |
|---|---|
| `host.model.stream_read` / `stream_close` | 只配合 `host.model.execute_stream` 使用 |
| `host.auth.get_runtime` 已列在 🟢 | — |
| `command_line.register` | 配合 `CommandLinePlugin`，见 A2 |

---

## 六、优先级建议汇总

| 优先级 | 项目 | 收益 | 代价 |
|---|---|---|---|
| 🥇 **1** | `host.auth.get_runtime` | 面板显示 CPA 真实状态（冷却/熔断/成功率），消除视图不一致 | 低 |
| 🥈 **2** | `RequestLifecyclePlugin` | 补上"请求未到达执行器"的观测盲区 | 低 |
| 🥉 **3** | `host.log` | 日志可分级、可按插件过滤 | 极低 |
| 4 | `SchedulerAcrossPriorities` | 仅在用 CPA priority 分层时有价值 | 低 |
| 5 | `host.affinity.lookup` | 可能提升上游 cache 命中、省额度 | 中 |
| 6 | `CommandLinePlugin` | 容器化部署便利 | 低-中 |
| 7 | `host.model.execute` | 产品功能，有递归风险 | 中 |

**1 和 3 我建议直接做** —— 代价低、收益明确，且都是"消除插件与宿主之间的信息不一致"，
这正是过去几轮 bug 的共同根因（账号名退化、count_tokens 线形、额度来源标签，
全都是插件对宿主行为的假设出错）。

---

## 七、一句话总结

CPA 给了 27 项能力，workbuddy 用了 10 项。**未用的 17 项里，真正值得接的是 3 项**
（`host.auth.get_runtime`、`RequestLifecyclePlugin`、`host.log`），
其余要么职责不属于 provider 插件（拦截器类），要么上游场景不支持（WebSocket、
thinking），要么是产品功能而非链路需求。

**最该做的是 `host.auth.get_runtime`**：它能让插件停止维护自己那套可能与宿主
不一致的状态视图，从根上消除一类 bug。

---

## 八、接入实现与真机验证（0.9.37）

三项均已接入并在真实 CPA v7.3.12 宿主上验证。

### 8.1 `host.auth.get_runtime`

- `host_auth.go` 新增 `hostAuthGetRuntime` / `hostAuthRuntime`；
  `panel.go` 的 `hostAuthRuntimeFor` 适配为面板字段（`runtime`）。
- 面板新增「宿主调度状态」区块：可用/冷却中 + 剩余时间、状态、优先级、
  成功/失败计数。
- **降级**：宿主不支持该 RPC 或凭据已删除时返回 `nil`（不是错误），
  面板退回只显示插件侧状态，不空白。
- **一个关键推导**：`NextRetryAfter` 在未来即视为不可用 —— 即便宿主尚未置
  `unavailable` 布尔位（两个字段在冷却路径的不同阶段写入，以截止时间为准更安全）。

真机结果：`{"status": "active"}`（来自宿主的真实状态）。

### 8.2 `RequestLifecyclePlugin`

- `request_lifecycle.go` 接收 `request.complete`，按 1 小时窗口统计
  succeeded/failed/rejected/canceled，保留最近 50 条**非成功**事件。
- 面板「近期请求」行 + 连续失败告警。

**⚠️ 实测边界（与最初假设不同，已按实测修正）**：

我在接入前断言这能捕获"所有凭据不可用"的拒绝。**实测发现只对了一半**：

| 场景 | 是否产生事件 | 原因 |
|---|---|---|
| 模型无人认识 | ❌ | tracker 在 `providersForExecution` **之后**创建，请求没走到 |
| provider 已知但无可用凭据 | ✅ `failed` | `AuthManager.Execute` 在 tracker 之后 |
| 拦截器终止请求 | ✅ `rejected` | `msg.DirectResponse` |

关键修正：**`rejected` 不是"凭据耗尽"信号** —— 宿主只在**拦截器**终止请求时
发出它（那是插件策略决定）。凭据不可用产生的是 `failed`。

因此统计的是"连续未成功"而非"连续被拒"，且不做原因归类 ——
宿主给什么错误文本就显示什么，**猜类别正是这个插件过去几轮 bug 的根因**。

真机结果：6 个失败请求后 `counts:{failed:6}, fail_streak:6, all_failing:true`；
告警在**恰好第 5 次**触发一次（不是每次），走 `host.log` 的 warn 级。

### 8.3 `host.log`

- `host_log.go`：`hostLog` / `hostLogf`，**桥不可用时回退 `log.Printf`**
  （桥是改进不是依赖，丢诊断是坏交易）；quiesce 期间直接回退。
- 已用于：插件注册行、连续失败告警。

**⚠️ 一个只有真机才能发现的坑**：宿主的日志格式化器**只打印固定白名单字段**
（`logFieldOrder`，见 `internal/logging/global_logger.go`）。我最初发的
`fail_streak` / `schema_version` 被**静默丢弃** —— 消息照常打印，字段不见了。
改用白名单内的 `provider` / `reason` 后才可见。

真机结果：

```
[info ] plugin v0.9.37 registered (pluginabi schema 6) provider=workbuddy version=0.9.37
[warn ] 5 consecutive requests did not succeed — ... provider=workbuddy reason="no_usable_credential"
```

对比走 stdlib 的旧格式（无级别、无结构化字段、无法按插件过滤）：

```
2026/09/22 20:07:57 workbuddy: config checkin_auto=true ...
```

### 8.4 测试

新增 `integration_builtins_test.go`（20 个用例）覆盖：终态计数、
连续失败语义（含"取消不重置也不延长"）、环形缓冲只留失败、duration 推导、
RPC 容忍畸形输入、方法名锁定、get_runtime 的冷却推导/过期冷却/陈旧计数/
静默降级、host.log 回退与线形。

覆盖率 66.5% → **67.1%**。
