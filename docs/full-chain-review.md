# WorkBuddy 插件完整链路规划（审查稿）

> 基线：workbuddy **0.9.35** / 商店 **v1.0.19** / CPA **v7.3.12**
> 生成日期：2026-09-22
> 用途：审查用的 **0.9.35 历史快照**，不是当前实现说明；后续区域合并与行为变更以代码、README 和 `docs/architecture.md` 为准。文中 Global/Intl 三分区、耗尽删除及旧 HTTP fallback 等描述已过时。

---

## 0. 一句话概览

WorkBuddy 插件是一个 c-shared `.so`，通过 CPA 的 plugin RPC 协议对外声明 7 项能力，
把腾讯 CodeBuddy/WorkBuddy 包装成 CPA 的一个 provider（本文历史基线曾按 CN / Global / Intl 描述；当前用户可见区域为 CN / Intl）：
**认证、模型目录、聊天执行、额度、调度、用量统计、管理面板**。

---

## 1. 能力声明面（`wbRegistration`）

| 能力 | 值 | 实现文件 | 职责 |
|---|---|---|---|
| `ModelProvider` | true | `models.go` | 静态目录 + 动态发现 + 别名反解 + excluded 过滤 |
| `AuthProvider` | true | `oauth.go`, `authfile.go` | OAuth 登录、token 刷新、auth 文件解析 |
| `Executor` | true | `main.go`, `stream.go`, `payload.go` | 聊天执行（流式 + 非流式） |
| `ExecutorModelScope` | `OAuth` | — | 只为 OAuth 账号提供模型 |
| `ExecutorInputFormats` | `["chat-completions"]` | — | 只接 openai 格式请求 |
| `ExecutorOutputFormats` | `["chat-completions"]` | — | 只吐 openai 格式响应（**决定了 count_tokens 线形**，见 §5.4） |
| `Scheduler` | true | `scheduler.go`, `active_auth.go` | 默认委托 CPA 内置；可选插件自选账号 |
| `ManagementAPI` | true | `management.go` + 11 个文件 | 19 个管理端点 + 2 个面板资源 |
| `QuotaProvider` | true | `quota.go`, `billing.go` | **付费积分**的原生额度刷新 |
| `UsagePlugin` | true | `daily_quota.go` | **每日免费额度**的本机统计 |
| `FrontendAuthProvider` | false | — | 不使用 |

**SchemaVersion 6**（与 CPA v7.3.12 一致）。

> 关键区分：`QuotaProvider` 报的是**付费积分**（账号级、按套餐周期）；
> `UsagePlugin` 统计的是**每日免费额度**（每模型、每天重置）。
> 两者是不同预算，面板分区展示，代码路径完全独立。

---

## 2. 请求链路

### 2.1 聊天 — 非流式（`executor.execute`）

```
客户端 POST /v1/chat/completions
  │
  ├─ CPA：路由 → 选中 workbuddy 凭据 → RPC executor.execute
  │
  └─ 插件 handleExecExecute (main.go:903)
       1. parseStored(StorageJSON)          → storedAuth
       2. resolveUpstreamModel(model, attrs) → 反解 oauth-model-alias
       3. prepareUpstreamBody(payload, sa, model)
            单次 unmarshal/marshal 完成：
              forceStream  （上游 code 11101 拒绝非流式，必须强制 stream=true）
              normalizeTools
              rewriteSystem / ensureSystemMessage
              rewriteModel
       4. http.NewRequest(POST, endpointChatFor(sa))
       5. backendHeaders(req, sa)           → 区域化认证头
       6. hostHTTPDoStream(req)             → 走宿主 host.http.do_stream（请求日志可捕获）
       7. statusCode >= 400 → reconcileAfterExecutorError + translateChatUpstreamError
                              → errorEnvelope("upstream_error", msg, statusCode)
       8. aggregateCompletion(reader)       → SSE 折叠成单个 chat.completion
             └─ 空回答（只有框架无正文）→ emptyAnswerError → 429
       9. invalidateAccountCredits()
      10. okEnvelope(ExecutorResponse{Payload: completion})
```

**设计要点**：上游拒绝非流式，所以**永远**以流式请求上游、在插件内折叠。
客户端看到的仍是普通 `chat.completion`。

### 2.2 聊天 — 流式（`executor.execute_stream`）

两条分支：

```
handleExecStream (main.go:966)
  ├─ 有 StreamID（异步，主流路径）
  │    1. registerAsyncStream(StreamID, cancel)   注册到 quiesce 感知表
  │    2. 起 goroutine：pumpUpstreamStream(...)
  │         ├─ 逐块读上游 SSE → cleanChunkJSON → streamEmit(streamID, chunk)
  │         ├─ contentChunks 计数；结束时为 0 → emptyAnswerError → streamEmitError
  │         └─ 每块经 host.stream.emit 推给客户端（真流式）
  │    3. 立即返回 okEnvelope(streamResponse{Headers})
  │
  └─ 无 StreamID（同步回退，测试/兼容）
       collectUpstreamStream → 一次性返回所有 chunks
         └─ validChunks == 0 → emptyAnswerError → 429
```

**关键约束**：`registerAsyncStream` 登记到全局表，`quiescePlugin()` 会取消所有
在途流并等待；用 `context.Background()` 而非 `nil`，使客户端断连能取消上游读取
（否则会占着连接池直到 120s 超时）。

### 2.3 count_tokens（`executor.count_tokens`）

```
estimateInputTokensPayload(request)
  └─ countTokensWireShape(N)  →  {"usage":{"prompt_tokens":N,"completion_tokens":0,"total_tokens":N}}
```

**纯本地估算**，不请求上游、不消耗额度。除数 3（刻意高估），下限 1。

⚠️ **线形必须跟随宿主，不是客户端** —— 详见 §5.4。

---

## 3. 账号与凭据链路

### 3.1 登录（`auth.login_start` / `auth.login_poll`）

```
StartLogin → loginCtx{ cookies, region, platform, expires }
              state 存 loginStates（带 janitor 每分钟清理僵尸）
PollLogin  → 用同一 cookie 集轮询（CodeBuddy 把登录绑定到 auth/state 签发的 state）
           → 成功：返回 AuthData（accessToken/refreshToken/domain/region）
```

登录区域由 `login_region` 决定（`cn` / `intl`），平台由 `login_platform`（`CLI` / `ide`）。

### 3.2 凭据持久化（`host.auth.save`）

**所有**持久化都走宿主 RPC，插件从不直接写 auth 文件。

```
saveAuthState(name, existing, sa, disabled, note, extra)
  └─ buildAuthFileJSONFromExisting(existing, ...)
       ├─ 保留既有字段（operator 手工编辑优先）
       ├─ 写入身份元数据（仅缺失时）：
       │    email / account_name / uid / enterprise_id / region
       └─ hostAuthPersist → host.auth.save
```

⚠️ **宿主从文件 JSON 重建 auth 记录**（`buildAuthFromFileData`），
label 取 `metadata["email"]`，缺失则退化成 provider 名 `workbuddy`。
所以身份必须落进文件 —— 这是 0.9.32 修的 bug。

### 3.3 区域判定（`accountRegion`，单一真相源；以下为 0.9.35 历史实现）

优先级：
1. `sa.Auth.Region` 显式字段（`cn` / `global` / `intl`）
2. 归一化域名：`workbuddy.ai` → Global；`codebuddy.ai` → Intl；`codebuddy.cn`/`tencent.com`/`workbuddy.cn` → CN
3. 域名空 + JWT issuer 判定 → Global
4. 兜底 CN（历史兼容）

域名归一化会处理大小写、尾部点、scheme、路径 —— 不能直接字符串比较。

### 3.4 区域 → 端点映射（0.9.35 历史映射；当前应按 service realm 路由）

| 区域 | 上游 base | Origin/Referer | 特殊头 |
|---|---|---|---|
| CN | `https://copilot.tencent.com` | `codebuddy.cn` | — |
| Global | `https://www.workbuddy.ai` | `workbuddy.ai` | — |
| Intl | `https://www.codebuddy.ai` | `codebuddy.ai` | `X-IDE-Type/Name/Version`、`X-Product-Version`；**删除** `X-Requested-With` |

路径：
- 聊天 `{base}/v2/chat/completions`
- 模型 `{base}/console/enterprises/personal/models`
- 刷新 `{base}/v2/plugin/auth/token/refresh`
- 登录（固定 CN）`/v2/plugin/auth/state`、`/v2/plugin/login/account`

本节描述 0.9.35 时的路由映射，仅供历史审查；现行逻辑以 `accountServiceRegion` 和 `billing.go` 为准，按 auth 域名分别保留 CN、legacy WorkBuddy、CodeBuddy Intl 服务路由。

### 3.5 文件命名

```
authFileNameFor(sa) → workbuddy-CN-<uid>.json
                      workbuddy-Global-<uid>.json
                      workbuddy-intl-<uid>.json
                      （无 uid → workbuddy.json）
authFileNameCandidates(uid) → 新名 + 旧名 + 历史变体（大小写不敏感匹配）
```

有宿主物理文件名时**绝不改名**（`authFileNameForPhysical`），避免产生第二条 auth record。

### 3.6 旧账号收养（`adopt.go`）

启动时扫 `codebuddy-*` / 旧命名文件，写入新 canonical 记录，
**旧记录保留并标记 disabled**（由 CPA 管理端清理，插件不直接删宿主文件）。

---

## 4. 后台自动化链路

### 4.1 统一调度循环（`ensureScheduler` → `schedulerLoop`；本文记录 0.9.35 行为快照）

```
每 tick 取最近的时间点（checkin 09:00/21:00 ∪ keepalive 22:00）
  → runAutoCheckin()
      CN：签到（可选）→ reconcile（耗尽禁用 / 回血启用）
      Global：不自动领试用（一次性，仅手动）→ reconcile 保持耗尽禁用
      并发上限 4
  → 若命中 keepalive 窗口 → runTokenKeepalive()
```

循环归属插件生命周期：`quiescePlugin` 取消根 context 并等待该 worker 退出，
无独立 stop channel，不会与宿主关闭竞争。

### 4.2 积分生命周期（`policy.go` 纯决策 + `lifecycle.go` 应用；以下 Global 删除规则为历史行为）

```
reconcileOneAccount(authIndex, authID, force)
  1. hostAuthGetBundle → sa + phys（一次 RPC 拿全，A-19 优化）
  2. 取积分：缓存命中 → 用之；否则 cachedAccountDetails（singleflight 去重）
  3. region == cn && disabled → shouldReenableCN? → reenable / 仅刷新备注
  4. lifecycleActionFor(region, cr)：
       CN     耗尽 → disable（保留文件，标记 disabled）
       Global 耗尽 → delete（先二次确认，防 402 瞬态误删）
       健康        → 刷新备注（节流）
```

**纯函数分层**：`policy.go` 不做任何 I/O，只给决策；`lifecycle.go` 负责落盘。

触发点：
- 定时 tick（`runAutoCheckin`）
- 执行器错误（`reconcileAfterExecutorError`，仅硬积分错误）
- 面板手动 `/refresh`

**软限流（429）从不触发禁用** —— 只有硬积分标记（402 或 body 含
`insufficient credit`/`积分不足` 等）才动生命周期。避免误杀。

### 4.3 Token 保活（`keepalive.go`）

```
runTokenKeepalive()
  → 每账号 refreshOneAuth → 上游 token/refresh
      ├─ 成功 → persistAuthTokens（写回 auth 文件）
      └─ session dead → markSessionDead
  → recordKeepalive(summary) 供 /keepalive/status 查询
```

### 4.4 CN 成长任务 / 签到（`checkin.go`, `growth_events.go`, `tasks.go`）

- 签到：每日 09:00 / 21:00（`checkinHours`），签到后触发 reconcile 恢复账号
- 成长任务：`growth_auto` 开关；事件上报 → 点亮 → 领取
- 旅行：`travel_auto` 开关；到达领取 / 空闲出发

### 4.5 每日免费额度统计（`daily_quota.go`）

```
宿主每完成一个请求 → RPC usage.handle
  └─ handleUsageRecord(raw)
       ├─ 解析失败 → 仍回 ok（不能让已成功的请求因统计失败而报插件故障）
       ├─ isWorkBuddyUsage(record)?  非本 provider → 丢弃
       ├─ key = {AuthID, model, YYYY-MM-DD(本地)}
       ├─ 累加：input/output/total tokens、requests、failed
       ├─ pruneDailyQuotaLocked（保留 3 天）
       └─ persistDailyQuota → 原子写 {authDir}/workbuddy-daily-quota.json (0600)
```

`authDir` 来源：`Host.AuthDir` 只随 **model 发现类 RPC** 下发，
所以 `cacheModelAliases` 里调 `setDailyQuotaPath`。

**只显示不拦截**：本地计数可能漂移（重启/丢记录/时钟），绝不能挡住付费请求。
上游真拒绝时 402/429 照旧随错误信封走，由 CPA 冷却。

---

## 5. 与 CPA 宿主的契约（最易踩坑处）

### 5.1 RPC 全表（20 个 method）

| 类别 | method |
|---|---|
| 生命周期 | `plugin.register` / `plugin.reconfigure` / `plugin.quiesce` |
| 模型 | `model.static` / `model.for_auth` |
| 认证 | `auth.identifier` / `auth.parse` / `auth.login_start` / `auth.login_poll` / `auth.refresh` |
| 额度 | `quota.identifier` / `quota.describe` / `quota.fetch` / `quota.reset` |
| 执行 | `executor.identifier` / `executor.execute` / `executor.execute_stream` / `executor.count_tokens` |
| 调度 | `scheduler.pick` |
| 用量 | `usage.handle` |
| 管理 | `management.register` / `management.handle` |
| 宿主回调 | `host.auth.list` / `host.auth.get` / `host.auth.save` / `host.http.do` / `host.http.do_stream` / `host.stream.emit` / `host.stream.close` |

### 5.2 错误信封

```go
envelope{OK:false, Error:&envelopeError{Code, Message, HTTPStatus}}
```

`HTTPStatus` 是**上游真实状态码**（401/403/404/429/5xx），
宿主据此映射给客户端并决定冷却策略。缺失 → 宿主按 500 处理。

### 5.3 调度委托（0.9.34 变更）

默认 `builtin`：

```go
SchedulerPickResponse{DelegateBuiltin: "round-robin", Handled: true}   // 不填 AuthID
```

为什么不是 `Handled:false`：后者只表示"本插件不管"，宿主下一步未指定；
`DelegateBuiltin` **点名策略**，把行为钉死在 CPA 自己的 round-robin 上。

`scheduler_mode: credits` 时才返回 `AuthID`（面板选中账号，粘性 + 耗尽回退）。

### 5.4 count_tokens 线形（0.9.33 修，最隐蔽的坑）

插件声明 `ExecutorOutputFormats=["chat-completions"]`，因此宿主适配器执行：

```
TranslateNonStream(outputFormat=openai, requestedFormat=claude, …)
```

这走 **openai→claude 响应翻译器**，它读 `usage.prompt_tokens`。
若返回 Claude 原生 `{"input_tokens":N}`，会被**静默重写成 0**。

宿主的 `TokenCount` 钩子只对原生执行器可用（`ClaudeExecutor` 直接以参数调用），
插件执行器走不到。

**所以插件必须返回**：

```json
{"usage":{"prompt_tokens":N,"completion_tokens":0,"total_tokens":N}}
```

与 `helps.BuildOpenAIUsageJSON` 一致 —— 所有 openai 兼容原生执行器的形状。

验证：`scripts/verify-count-tokens-wire.sh`（两段式，含带牙齿检查）。

### 5.5 宿主 HTTP 桥

生产请求走 `host.http.do` / `host.http.do_stream`（请求日志可捕获）；
0.9.35 文档曾记载 `sharedHTTPClient` 在宿主不可用时回退；当前生产请求通过 CPA HTTP bridge 并在 bridge 不可用时失败关闭，测试使用 `hostHTTPTestOverride` 注入。

### 5.6 额度提供者

```
quota.describe → {SupportedProviders:[workbuddy, codebuddy, workbuddy-cn/global/intl, …],
                  DisplayName:"WorkBuddy", SupportsReset:false}
quota.fetch    → 解析 StorageJSON 或按 AuthIndex 取凭据 → 查 billing → 映射
quota.reset    → 明确 Success:false（上游无此能力，不伪造成功）
```

**`quota.fetch` 是只读的**：拉额度、更新插件缓存，但**不做任何生命周期副作用**。

---

## 6. 配置面

### 6.1 可视化字段（面板可见）

| key | 类型 | 默认 | 说明 |
|---|---|---|---|
| `checkin_auto` | bool | true | CN 每日自动签到 + 回血恢复 |
| `lifecycle_auto` | bool | true | 耗尽自动停用 / 回血自动启用 |
| `login_region` | enum cn/intl | cn | 只影响新登录 |
| `login_platform` | enum CLI/ide | CLI | 只影响新登录 |
| `models_cn` | string | 空 | 留空 = 自动发现 |
| `models_intl_global` | string | 空 | 留空 = 内置列表 |

### 6.2 隐藏字段（YAML 可用，面板不显示）

| key | 默认 | 说明 |
|---|---|---|
| `token_keepalive` | true | 22:00 token 保活 |
| `growth_auto` | true | CN 成长任务自动点亮（**同时是 travel 的总开关**，见下） |
| `travel_auto` | true | CN 旅行自动循环；**仅当 `growth_auto` 为 true 时才生效** |
| `scheduler_mode` | **builtin** | builtin / off(别名) / credits |
| `models_global` / `models_intl` | 空 | 分区域 pin |
| `models` | — | 自定义静态模型（支持按渠道分组） |
| `daily_free_limits` | 见 §4.5 | 每模型每日免费额度覆盖 |

> ⚠️ `travel_auto` 嵌在 `growth_auto` 之内：调用链是
> `checkin tick → growthAutoEnabled()? → runGrowthAutomation() → travelAutoEnabled()? → runBuddyTravel()`。
> 所以 `growth_auto: false` + `travel_auto: true` 时**旅行不会跑**。
> 这两个开关不是正交的，配置时需要知道这一点。

设计意图：面板只暴露"普通用户可能改"的字段；隐藏键在 YAML 里继续有效
（编辑器只 PATCH 被触碰的字段，隐藏键不会被覆盖）。

---

## 7. 管理 API（19 路由 + 2 资源）

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/accounts` | 面板主数据（账号 + 积分 + 每日额度） |
| POST | `/refresh` | 强制刷新额度 + 生命周期 reconcile |
| POST | `/checkin` | 手动签到（单个 / 全部） |
| POST | `/checkin/config` | 开关自动签到 |
| GET | `/credits` | 实时积分（单个 / 全部） |
| POST | `/import` | 导入凭据 JSON |
| POST | `/trial` | 领取 Global 专家试用包 |
| POST | `/select` | 选择面板活跃账号 |
| POST | `/keepalive` | 手动刷新 token |
| GET | `/keepalive/status` | 上次保活摘要 |
| GET | `/tasks` | CN 成长任务列表 |
| POST | `/tasks/accept` `/accept_all` `/claim` `/light` `/travel` | 成长任务操作 |
| GET | `/tasks/status` | 成长任务操作状态 |
| GET | `/daily-quota` | 每日免费额度（全部 / 单个） |
| POST | `/daily-quota/reset` | 清空**本地**计数（明确说明不恢复上游额度） |

资源（浏览器可访问，无管理鉴权）：
- `/v0/resource/plugins/workbuddy/panel` — 面板 HTML
- `/v0/resource/plugins/workbuddy/panel.js` — 面板脚本

⚠️ 两个资源都**必须**在 `managementRegistration` 里声明 —— 宿主按精确路径匹配，
未声明的资源会 404 导致页面白屏。

### 7.1 面板展示分区

```
┌─ 账号卡片 ─────────────────────────────┐
│ 昵称 / 邮箱  [区域徽章][套餐][耗尽][禁用] │
│ 积分进度条（付费额度，账号级）           │
│ ─────────────────────────────────────  │
│ 每日免费额度（本机统计）                 │
│   deepseek-v4.1-flash  剩余 1.9亿/2亿   │
│ ─────────────────────────────────────  │
│ 模型来源行 / 错误行                      │
│ [选用][刷新][任务][签到/领试用]          │
└────────────────────────────────────────┘
```

**原生 `/quota` 页面无法承载此内容**（已核实两处）：
- 前端按 provider 名硬编码 7 个 adapter（antigravity/claude/codex/xai/kimi/devin/meta）
- 前端完全不读后端的 `supports_quota` / `quota_provider` / `model_quotas` 字段

---

## 8. 缓存与并发

| 项 | 值 | 说明 |
|---|---|---|
| `accountCacheTTL` | 45s | 积分/套餐/签到快照 |
| `dynamicModelsCacheTTL` | 5min | 动态模型目录 |
| 账号详情 singleflight | — | 并发面板刷新合并为一次上游调用 |
| `dailyQuotaRetainDays` | 3 | 每日额度计数保留天数 |
| 并发上限 | 4 | 面板 / 签到 / reconcile 的每账号并发 |
| `managementBodyLimit` | 1 MiB | 管理请求体上限 |
| 发现重试阶梯 | 700ms / 2s（上限 5s） | 尊重 `Retry-After` |
| billing 重试阶梯 | 见 `billingRetryDelays` | 尊重 `Retry-After` |
| 管理限流 | 每 IP 令牌桶：容量 30，1 token/秒补充 | 仅 POST / 变更类 GET；idle 条目惰性淘汰（>1024 时） |

---

## 9. 降级与容错

| 场景 | 行为 |
|---|---|
| 上游空回答（只有 SSE 框架） | `emptyAnswerError` → **429**，让 CPA 冷却并换号 |
| 目录 `contextWindow` 全为 0 | `discoveryContextDegraded` → 429 → 回退区域静态目录（带真实上下文） |
| 积分未知（nil / 无包） | **不动**生命周期（`isCreditsExhausted` 为 false） |
| 软限流 429（无硬积分标记） | 不触发禁用 |
| Legacy WorkBuddy 402 | 旧行为为二次确认后 delete；当前保留并禁用账号记录 |
| 宿主 HTTP 桥不可用 | 旧文档为回退 `sharedHTTPClient`；当前生产请求失败关闭 |
| 用量记录畸形 | 仍回 ok（不因统计失败报插件故障） |
| 插件 quiesce | 取消根 context、等在途流、拒绝新工作 |
| 并发调用崩溃 | 宿主 `fusePlugin` 熔断该插件 |

---

## 10. 测试与验证

| 层 | 手段 |
|---|---|
| 单元 | `go test ./...`（覆盖率 66.5%） |
| 竞态 | `go test -race ./...` |
| 静态 | `gofmt -l`、`go vet` |
| 宿主契约 | `scripts/verify-count-tokens-wire.sh`（真实翻译器 + 带牙齿检查） |
| 真机 e2e | 构建 CPA v7.3.12 宿主 + 加载商店产物 `.so`，打真实 HTTP |
| 发布校验 | 下载全部资产 + `sha256sum -c` + 二进制符号核对 |

---

## 11. 已知缺口 / 待决策

### 11.1 免费额度数据是"本机统计"

上游无每模型额度接口，所以"今日已用"是插件自己累加的：

- **只覆盖本 CPA 实例的流量**。若同一账号在别处（官方客户端、另一台 CPA）也在用，
  插件看到的用量偏低，剩余量会显示得比真实乐观。
- 面板已标注「本机统计」并在 tooltip 说明，但这是**固有局限**，无法靠代码消除。

### 11.2 ~~`hy4-preview` 免费期可能已到期~~ → 已于 0.9.36 移除

内置表原本按 14 天窗口（2026-08-28 起算）给它设了 2 亿/天。窗口已过，
继续显示「剩余 2 亿」是误导，**0.9.36 已从内置表移除**。

注意是**移除而非拉黑**：插件仍然统计并显示它的用量，只是不再给百分比。
窗口确认重开时加回内置表，或用 `daily_free_limits` 覆盖即可。

内置表现在只剩 `deepseek-v4.1-flash`。有回归测试
（`TestOnlyDeepSeekHasBuiltInFreeAllowance`）锁住这一点，防止它被误加回来。

### 11.3 每日额度重置时区

按**宿主本地日历日**切分，上游真实重置语义未公开。
若上游按 UTC 或按滚动 24h，跨零点时面板会有偏差。

### 11.4 `scheduler_mode: credits` 与面板选中账号的耦合

`credits` 模式下，面板的"选用"按钮会**真的改变路由**。
默认 `builtin` 下它只是 UI 状态。这是有意设计，但值得在文档里更醒目。

### 11.5 其他三个插件未对齐

按你的要求本轮只动 workbuddy。account-concurrency 仍在 v7.2.158、
qoder/trae 在 v7.3.2。它们与 v7.3.12 的 ABI 兼容（SchemaVersion 均为 6，
pluginapi 纯增量），但没有实测过。

### 11.6 待你决策

1. **§11.1 的局限**是否需要在面板上更醒目（比如加一行"仅统计本实例"）？
2. ~~**§11.2** 的默认值要不要现在就清掉 `hy4-preview`？~~ → 已清掉（0.9.36）
3. **§11.5** 是否要把另外三个插件也对齐到 v7.3.12？
4. 每日额度的**重置时区**要不要做成可配置（`daily_quota_timezone`）？

---

## 附录 A：文件职责映射

```
main.go            RPC 分发、执行器入口、端点/头部常量、版本
stream.go          SSE 解析、流式 pump、聚合、空回答判定
payload.go         请求体重写（forceStream/normalizeTools/system/model）
models.go          静态+动态目录、别名反解、降级守卫、发现重试
billing.go         区域判定、端点映射、积分查询、上游错误类型
quota.go           原生 QuotaProvider RPC
daily_quota.go     每日免费额度计数 + 持久化
daily_quota_handler.go  每日额度管理端点
oauth.go           OAuth 登录流程
authfile.go        auth 文件命名、读写、身份元数据
lifecycle.go       生命周期应用 + 账号名解析
policy.go          生命周期纯决策
checkin.go         签到 + 统一调度循环
keepalive.go       token 保活
growth_events.go   CN 成长任务
tasks.go           任务数据
tasks_handler.go   任务管理端点
scheduler.go       scheduler.pick
active_auth.go     面板活跃账号状态
cache.go           账号缓存 + singleflight
panel.go           面板数据构建
management.go      管理路由注册与分发
credits_handler.go 积分/导入/选择/试用端点
host_bridge.go     宿主 HTTP 桥 + 测试接缝
host_auth.go       宿主 auth RPC 封装
config.go          配置解析（YAML）
token_count.go     count_tokens 本地估算
adopt.go           旧账号收养
fingerprint.go     设备指纹
redact.go          敏感信息脱敏
```
