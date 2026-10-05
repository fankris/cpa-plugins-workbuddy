# Independent rebuild — v8.0.15-1.0.45 (2026-10-05)

- Replaced the old panel with a Manager-led React/TypeScript four-workspace UI.
- Pinned official CPA 8.0.15 SDK/schema 6 and tested real loading with CPAMC 1.25.3.
- Delegated settings/OAuth/logs and credential actions to native host capabilities.
- Added same-origin locale/theme following and transient state restoration through CPAMC iframe remounts.
- Preserved legitimate tasks with accept-only runs, cooperative cancellation and truthful/redacted outcomes; retired synthetic reporting.
- Changed omitted scheduler mode to host; keepalive/travel now opt-in. Explicit compatibility modes remain. See README_CN.md before upgrading.
- Final isolated regression passed; real account business E2E and production deployment remain untested.

---

## Historical upstream/development changelog (not current acceptance evidence)

# Changelog

## v8.0.13-1.0.41 — 2026-10-04

### Resource menu migration and responsive panel workspace

- Register the single WorkBuddy dashboard menu on the current `/panel` resource route and remove the legacy GET menu route so current CPA hosts expose the correct entry.
- Reorganize account, global model, and automation controls into responsive in-panel workspaces; add keyboard-accessible tabs, modal focus handling, account search/filters, and stale-data/empty states.
- Keep automation defaults display-only until touched; serialize changed-key CPA config PATCHes and guard late dashboard/config responses.
- Distinguish a fetched zero balance from unknown quota, bound background credit lookups, and clarify plugin account preference versus CPA routing.
- Use host-returned service eligibility and structured check-in outcomes for action state and feedback.

## v8.0.13-1.0.39 — 2026-10-03

### SDK v8 alignment and request compatibility

- Migrate the plugin API dependency and imports to CLIProxyAPI v8.0.13.
- Normalize string-form OpenAI `image_url` parts and escaped underscores in tool/function JSON Schema patterns before forwarding requests.
- Add focused regression coverage while preserving canonical image objects and non-tool schema data.
- Ignore usage records without a provider identity so unrelated or incomplete records cannot create WorkBuddy daily-quota buckets.
- Compare the reference gateways and retain the plugin’s existing account, quota, and model-routing boundaries; gateway-only pool/admin features and unverified credit-error semantics are not copied.

## v7.3.20-1.0.38 — 2026-09-27

### Credit visibility, model management, and stability

- Replace the misleading missing-daily-limit label with account-cycle credit consumption and a transparently labeled model-cost estimate.
- Clarify model-management rows with model name/ID, context/output caps, local token usage, estimated credits, and credits per million tokens; preserve per-model enable/disable controls.
- Coerce HTML-escaping input to a string so numeric catalog metadata cannot break supported-model loading.
- Document the repeatable full upgrade, validation, and release workflow.

## v7.3.20-1.0.37 — 2026-09-27

### Model allowlist and panel refinements

- Define and validate `fmtClock` so cooldown cards no longer throw at render time.
- Make global model registration opt-in: all models default disabled; checked IDs are saved in the `models_enabled` allowlist.
- Improve the per-account supported-model browser with search, enabled/disabled filters, counts, and clearer metadata.
- Keep compact account-card actions on one row with compact labels and sizing.

## v7.3.20-1.0.36 — 2026-09-27

### CPA v7.3.20 and settings refresh

- Upgrade the CPA SDK to v7.3.20 and use terminal scheduler rejection when every matching WorkBuddy candidate is disabled or unavailable.
- Move global model exclusions into Settings → Model Management with search, status filters, responsive layout, and visible saved IDs that are temporarily absent from the upstream catalog.
- Reorganize settings into automation and model-management tabs, with clearer loading and save states.

## 1.0.35 — 2026-09-26

### CPA v7.3.18 alignment and panel menu compatibility

- Align the plugin SDK with CPA v7.3.18; no breaking plugin API changes were required.
- Advertise the dashboard menu through a legacy GET route so older CPA hosts still discover it; retain the explicit `panel.js` resource.

## 0.9.50 — 2026-09-26

### DeepSeek reasoning mode normalization

- Empty or partial `thinking` objects now preserve caller options while enabling the reasoning mode, unless `thinking.type` is explicitly `disabled`; this keeps injected `reasoning_effort` consistent with the documented opt-out behavior.

## 0.9.49 — 2026-09-25

### Reasoning effort 传输与自动档位

- 将客户端 `reasoningEffort` 与 `reasoning_effort` 统一为上游 canonical 字段，并规范已知档位大小写；camelCase 输入和冲突字段按确定优先级处理。
- 有明确档位就用明确档位；`none`、`off`、未知值或空值不再发送给上游，也不用于关闭推理——改为使用模型目录声明的默认档位，目录未声明时省略该字段由上游走自动默认。关闭推理仅通过 `thinking.type=disabled` 表达。

## 0.9.48 — 2026-09-25

### Intl 区域合并、全局模型禁用与 reasoning effort 自愈

- CPA 原生配置表单现在仅暴露 `login_region` 和 `login_platform`；原有高级 YAML 键仍兼容读取。
- 面板可全局持久化 `models_disabled`；禁用模型从 `model.static` 与 `model.for_auth` 结果中同时剔除，宿主 `oauth-excluded-models` 仍独立生效。
- 当上游以 HTTP 400 明确返回 `code=11150` / `invalid_reasoning_effort` 时，经 CPA host stream bridge 无 effort 参数重试一次；不会重跑 effort 注入，消息级 reasoning traces 保留。目录未声明默认档位时不再猜测 `high`。
- 用户可见区域统一为 CN / Intl。`workbuddy.ai` 与 `codebuddy.ai` 仍采用独立服务路由、请求身份、模型缓存和账号命名，避免旧账号被改发到新网关。
- 中英文 README 与架构图补充配置表面、模型禁用和 Global→Intl 服务身份拆分说明。

### 成长任务管线加固、夜猫子 tick、目录对齐：学习 workbuddy2api-hub 第二轮 (0.9.47)

继续对照 workbuddy2api-hub 的实测修复（PR #21/#27、PR #22 之后的任务与目录侧）：

1. **领取竞态修复**（growth_events.go）：原来上报完睡 1.5s 就盲领。上游落账
   异步且有事件被静默丢弃（id 去重、未接取），盲领产出 "task not completed"
   失败并把奖励留死。现在领取前轮询任务进度（≤6 次快查，进度在动时放宽到
   2.5s 间隔），达标才领，未达标记"领奖顺延到下次运行"。
2. **接取重试与未接取跳过**：上游偶发整批瞬时拒绝接取；过去只试一次，之后
   所有事件都作用在未接取的任务上（进度永远 0）。现在复查后补接一次，重列
   清单后仍未接取的任务直接跳过并注明原因，不再浪费上报。
3. **01:00 夜猫子 tick**（checkin.go / config.go）：夜猫子（black_cat）只在
   23:00–08:00 计数，而每日 growth 跑在 09:00/21:00——都在窗口外，任务永远
   不会被自动点亮。现在调度器增加 01:00 专用 tick，只处理 black_cat：达标
   直接领，未达标上报一次夜间对话事件后走同一进度轮询。参考项目调度器的
   "每日 01:00 夜猫子" 完全一致。
4. **动态发现过滤非 chat 模型**（models.go）：上游模型 API 会带出 `lite`
   （内部标题/压缩助手，调用必 11102）、`default-model` 等快捷别名、
   `codewise-*`/`completion-*`（IDE 行内补全）、`*-image-alpha` 等图片变体。
   发现结果现在按 workbuddy2api-hub 的 is_chat_model 规则清洗。
5. **静态目录对齐桌面端实测清单**：CN 静态表从 12 项（过时的 8192 输出上限）
   对齐到实测 CN_UI_ORDER 14 项 + legacy 尾部；Global/Intl 静态表从仅 1 项
   扩到实测国际版 16 项（www.workbuddy.ai 与 www.codebuddy.ai 解析到同一
   地址，同一网关）。CN-only 的 deepseek-v4-flash/-pro、kimi-k3-1 仍然绝不
   出现在国际侧目录；legacy 条目保留在尾部，存量路由不受影响。
6. **核实后不采纳**：参考项目称 www.codebuddy.ai 在部分网络无法解析（他们的
   DNS 返回 0.0.0.1）。本机实测四域名全部正常解析、端点应答 401/302，说明
   这是他们本地 DNS 的问题，插件端点不做变更。

测试桩升级为有状态模拟（接取成功改变 accept_status、上报成功推进 current），
新增接取重试、未接取跳过、进度滞留顺延、夜猫子专项四个行为测试。

### 错误分类与请求修复：学习 workbuddy2api-hub (0.9.46)

对照 [workbuddy2api-hub](https://github.com/ardeyouxipianyi/workbuddy2api-hub)
（针对同一上游的独立网关项目）的五项已实测优化，适配到 CPA 插件架构：

1. **模型级频控（code 6004）识别**（chat_error.go）：上游 429 返回
   `code 6004 "usage exceeds frequency limit"` 时，这是**模型级**限流，
   不是账号失效。CPA 对 executor 429 的冷却本来就只落在失败模型上
   （`auth.ModelStates`），所以保持 429 状态不动，但错误信息会解析 body 里的
   "reset at ..." 戳并给出恢复时间。参考项目 PR #22 的教训：把 6004 当账号
   失效会拖垮同账号其它模型。
2. **403 内容审核解耦**（chat_error.go）：chat 403 是内容审核拦截
   （11140 等），凭据与模型都健康。原样上报 403 会让 CPA 给该账号此模型
   30 分钟冷却（`case 401,402,403: now+30min`）——一次内容拦截毒化账号。
   现改为映射到 **400**（CPA 的 request-scoped 类：`shouldSkipCredentialCooldown`
   不冷却、轮询继续），双语文案说明"账号未被惩罚，改提示词重试即可"。
   对应参考项目 PR #28 的"403 直通，不毒化账号池"。
3. **DeepSeek 思维链一致性**（payload.go）：thinking 开启时上游要求每条
   assistant 消息带 `reasoning_content`（否则 400 code 11155），且
   `thinking:{type:enabled}` 单独存在时上游不产生思维链——必须有
   `reasoning_effort`。现在：未显式退出（thinking.type≠disabled 且
   effort≠none）时回填全部 assistant 消息的 `reasoning_content` +
   非空 `reasoning` 镜像，并注入 `thinking:{type:enabled}` 与默认档位
   （读上游模型目录声明的 `reasoning.defaultEffort`，缺省 high）。
   客户端显式档位不覆盖。对应参考项目 v1.4.9 与官方
   ReasoningContentBackfillRule。
4. **工具调用配对自愈**（payload.go）：失败的调用让历史里留下无人应答的
   `tool_calls`，上游对之后每一轮都回 400 code 11148，一次失败报废整条会话。
   出站前先修复：把被插入消息隔开的结果块挪回所属批次（保持相对顺序），
   再按同一 id 集合对称裁剪"有调用无结果"与"有结果无调用"。健康历史零改动。
   对应参考项目 v1.4.9。
5. **流式 usage 全 0 占位帧防护**（stream.go）：GPT 系模型中间帧会带全 0
   usage 占位；折叠完成体时按"总量单调"吸收（新帧 total ≥ 已存 total 才
   替换），避免占位帧抹掉真实用量并污染宿主侧 token 记账。对应参考项目
   v1.4.5。另：未指定时注入 `stream_options.include_usage`（参考项目实测
   上游容忍该字段）。

未采纳（有明确理由）：`prompt_cache_key` 注入（参考项目实测上游本就自动
复用前缀，默认关闭）、跨区域身分切换（CPA 内置调度拥有路由/重试权，插件
不越权）、web_search 代跑（上游无此能力，插件本就不注入）。

### 操作区左右对齐修正：选用靠右，其余靠左 (0.9.45)

0.9.44 我把操作区做成了**三段平铺**，方向错了。原生的结构是：

```
actions (justify-content:space-between)
├── actionsMain (左)
│   ├── [≡ 模型]        带标签按钮
│   └── utilityActions  纯图标工具（**内嵌在 actionsMain 里**，不是独立一段）
└── toggleWrap (右)     状态控件，margin-left:auto
```

两处修正：

1. **`actions-util` 改为嵌在 `actions-main` 内部** —— 原生 `utilityActions` 就是
   `actionsMain` 的子元素，我之前把它当成了并列的第三段
2. **选用移到右侧 `toggle-wrap`**，用 `margin-left:auto` 顶到右边（原生
   `toggleWrap` 的写法）。选用对应原生的状态开关，语义上就该在右侧

结果：

```
[≡ 模型] [↻ 刷新] [✦ 任务] [签到]                    [选用]
└──────────── 左 ────────────┘                      └─ 右 ─┘
```



### 模型信息完全收进按钮，对齐原生 (0.9.44)

**去掉模型数量的外显**：卡片上那行「模型 N 个」整行删除 —— 原生卡片
**根本没有这一行**，模型信息全部收进按钮。数量与目录来源现在只在弹窗里看。

**模型按钮改为「图标 + 文字」**（学原生 `models_button`）：

原生那个按钮是 `variant:secondary, size:sm`，内容是**图标 + "模型"标签**，
不是纯图标按钮 —— 我上一版做成了纯图标 `≡`，与原生的表达力不同。
现在改为 `≡ 模型`，并放在 `actionsMain` 首位（原生的位置）。

**操作区结构对齐原生**：

```
actions
├── actionsMain   [≡ 模型] [选用] [签到/领试用]      ← 带标签的按钮
└── actionsUtil   [↻ 刷新] [✦ 任务]                  ← 纯图标按钮
```

**过程中我搞坏了一次**：用索引删除模型行时，连带删掉了夹在中间的四个函数
（`fmtDuration` / `fmtNum` / `fmtTokens` / `dailyFreeHTML`），
jsdom 实跑立刻报 `dailyFreeHTML is not defined` —— 语法检查看不出来。
已按 git 原版精确恢复这四个函数（而非重写）。



### 去掉模型入口的重复 (0.9.43)

0.9.42 我给模型弹窗加了**两个入口**：行内的「N 个模型 · 查看」链接和操作区的
`≡` 图标按钮 —— 同一功能两个入口是重复，不是便利。

保留**操作区图标**（原生 `models_button` 就在工具区），去掉行内按钮。

行内改为纯文本「N 个」，因为**数量本身有信息量** —— 一眼看出目录是否异常
（例如从 12 个掉到 1 个）。目录获取失败时仍在行内标注并附原因（hover 看全文）。

连带清理已无引用的 `.md-row .link-btn` 样式。



### 去掉重复指标行 + 模型列表可查看 + 操作区分层 (0.9.42)

**1. 删除重复的关键指标行**

卡片顶部那行「可用积分 / 成功率 / 免费额度」与下面的分区**完全重复**
（同一份数据渲染两遍），已整行删除，连带 `statChip` 与 `.chip*` 样式。
数据仍在下方的积分条 / 健康条 / 免费额度分区里，信息没有减少。

**2. 模型列表可查看（学原生 `models_button` → 弹窗）**

旧版只有一行「已支持 N 个模型」—— **纯数字回答不了任何实际问题**：
模型缺失时运维需要知道**具体是哪些模型**，才能判断问题出在区域路由、
excluded 过滤、配置 pin 还是上游目录。

- 卡片改为可点击入口：`N 个模型 · 查看`
- 弹窗列出每个模型的 **ID / 上下文 / 输出上限**，并显示目录来源
- 新增 `GET /models`（全部或按 `auth_index`）与 `POST /models/refresh`

**关键**：列表走的是**与宿主发现流程同一套解析**
（`fetchDynamicModelsFromStorage`），所以面板显示的模型列表不会与 CPA 实际
路由的列表不一致 —— 这正是不再重复上一轮健康条那类错误（自己算一套、
结果和宿主用的不是同一个数）。手动刷新会**先失效缓存**再查上游，
否则 5 分钟缓存会让刷新按钮看起来没反应。

**3. 操作区改为三层（学原生 `actions` 的 main/utility 结构）**

原先把「选用/刷新/任务/签到」平铺一排。现在分为：

- `actions-main`：主操作（选用 + 签到/领试用）
- `actions-util`：图标按钮（刷新 ↻ / 模型 ≡ / 任务 ✦），30×30、次要操作不抢视觉
- 顶部分隔线 + `margin-top:auto` 贴底（0.9.40 已做，本轮保持）

Global/Intl 账号不显示「任务」图标（成长任务仅 CN）。

**验证**：jsdom 实跑 15 项断言全过（指标行已删、三个分区仍在、模型入口可点击、
actions 三段式、弹窗列表/错误/空态渲染），无运行时错误；新增 5 个 Go 用例
覆盖模型接口（全量/单账号/未知账号报错/刷新缺参拒绝/排序稳定）。

### 健康条改用宿主数据：修掉"插件自己统计"的根本性错误 (0.9.41)

**0.9.40 我做错了一件事**：自己在插件里实现了时间分桶统计，还做成了
**provider 级**。正确做法是读宿主的 —— 而宿主**早就有了**。

实测确认（读源码 + 真机验证）：

- 宿主已按**凭据**维护 `20 × 10 分钟` 的 success/failed 环形分桶
  （`sdk/cliproxy/auth` 的 `RecentRequestsSnapshot`）
- 每个被**路由到该凭据**的请求都记进去（`conductor_cooldown.go`）
- 经 `host.auth.get_runtime` → `buildHostAuthFileEntry` → `RecentRequests`
  下发给插件

**为什么插件自己统计是错的**（不只是重复劳动）：

| | 宿主统计 | 插件自己统计 |
|---|---|---|
| 覆盖范围 | 所有路由结果 | 只有到达本插件执行器的请求 |
| 账号归属 | ✅ 按凭据 | ❌ 拿不到（实测 `selected_auth_id` 不下发） |
| 漏计场景 | — | 全部凭据不可用时宿主直接拒绝，**一条都看不到** |

也就是说：我做的那个 provider 级健康条，恰好在"所有账号都不可用"时
**最不可靠** —— 而那正是最需要它的时刻。

**改动**：

- 删除插件侧的分桶实现（`lifecycleBucket` / 旋转 / 对齐 / 快照字段）
- `host_auth.go` 读取 `RecentRequests`，`panel.go` 透传为 `runtime.health`
- 健康条改为**每账号一条**，放回卡片内（现在有账号维度数据了）
- 汇总区的全局健康条移除

**保留**插件侧 `request.complete` 的用途：错误事件环形缓冲（"为什么失败"），
那是宿主不提供的。但**不再**用它算健康度。

新增 3 个用例锁定方向：宿主序列透传不变形、无该字段时仍可用（不 nil 不报错）、
以及**结构性守卫** —— 断言插件不再发布自己的 `buckets` 字段，防止实现被改回去。



### 学习原生卡片：健康条 + 布局对齐 + 冷却绝对时刻 + 密度切换 (0.9.40)

按 `docs/native-card-study.md` 的建议实施前四项。

**1. 时间分桶健康条（学原生 `statusBar`）**

20 格 × 10 分钟 = 最近 200 分钟，每格按该时段成功率着色，悬停显示
`时间范围 · 成功 N · 失败 N · 百分比`；右侧是整体成功率（≥90% 绿 / ≥50% 黄 / 其余红）。

关键语义（照抄原生的设计）：**空闲格与全失败格严格区分** —— 灰色=无请求，
红色=全失败。混用会让闲置账号看起来像故障。

三个实现要点：

- 轴按**绝对时间**对齐，空闲期照常补空格。若只在有流量时才建桶，空闲时段会被
  静默压缩，一个"故障后转安静"的账号会看起来一直健康
- `Canceled` **不计入**健康条：客户端断连不是 provider 信号，计入会往健康账号上涂红
- 旋转步数封顶，避免时钟跳变（或坏 `CompletedAt`）把循环卡住

内存**反而变小**：20 桶 × 2 个整数，比原先保留 50 条事件还省。

**实测纠正了一处设计假设**：我本想把健康条做成**按账号**的（每张卡片一条），
先加了从 `completion.Metadata["selected_auth_id"]` 取账号的逻辑。**实测宿主不下发
这个字段** —— tracker 捕获的 metadata 与 conductor 内部新建的 map 不是同一个，
所以插件拿不到账号维度。因此健康条是 **provider 级**，放在汇总区只出现一次；
放在每张卡片会重复同一份数据并暗示它是按账号统计的。已移除那段取不到数据的代码。

**2. 卡片布局对齐**（学原生 `actions` 的 `margin-top:auto`）

卡片改为 flex 列、`.card-body` 撑开、操作区贴底并加顶部分隔线 —— 网格里多张
卡片高度不一时底部按钮对齐，视觉更整齐。

**3. 冷却绝对解除时刻**（学原生 `deadline`）

冷却徽章的 tooltip 现在显示 `解除时间 HH:MM:SS`（本地时间），省去"现在几点 +
还剩多久"的心算；同时原样显示 `status_message`（**不做原因分类映射** ——
实测宿主不返回结构化原因，硬映射等于猜）。`fmtClock` 容忍非法输入，不会出现
`Invalid Date`。

**4. 密度切换**（学原生 `gridCompact`）

工具栏新增「紧凑视图」，列宽 320px ↔ 250px，偏好存 localStorage。

验证：jsdom 实际执行渲染 15 项断言全过（健康条格数/空闲区分、卡片 flex 与
贴底、`fmtClock` 输出与容错、冷却 tooltip、密度切换往返），无运行时错误；
新增 7 个 Go 用例覆盖分桶语义（宽度固定/空闲与失败分离/事件归属正确桶/
空闲补位/排除 canceled/时钟跳变/超窗丢弃）。覆盖率 67.2% → 67.3%。

### 面板去掉「宿主调度状态」区块 (0.9.39)

按需求移除卡片里那个多行区块（标题 / 可用-冷却 / 状态 / 优先级 / 成功失败 /
原因文字），以及随之无用的 `.rt-*` 样式与 `runtimeHTML` 函数。

**保留**压缩到一行内的摘要，它们不占版面：

- 顶部「冷却 X 分钟」徽章
- 关键指标里的「成功率」

`host.auth.get_runtime` 的数据获取**保持不变** —— 徽章和指标仍然依赖它，
去掉的只是展示区块。页面副标题与注释也同步更新，不再提这个区块。

**过程中发现并修复了一个自己引入的 bug**：删区块时一并删掉了 `fmtDuration`，
但「冷却」徽章仍在调用它 → 一旦账号进入冷却就 `ReferenceError`。
语法检查（`node --check`）全程通过，只有用 jsdom **实际执行**渲染才暴露出来
（`FAIL cooling: fmtDuration is not defined`）。已恢复该函数并注明其唯一用途。



### 流式限流不再误报成功 + 面板界面重构 (0.9.38)

**修复 1（核心）：流式错误发错了字段，导致限流被记为成功**

这是"上下文为 0 / 限流时仍显示成功"的**真正根因**，在插件侧而非宿主侧。

宿主解析 `host.stream.emit` 时：

```go
chunk := pluginapi.ExecutorStreamChunk{Payload: req.Payload}
if req.Error != "" { chunk.Err = fmt.Errorf("%s", req.Error) }   // ← 只有这个字段会标记失败
```

而插件旧实现把错误当成 **payload** 发出去：

```go
errJSON, _ := json.Marshal(map[string]any{"error": map[string]any{"message": ...}})
_ = streamEmit(streamID, errJSON)   // ← 宿主视作普通模型输出
```

后果：被限流的流（200 + 合法 SSE 框架 + 无内容）在宿主眼里是**成功请求**，
`chunk.Err` 始终为 nil，凭据永不冷却 —— 这正是"限流但还是成功状态"。

修复：`streamEmitError` 改为把消息放进 `error` 字段，payload 留空。
所有异步流错误路径（HTTP 错误、上游 >=400、空回答、读失败）都汇聚到这一个函数，
所以一处修复覆盖全部。消息仍经 `redactSecrets` 脱敏（宿主会把它带进错误链路）。

真机验证：流式请求失败现在记为 `outcome=failed, stream=True`（此前为 succeeded）。

**已知残余**：宿主的 `error` 字段只承载文本，无法携带状态码，所以请求终态里
`status` 显示 500 而非上游真实的 401/429。这是宿主接口的限制，插件无法传递；
但"失败"这一判定是准确的，冷却与重试因此正常。

**修复 2：面板界面重构**

原布局是平铺堆叠，账号一多重要信息就被噪音淹没。重构为：

- **页头**：标题 + 主操作（导入/设置/刷新）分离，不再和批量操作挤一行
- **卡片**：左侧 3px 状态色条（冷却/禁用/耗尽/使用中/正常），扫读时不用逐个看徽章
- **关键指标行**：可用积分 / 成功率 / 免费额度占用，三项以内一眼看完
- **分区详情**：积分（付费）· 宿主调度状态（CPA 侧）· 每日免费额度（本机统计）
  三者视觉分离 —— 它们常被混为一谈，必须一眼能区分
- **指标格式化**：积分用千分位（`1,234`），token 用中文缩写（`1.99亿`）；
  积分粒度小，缩写会让"剩余 1万"看不出是 10000 还是 19999

用 jsdom 对 8 种账号状态（正常/冷却/耗尽/禁用/无额度/无积分/无 runtime/错误）
实际执行渲染，确认无运行时错误、结构完整、告警逻辑生效。

### 接入 CPA 三项内置能力 (0.9.37)

按 `docs/cpa-builtins-audit.md` 的建议接入三项（其余 14 项评估为不该接，
理由见该文档）。共同动机：过去几轮 bug 的**共同根因是插件在猜宿主行为**，
这三项都是"改为向宿主读取事实"。

**1. `host.auth.get_runtime` — 面板显示 CPA 的真实调度状态**

`host.auth.get` 只返回 `{auth_index, name, path, json}`，所以插件一直自己维护
"耗尽/禁用/冷却"的判断，而这套判断与 CPA 实际调度依据**可能不一致**。
现在面板新增「宿主调度状态」区块：可用/冷却中（含剩余时间）、状态、优先级、
成功/失败计数，全部来自宿主。

一个推导：`NextRetryAfter` 在未来即视为不可用 —— 即便宿主尚未置
`unavailable` 布尔位（两字段在冷却路径不同阶段写入，以截止时间为准更安全）。

降级：宿主不支持该 RPC 或凭据已删除时返回 nil（非错误），面板退回插件侧状态。

**2. `RequestLifecyclePlugin` — 补上用量统计的观测盲区**

`UsagePlugin` 只覆盖到达执行器的请求；没有可用凭据时不产生 usage 记录，
插件对"请求来了但没被服务"完全无感。现在接收 `request.complete` 终态事件。

**⚠️ 实测修正了我最初的假设**（接入前我断言能捕获"凭据不可用"的拒绝，
只对了一半）：

| 场景 | 事件 | 原因 |
|---|---|---|
| 模型无人认识 | ❌ | tracker 在 provider 解析**之后**创建 |
| provider 已知但无可用凭据 | ✅ failed | 走 AuthManager.Execute |
| 拦截器终止请求 | ✅ rejected | `msg.DirectResponse` |

关键：**`rejected` 不是凭据耗尽信号** —— 宿主只在拦截器终止时发它。
凭据不可用产生 `failed`。所以统计"连续未成功"而非"连续被拒"，
且**不按字符串猜失败类别**（猜类别正是过去 bug 的根因）。

**3. `host.log` — 日志走宿主管道**

可分级、可按插件过滤、可带 request_id。**桥不可用时回退 stdlib log**
（桥是改进不是依赖，丢诊断是坏交易）。

**⚠️ 真机发现的坑**：宿主日志格式化器**只打印固定白名单字段**
（`logFieldOrder`）。最初发的 `fail_streak`/`schema_version` 被**静默丢弃** ——
消息正常打印但字段消失。改用白名单内的 `provider`/`reason` 才可见。

真机验证：

```
[info ] plugin v0.9.37 registered (pluginabi schema 6) provider=workbuddy version=0.9.37
[warn ] 5 consecutive requests did not succeed — ... provider=workbuddy reason="no_usable_credential"
```

**面板新增**：「宿主调度状态」区块（每账号）、「近期请求」汇总行、
连续失败红色告警条。

测试：新增 `integration_builtins_test.go`（20 用例），覆盖率 66.5% → 67.1%。

### 对齐 CPA v7.3.12 + 调度默认交还内置 + 每日免费额度面板 (0.9.34)

**变更 1：对齐 CPA v7.3.12**

`go.mod` 从 v7.3.7 升到 v7.3.12。**零源码改动**即可编译通过 ——
`sdk/pluginapi` 与 `sdk/pluginabi` 在此期间是纯增量变更（`SchemaVersion`
始终为 6，无字段删除或改语义）。`scripts/verify-count-tokens-wire.sh`
已用 v7.3.12 的真实翻译器重跑，count_tokens 线形仍然通过。

**变更 2：`scheduler_mode` 默认改为交还 CPA 内置调度**

旧默认是 `off`，行为是返回 `Handled:false` —— 只表示"本插件不管"，之后
宿主怎么做并未指定。新默认 `builtin` 显式返回：

```go
DelegateBuiltin: pluginapi.SchedulerBuiltinRoundRobin
```

差别是**确定的**：`DelegateBuiltin` 把策略钉死在 CPA 自己的 round-robin
实现上，插件无法与其漂移。`off` 保留为 `builtin` 的别名，旧配置继续可用。

开关仍然保留：`scheduler_mode: credits` 时才由插件按面板选中账号调度。

**变更 3：面板新增「每日免费额度」（按模型）**

这是与积分**不同**的预算，之前混在一起理解会出错：

|          | 积分（credits） | 每日免费额度 |
|----------|----------------|-------------|
| 性质     | **付费**额度    | **免费**额度 |
| 维度     | 账号级总量      | **每模型**   |
| 重置     | 按套餐周期      | **每天**     |
| 数据来源 | 上游 billing    | **插件本机统计** |

上游不提供每模型额度接口，所以"今日已用"由插件自己统计：新增
`UsagePlugin` 能力接收宿主每请求上报的 `UsageRecord`，按
`(账号, 模型, 日期)` 累加，落盘到 auth 目录（0600，原子写入）。

内置限额表只有两个模型有免费额度，可用 `daily_free_limits` 覆盖或新增：

```yaml
# 内置默认（无需配置）：deepseek-v4.1-flash 2 亿/天
daily_free_limits:
  deepseek-v4.1-flash: 2.5亿   # 支持 亿/万/k/m/b/2e8/200_000_000
  glm-5.2: 50000000            # 新增内置表里没有的模型
```

未配置限额的模型仍会显示**已用**量，只是没有百分比 —— 上游没给额度时
不编造一个数字。

> 0.9.36 修正：移除 `hy4-preview` —— 它的免费窗口（2026-08-28 起 14 天）
> 已过，继续显示「剩余 2 亿」是误导。**它没有被拉黑**：插件仍然统计并显示
> 它的用量，只是不再给百分比。窗口确认重开时加回即可，或用
> `daily_free_limits` 覆盖。
>
> 0.9.35 修正：0.9.34 的内置表多写了 `deepseek-v4-flash` 与
> `deepseek-v4-pro` —— 这两个模型**没有**免费额度，内置它们会让面板给一个
> 不存在的额度配上百分比。现在内置表只有确实有免费额度的两个。

**只显示，不拦截**：超额不会让插件拒绝请求 —— 上游才是自己免费额度的
权威，一个可能漂移的本地计数器绝不能挡住付费请求。上游真正拒绝时
（402/429）状态码照旧随错误信封返回，由 CPA 冷却该凭据。

面板位置：**插件自己的面板**（CPA 管理页侧边栏的 WorkBuddy 页）。
原生 `/quota` 页面无法承载 —— 它按 provider 名硬编码了 7 个 adapter
（antigravity/claude/codex/xai/kimi/devin/meta），WorkBuddy 不在其中，
且面板前端完全不读后端输出的 `supports_quota`/`model_quotas` 字段。

**修复 1：限额来源标签错误（真机 e2e 发现）**

同时存在于内置表和配置覆盖里的模型，`limit_source` 会错报 `default` ——
数值是对的，但来源标签撒谎，而运维正是靠这个标签判断配置有没有生效。
改为显式跟踪覆盖集合，并加了回归测试。

**修复 2：`亿`/`万` 后缀根本没被解析（真机 e2e 发现，同一个坑）**

配置里写 `deepseek-v4.1-flash: 2亿` 时 `parseTokenLimit` 返回解析失败，
覆盖被**静默丢弃**，退回内置默认值。因为两者恰好都是 200000000，
**数值看起来完全正常** —— 只有 `limit_source` 标签暴露了问题。若运维写
`2.5亿`，会静默拿到默认的 2 亿，即一个**错误的限额且没有任何报错**。

现支持 `亿`(1e8) / `万`(1e4)，与 `k`/`m`/`b`/科学计数法一致；无法解析的
输入显式报失败（调用方会记日志），而不是被当成 0 静默吞掉。

### count_tokens 估算被宿主翻译层丢弃，客户端仍收到 0 (0.9.33)

**症状**：0.9.32 之后插件本地确实算出了估算值
（`TestCountTokensDispatchNoLongerReturnsHardcodedZero` 通过），但 Claude Code 调
`POST /v1/messages/count_tokens` 收到的仍是 `usage.input_tokens: 0` —— 于是继续
不压缩历史，真正请求撞上游上限。

**根因**：插件声明 `ExecutorOutputFormats=["chat-completions"]`，因此宿主适配器
（`internal/pluginhost/adapters_executors.go` `translateExecutorResponse`）对插件
返回的 payload 执行：

```go
TranslateNonStream(outputFormat=openai, requestedFormat=claude, …)
```

这走的是 **openai→claude 响应翻译器**，它通过 `extractOpenAIUsage` 读
`usage.prompt_tokens`。0.9.32 返回的是 Claude 原生形状 `{"input_tokens":N}`，该
字段被翻译器忽略并**静默重写成 0**。宿主的 `TokenCount` 钩子只对原生执行器
（如 `ClaudeExecutor`，它直接以参数形式调 `TranslateTokenCount`）可用，插件执行器
走不到那条路径。

**修复**：`countTokensWireShape` 改为返回
`{"usage":{"prompt_tokens":N,"completion_tokens":0,"total_tokens":N}}` —— 与
`helps.BuildOpenAIUsageJSON` 完全一致，即所有 openai 兼容原生执行器使用的形状，
因此也是测试覆盖最充分的形状。

**验证**：新增 `scripts/verify-count-tokens-wire.sh`，把插件**真实**产出的 payload
送进 CLIProxyAPI 检出目录里**真实**的翻译器（插件模块无法 import 宿主 `internal/`，
所以必须两段式）：

```
ok   claude  usage.input_tokens = 4321
ok   gemini  usageMetadata.promptTokenCount = 4321
ok   openai  passthrough unchanged
ok   teeth   old shape still loses the count ({"input_tokens":4321} -> input_tokens=0)
```

最后一行是**带牙齿检查**：旧的错误形状必须仍被判定为失败，否则说明这个验证脚本
已经失去意义。这正是 0.9.32 漏掉的一类 bug —— 插件侧单元测试全绿，数值仍然在
翻译层被丢掉，只有跑真实翻译器才能发现。

### 空回答误报成功 + 账号名退化为 workbuddy (0.9.32)

**修复 1：上游「空回答」不再被当成成功（改判 429 限流）**

被限流的账号会回 **HTTP 200 + 合法 SSE 框架但没有任何内容**（只有
`role` / `finish_reason` / `usage`）。旧代码把「至少有一个合法 JSON chunk」
当作成功：

- 非流式 `aggregateCompletion` 折叠出 `content:""` 的正常 `chat.completion`
  交给客户端 —— 用户看到「成功但什么都没有」，且账号不会被冷却，后续每个
  请求继续打到同一个被限流的账号；
- 流式 `pumpUpstreamStream` / `aggregateSSEWithCollector` 只统计 chunk 数量，
  同样放行只有框架的流。

现新增 `emptyAnswerError`（三处路径统一）：**只有框架、没有正文/推理/工具调用
的流判定为上游限流**，`chatErrorStatus` 把状态码映射为 **429**
（`rate_limit_error`），让 CPA 冷却该凭据并把重试路由到其他账号——而不是
502（那只会让客户端退避、账号仍留在轮转里）。

判定按「正文 / 推理 / 工具调用」三者任一存在即为有效输出，因此**纯工具调用
轮次（content 为空但有 tool_calls）不会被误杀**。

**修复 2：模型目录 `contextWindow` 全为 0 不再被当作正常目录**

限流时目录接口仍回 200、JSON 仍能解析，但每个条目的 `contextWindow` 都是 0。
旧代码照单全收，把「0 上下文」的目录**缓存 5 分钟**并下发给客户端（客户端因此
退回默认值、过早压缩上下文）。现新增 `discoveryContextDegraded`：只有**全部启用
条目**都拿不到上下文窗口才判定降级（个别条目缺字段是合法的），命中即返回
typed `*upstreamError{429}`，由调用方回退到该区域带真实上下文的静态目录，并在
面板记录原因。`callModelsAPI` 同时补上退避重试（700ms / 2s，上限 5s，复用既有
`Retry-After` 解析），阶梯比 billing 短——它在客户端的 `/v1/models` 路径上。

**修复 3：账号名不再退化成 `workbuddy`**

根因在宿主的保存语义：插件所有持久化都走 `host.auth.save`，而宿主是**从文件
JSON 重建 auth 记录**的（`internal/pluginhost/auth_callbacks.go`
`buildAuthFromFileData`）：

```go
label := provider                       // "workbuddy"
if email := metadata["email"]; … { label = email }
```

插件以前从不往文件里写 `email` / 账号名，于是**第一次生命周期写入**（禁用、
备注同步、保活刷新）之后，账号行标签就退化成 provider 名 `workbuddy`——尽管
凭据里的 nickname 一直都在。修复覆盖写入与读取两侧：

- 写入：`buildAuthFileJSONFromExisting` 现在把 `email` / `account_name` /
  `uid` / `enterprise_id` 写进文件（**仅在缺失时**，管理端手工改名优先）；
  `enrichAuthMetadata` 同步携带这些字段，使 RPC 路径与文件路径一致。
- 读取：`parseStored` 现在也接受顶层 `email` / `account_name`（嵌套与 flat
  两种形态都支持），修复前这些字段在每次重读时被丢弃——文件里明明有名字，
  面板却显示空。
- 单一来源：新增 `accountNameForAuth`（昵称 → 令牌 `email`/`upn`/`sub` 声明），
  `labelForAuth` 与元数据都由它派生，二者不可能再不一致。它**绝不返回
  provider 名**——那正是本症状。

**修复 4：发现我自己引入的排序 bug（测试抓到）**

空回答判定最初写在 `sseFramed` 加前缀**之后**，导致跨格式客户端
（Claude/Gemini/Codex 走 `data: ` 分帧）的**每一个正常流都被误判为空回答**。
`chunkHasModelOutput` 现通过 `stripDataPrefix` 同时识别两种线上形态，且两处
流式路径都在加分帧**之前**做判定。回归测试
`TestChunkHasModelOutputToleratesSSEFrame` 等 4 个用例锁定该顺序。

**修复 5：`count_tokens` 不再硬编码返回 0**

`Executor.CountTokens` 原先无条件返回 `{"input_tokens":0}`，等于告诉做上下文
预算的客户端（Claude Code 每轮请求前都会调 `POST /v1/messages/count_tokens`）
「prompt 不占空间」——客户端因此从不压缩历史，真正请求时才撞上游上限失败。
新增 `token_count.go` 做**本地估算**（不请求上游、不消耗积分）：按
「字符数 / 3」估算并**刻意偏向高估**（高估只是让客户端略早压缩；低估会让请求
直接失败），只统计模型真正看得到的内容（消息文本、system、工具名/描述/schema），
跳过 `role`/`type`/`id`/采样参数等协议字段，避免工具密集请求被虚高。空/畸形
payload 保底返回 1（客户端把 0 当作「未知」）。

**测试**：新增 `empty_answer_test.go`（空回答三路径 + 目录降级 + 分帧回归）、
`account_name_test.go`（身份解析 / 保存往返 / 管理端改名优先）、
`token_count_test.go`（非零保证 / 随内容增长 / 结构字段不计入 / RPC 入口回归）。
新增 `hostStreamTestOverride` 测试缝，使流式数据面可在无宿主进程下被驱动。
覆盖率 62.2% → 65.6%。

### 配置解析、错误分类与测试覆盖修复 (0.9.31)

**修复 1：配置解析改用 YAML 解析器（三个真实 bug）**

`configure()` 原先用 `strings.HasPrefix` 逐行解析，实测存在三处静默错误：

- `checkin_auto: true # 开启` → 解析为 **false**（行内注释被当成值的一部分，
  布尔开关实际被关闭，用户以为开着）
- `login_region: "intl" # 海外` → 解析为 **cn**（引号+注释组合不匹配，
  新登录走错区域）
- `models_cn: a, b # 注释` → 把注释当成模型 ID（`b # 注释` 被注册为真实模型）

改为一次 `yaml.Unmarshal` 到 `scalarConfig`（与 `parseCustomStaticModels` 同源），
注释/引号/嵌套全部由 YAML 库正确处理；嵌套同名键也不会再被误读为顶层配置。

**修复 2：错误分类改为类型化错误 + 429 重试**

`isTransientBillingErr` 原先靠 `err.Error()` 字符串前缀匹配，被上层包装后即失配
（`"billing call failed: http 500"` 不再重试），且 **429 完全不重试**——最该退避的
限流反而直接失败。改为 `*upstreamError`（`errors.As` 判定）+ `Retry-After` 解析
（秒数与 HTTP-date 两种格式，上限 30s 防止恶意值拖死刷新）。

**修复 3：测试覆盖率 38.5% → 62.2%**

新增 lifecycle/cache/checkin/keepalive/growth/management/authfile/panel 等
测试；为此新增 `hostRPCTestOverride` 测试缝，使 host.auth.* 状态机可被驱动。
过程中测试还发现并修复了三个产品 bug：

- `performCheckinCall`/`performTrialCall` 无条件写 `success=true`，覆盖了上游
  明确的 `success:false`（"您今日已签到"），导致软失败被当成成功上报
- 自动点亮循环只跳过 `claimed` 未跳过 `locked`，会为未解锁任务上报行为事件
- `claimable` 计算未排除 `locked`，可能领取未解锁任务

**修复 4：`usage_config.go` → `config.go`**

该文件在 0.9.30 移除用量上报后已全是配置解析逻辑，改名反映实际职责。

**修复 5：面板拆分 + 孤儿路由**

- `panel.html` 1232 行 → 264 行（标记+样式+主题引导），逻辑移入 `panel.js`
  （978 行）。`panel.js` 已注册为 resource 路由（宿主按精确路径分发，不注册会 404）。
- `/keepalive/status` 不再无人调用：设置弹窗显示"上次保活：N 成功 · M 异常"。

### 删除插件侧用量直推（重复上报）(0.9.30)

- 背景：插件一直把每条请求 POST 到 CPAMP `/v0/management/usage/import`。
  实测（v7.3.7 实机）确认这**完全多余**：CPA ≥ v7.2.146 / v7.3.x 会把插件
  executor 的用量交给宿主自己的 usage reporter → 宿主用量队列
  （`/v0/management/usage-queue`），而 CPAMP 的 collector 正是拉取该端点。
  结果：一次聊天请求在 CPAMP 里落 **两条**记录（宿主一条 + 插件直推一条，
  source/request_id/时间戳不同，NDJSON 导入不会去重）。
- 修复：**整段删除**插件侧上报，不留开关。`usage.handle` 保留为无副作用的
  能力确认（宿主调用它时用量已入库），`publishUsage`/`forwardUsageToCPAMP`/
  SSE 用量采集器、`usage_report_url`/`usage_report_key`/`usage_report_bridge`/
  `legacy_usage_fallback` 配置、CPAMP 密钥探测与密钥文件读取、`management_key`
  （它只是该上报的密钥兜底）全部移除。残留旧配置键会被静默忽略，不影响启动。
- 面板状态条不再显示"用量上报/未找到 CPAMP 密钥"（无需任何配置）；
  配置摘要固定为 `usage_report=host (由 CPA 上报)`。
- 实测验证（v7.3.7 实机，修复后）：一次请求 → 宿主队列 1 条、插件直推 0 条。

### Automation switches: display and runtime now agree (0.9.29)

- Reported symptom: checkin_auto and lifecycle_auto appeared swapped.
  Root cause (reproduced on a live host): the host's native plugin-config
  editor renders an absent boolean key as OFF (`checked={value===true}`),
  while the plugin treats an absent key as its default — ON. A fresh install
  therefore showed both switches OFF while both were actually running, and
  after toggling one, the page read as if the other had changed.
- Opening the panel's settings modal now materializes the effective value of
  every missing switch into the stored config (same behaviour, no side
  effects), so the native page, the panel and the runtime always agree.
- Verified three-way consistency on a live v7.3.7 host: with
  lifecycle_auto=false and the rest default, the stored config, the plugin's
  reconfigure log and the dashboard payload all report the same values.

### Panel-managed automation settings (0.9.28)

- All automation toggles now live in the panel: a new "⚙ 自动任务设置" modal
  exposes 自动签到 / 积分生命周期 / 成长任务自动点亮 / 猫猫旅行 / 令牌保活,
  each with a plain-language explanation. Changes are written straight to
  CPA's plugin config (`PATCH /v0/management/plugins/workbuddy/config`) and
  take effect immediately — CPA persists config.yaml and re-applies it,
  which triggers plugin.reconfigure; no restart needed (verified end-to-end
  on a live host: patch → persisted → reconfigure log → dashboard update).
- Only touched keys are patched, so advanced YAML-only options
  (usage_report_*, management_key, models, scheduler_mode, ...) are never
  overwritten by the panel.
- `travel_auto` joins `growth_auto` as an independent switch; the dashboard
  payload reports all five switches and the status strip shows a compact
  "自动: 签到/生命周期/成长任务/猫猫旅行/保活" summary.
- The old inline check-in checkbox was removed (its state is now part of the
  settings modal and the status strip).

### Anti-abuse device identity + growth automation (0.9.27)

Capabilities adapted from workbuddy2api-hub (ardeyouxipianyi, MIT) and
Sliverkiss/workbuddy2api, verified against the endpoints this plugin uses:

- **Stable device fingerprint** (`fingerprint.go`): every outbound request now
  carries `X-Machine-ID` / `X-Session-ID` / `X-Request-ID` derived from the
  account UID plus fixed salts. One account presents exactly one virtual
  device across requests and restarts; different accounts are unrelated — the
  upstream can no longer correlate accounts through drifting or shared
  machine identifiers. Applied to chat, billing, growth and refresh calls.
- **Growth task lighting** (`growth_events.go`): tasks are advanced by
  reporting behaviour events to `/v2/report`, not by calling task endpoints.
  Implements all 14 lightable task families (canvas/template/expert/team/
  skill/automation/playbook/lighthouse/skin/chat/glm-chat/night-cat) with:
  rotating distinct expert/team ids (the upstream dedupes by
  `(eventCode, id)`, so a repeated id never advances progress), a 23:00–08:00
  gate for 夜猫子, and honest "desktop only" reporting for the tasks that
  require real client actions (deep links surfaced to the panel instead of
  forged events).
- **Buddy travel** (`growth_travel.go`): full 猫猫旅行 loop — status → claim
  when arrived → depart when idle, with the destination id fetched from
  `/travel/config` (depart rejects an empty body).
- **Automation wiring**: both run automatically on the 09:00/21:00 ticks
  after check-in, plus manual panel buttons (一键点亮 / 猫猫旅行) and
  `growth_auto` config key (default on).
- Endpoints: `POST /tasks/light` (background run, poll `/tasks/status`) and
  `POST /tasks/travel`.

### Foolproof configuration (0.9.26)

- Zero config is now the documented happy path: install and it works.
  checkin_auto, lifecycle_auto and token_keepalive all default to on; the
  panel shows a one-line status strip (server time + usage-report state).
- The visual plugin-config editor now exposes only six fields a normal user
  may plausibly touch (checkin_auto, lifecycle_auto, login_region,
  login_platform, models_cn, models_intl_global), each with plain-language
  "recommended default" copy. Advanced keys (scheduler_mode, usage_report_*,
  management_key, models, models_intl, legacy_usage_fallback) stay fully
  functional from YAML — the editor PATCHes only touched fields, so hidden
  keys are never overwritten.
- Boolean parsing accepts every spelling users actually type: true/True/TRUE,
  yes/YES, on/On, 1, 开启, quoted or not. login_region also accepts 海外/国际.
- Every configure logs a one-line effective-config summary (deduplicated
  across the host's repeated register calls, no key material) so a
  deployment can verify its settings at a glance.
- Panel status bar shows whether CPAMP usage reporting is on and where it
  points, or why it is off — no more silent disable.

### Plugin-layer management key gate removed; panel writes work again (0.9.25)

- Root cause of "刷新数据 / 签到 / 导入" failing with *invalid management key*
  while the panel itself rendered: the plugin enforced its own
  `management_key` on every mutating call, and the credential embedding UIs
  actually forward is the CPA management secret (CPAMP substitutes its saved
  CPA key after verifying the CPAMP admin key; CPAMC sends the CPA secret).
  With a CPAMP-era management_key configured, the two never matched — GETs
  passed, every POST 403'd.
- The official plugin spec keeps management.handle solely behind the host
  management middleware, and the official examples implement no plugin-side
  key check. That second gate is gone. management_key / WB_MANAGEMENT_KEY
  remain valid configuration (they still feed the CPAMP usage-key fallback).
- The per-IP token bucket stays as an abuse guard with capacity raised to 30
  (1/s refill) so a bulk "check in all" run no longer trips it.
- Verified on a real v7.3.7 host with deliberately misaligned keys:
  POST /refresh now returns 200 (previously 403); unauthenticated requests
  are still rejected by the host middleware (401).

### Panel auto-auth inside CPA Manager Plus (CPAMP) restored (0.9.24)

- Root cause of "management key 无效或缺失" when the panel is embedded in
  CPAMP: Manager Plus v1.13 moved its stored management key to `enc::v2::`
  obfuscation and its proxy now only substitutes the saved CPA key when the
  caller presents a verified CPAMP admin key (commit 3734f18, "prevent
  plugin resource auth elevation"). The panel only understood `enc::v1::`,
  failed to decrypt, and sent the raw ciphertext as a Bearer key — a
  guaranteed 401. Older CPAMP builds proxied unconditionally, which is why
  it used to work.
- The panel now reads both UIs: CPAMC's `cli-proxy-auth` object AND CPAMP's
  `managementKey` store in `enc::v2::` / `enc::v1::` / plaintext form
  (including JSON-string unquoting), and refuses to send undecrypted
  `enc::` remnants. Same fix applied to the qoder and trae panels.
- Round-trip tested against the exact storage formats of CPAMC and CPAMP
  (v1/v2/plaintext/object); unknown formats degrade to the manual key box
  instead of a guaranteed 401.

### Spec compliance: executor errors carry the upstream HTTP status (0.9.23)

- The plugin development spec requires executor failures to report the
  upstream HTTP status inside the RPC error envelope (`http_status`).
  WorkBuddy never set it, so every upstream 401 (session dead), 403 (model
  permission), 429 (rate limit) and 5xx surfaced to clients as an
  undifferentiated HTTP 500 — clients treated it as a transient gateway
  outage and retried a dead account with backoff forever, and CPA's core
  error classification never saw the real status.
- `executor.execute` and `executor.execute_stream` now return an
  `upstream_error` envelope carrying the real status; malformed upstream
  streams map to 502. Verified on a real v7.3.7 host: a chat request with
  an invalid token now returns HTTP 401 with `type=authentication_error`,
  `code=invalid_api_key` (previously HTTP 500 `server_error`).
- Registration logs `workbuddy: plugin vX.Y.Z registered (pluginabi
  schema 6)` — one grep in the CPA log proves which build is actually
  running after a store update (remember: updating a loaded plugin
  requires a CPA restart; the store refuses to overwrite in-memory .so
  files with `plugin_update_requires_restart`).

### Align native quota with the full v7.3.7 contract (0.9.22)

- SDK upgraded v7.3.2 → v7.3.7 (builds unchanged otherwise).
- Quota fetch responses now carry `summary` metrics (剩余积分 / 已用积分 /
  积分总额) alongside the subscription plan and groups, matching what
  management UIs render for normalized quota.
- `serverTimeOffsetMs` is derived from the upstream billing `Date` header so
  reset countdowns render against upstream time.
- `codebuddy` (bare) joins the supported quota providers so accounts still
  carrying the pre-merge provider id are quota-capable before adoption.

### Why the CPA main panel shows no WorkBuddy quota refresh (investigated)

- CPA server (v7.3.7) exposes complete plugin quota endpoints
  (`/v0/management/quota/providers|fetch`, `/plugins/:id/quota`) and the
  auth-files list marks plugin accounts `supports_quota: true`.
- The Management Center UI (CPAMC, v1.24.0) never calls any of them: its
  quota page is a hardcoded per-provider registry (antigravity, claude, codex,
  devin, xai, kimi, meta) that speaks to upstreams via the api-call
  passthrough. Plugin accounts therefore get no quota UI there regardless of
  plugin version — the WorkBuddy panel (刷新数据) remains the quota refresh
  surface until CPAMC grows plugin quota support.

### Fix doubled CPAMP configure probe (0.9.21)

- v1.0.3 shipped a `configure()` that resolved the management key and called
  `resolveUsageReport` twice (merge artifact). With a wrong usage key this
  doubled the failed-auth probe attempts per config load against the management
  endpoint, feeding CPA's brute-force IP ban (5 failures → 30 min) for exactly
  the "shared management key" setups the fallback targets.
- Panel behavior around the management-key prompt is unchanged; the prompt
  itself means the panel found no key (standalone open, or a fetch returned
  401 and the cached key was cleared).

### CPAMP reporting survives proxy deployments (0.9.20)

- Root cause of "CPAMC works but CPAMP never receives usage" in deployments
  where CPA has `proxy-url` configured: the host bridge applies the upstream
  proxy to every bridged request with no loopback bypass, so the configure-time
  probe and all usage imports to `127.0.0.1:18317` were routed to the proxy and
  died silently, permanently disabling reporting.
- CPAMP probe and imports now use a direct transport by default (management-
  plane traffic to a local service). Set `usage_report_bridge: true` to route
  via `host.http.do` again when no proxy is configured. AI upstream traffic is
  unchanged and still goes through the bridge.
- `management_key` (config) is now the final usage-key fallback after
  `WB_MANAGEMENT_KEY`, so single-key deployments work without extra env vars.
- Reporting that stays disabled is now visible: configure logs explain a
  missing key, a failed probe, or an ignored invalid URL instead of failing
  silently.

### WorkBuddy naming, native quota, and CPAMP management compatibility

- New credentials now use `workbuddy-CN-<uid>.json`, `workbuddy-Global-<uid>.json`,
  and `workbuddy-intl-<uid>.json`; legacy names remain readable and adoption
  deduplicates by UID plus detected realm without deleting host-owned records.
- Added CPA native `QuotaProvider` RPC support. Native quota fetch exposes plan,
  aggregate credits, and per-package reset windows; native reset explicitly reports
  unsupported. The custom management refresh remains the lifecycle-aware full refresh.
- CPAMP URLs now accept the default explicit `:18317` port. Usage probes and posts
  send both `Authorization: Bearer` and `X-Management-Key`, and usage forwarding
  prefers `UsageRecord.AuthIndex` over `AuthID`.
- Dedicated CPAMP keys remain higher priority; `WB_MANAGEMENT_KEY` is only a final
  compatibility fallback. The bcrypt `remote-management.secret-key` is never used
  as a Bearer token. Plugin management accepts either supported key header, while
  only rejected credentials consume the failure rate-limit bucket.

### Unified CN / overseas channel routing

- Centralized credential channel detection: explicit `auth.region`, normalized
  auth domain, and domain-less JWT issuer fallback now consistently select CN,
  Intl, or Global for chat, billing, refresh, models, lifecycle, and tasks.
- Realm-aware request headers now include the matching Origin/Referer, product
  identity, tenant metadata, and `X-Auth-Refresh-Source: plugin` on refresh.
  Refresh tokens remain scoped to refresh calls and are never sent with chat.
- OAuth polling now treats only the documented token/account pending codes
  (`11217` / `12151`) as pending; terminal business and HTTP errors clear the
  login state immediately.
- Intl credentials use `workbuddy-intl-<uid>.json` to avoid same-UID collisions
  with CN/Global credentials. Added regression coverage for routing and headers.


### Custom static models by channel

- The `models` config now supports natural channel grouping (`models.intl_global`
  and `models.cn`) with simple model ID string lists (e.g.
  `[deepseek-v4.1-flash, gpt-6-astra, gpt-5.6-sol]`) or full objects.
- `intl_global` is shared by both Intl and Global accounts, while `cn` targets
  CN accounts only. Legacy flat arrays with `channel` / `realm` remain fully
  compatible.
- Entries are merged into the matching static and discovered catalogs, marked
  `UserDefined`, and replaced on every reconfigure. Explicit pins remain
  authoritative full-list overrides.

### Global and Intl static model catalog

- Global and Intl built-in static fallbacks now contain only `hy4-preview`.
- `deepseek-v4.1-flash`, `gpt-6-astra`, and `gpt-5.6-sol` are no longer built-in;
  add them explicitly through the custom `models` array when desired.
- Intl still skips the retired model-discovery endpoint; `models_intl` remains
  the explicit override for accounts that need a different model list.

### CPA lifecycle and protocol hardening

- Quiesce now drains host callbacks and asynchronous streams; new work is rejected
  while stream cleanup remains allowed.
- Standard SSE multiline data frames are joined, `[DONE]` stops trailing input,
  invalid JSON is dropped, and usage accepts numeric strings/nested token fields.
- Global exhausted credentials remain as disabled records for CPA management cleanup;
  the plugin never removes auth files directly.

### Intl model discovery policy

The plugin now uses only the built-in `hy4-preview` static Intl catalog by
default. The historical `/console/enterprises/personal/models` route returns a
gateway 500 even for valid Intl credentials, including when attempted through
the Global catalog; Intl chat and authorization remain on `codebuddy.ai`.
Additional models should be added through the custom `models` array or an
explicit `models_intl` full-list override. CN and Global discovery are
unchanged.

## 0.9.10

### Fix bridged HTTP status decode — dynamic discovery always saw "status 0" (repo v0.12.49)

Root cause of the user-visible `发现失败: models API status 0` on BOTH realms
with a healthy upstream: the host http bridge marshals the tag-less
`pluginapi.HTTPResponse` struct, so the status rides as PascalCase
`"StatusCode"`. The plugin decoded only `"status_code"` — Go's
case-insensitive field match rescued `Headers`/`Body` but NOT `StatusCode`
(underscore vs no underscore), so every bridged response decoded with status
0 and `callModelsAPI` treated even a genuine upstream 200 as a failure and
fell back to the static catalog. Chat (stream path) and billing (lenient
`>= 400` checks + body parsing) were unaffected, which is exactly why only
model discovery appeared broken. Confirmed against the official wire: the
console models endpoint exists on all three domains and answers Bearer auth
(401 on a bogus token, 302 login redirect without one), while the v2 gateway
has NO models route — the console endpoint remains the only dynamic source,
as also evidenced by upstream tools (OmniRoute ships a static model table;
cockpit-tools only does auth/billing).

- **Dual-shape decode** (`host_bridge.go`): `hostHTTPDo` now decodes both
  `"status_code"` (documented) and `"StatusCode"` (tag-less marshal via
  `decodeHostHTTPDoResult`). A genuine status 0 — HTTP has no such status —
  falls back to one direct request so callers see the real status or the real
  transport error instead of a bogus 0.
- **Richer discovery errors** (`models.go`): non-200 now reports
  `models API status %d from <url>: <body snippet>` (control chars stripped,
  200-byte cap) — a login redirect, auth wall, or server fault is visible
  from the panel hover instead of a bare code.
- **UA refresh** (`main.go`): `CLI/2.63.2 CodeBuddy/2.63.2` →
  `CLI/2.108.1 CodeBuddy/2.108.1`, matching the current official CLI
  (parity with OmniRoute's constant; the gateway rejects missing/stale
  client identification with 403/code 10085).
- Tests: dual-shape decode (PascalCase / lowercase / absent / malformed),
  direct-path status surfacing over httptest.

## 0.9.9

### Model-source diagnostics: the model list now explains itself (repo v0.12.48)

Follow-up to 0.9.8 (v0.12.47). Two user questions — "why didn't dynamic
discovery pick up the new model" and "why does the codebuddy intl realm only
show hy4" — had the same blind spot: the discovery-to-static-fallback
decision was completely invisible, so a realm stuck on its (thin) static
catalog was indistinguishable from discovery legitimately serving a short
list.

- **Per-realm diagnostics trail** (`models.go` / `main.go`): the realm cache
  entry now records WHERE the advertised list came from (`discovery` / `pin`
  / `static`), when it was fetched, and the last discovery failure reason.
- **Throttled failure logging**: a discovery failure logs
  `models: realm=<r> discovery failed (<reason>) — serving static catalog
  (N model(s))…` immediately and at most once per minute thereafter
  (`model.for_auth` can fire per models query; the old code swallowed the
  error entirely).
- **Panel "模型" row** (`panel.go` / `panel.html`): each account card shows
  the realm's model source — `动态发现 N 个模型 · X分钟前`,
  `配置钉住 N 个模型`, or `静态兜底 N 个模型 · 发现失败: <reason>`
  (hover for the full reason). A stale `models_cn/models_intl/models_global`
  pin — which silently overrides discovery for the realm — is now visible
  too.
- No change to model resolution itself: the
  pin → discovery (5-minute cache) → static-catalog chain is identical to
  0.9.6–0.9.8.
- Tests: discovery-failure state recording + reason surfacing; discovery
  success clearing the failure; pin short-circuit (discovery must not be
  called) + pin state recording.

## 0.9.8

### Promote new upstream models beyond the cli agent list + DeepSeek V4.1 Flash (repo v0.12.47)

- **Discovery promotion** (`models.go`): the cli agent's model IDs stay the
  ordered base, but any ENABLED `data.models` entry missing from that list is
  now PROMOTED (and logged) instead of silently dropped. Tencent adds new
  models to `data.models` while the cli agent list lags — the exact shape of
  the deepseek-v4.1-flash rollout on 2026-09-10 — and the old cli-only filter
  made the plugin trail the official client on every launch.
- **cli agent missing/renamed no longer hard-errors** discovery: enabled
  `data.models` alone still produce the list (before: error → stale static
  fallback).
- **`rawJSONI64`**: `contextWindow` / `maxTokens` now tolerate number,
  numeric-string and null shapes.
- **CN static fallback** gains `deepseek-v4.1-flash` (1M context; official
  DeepSeek launch-partner announcement, same evidence bar as hy4-preview).
  Intl/Global catalogs unchanged (no direct upstream evidence yet).
- Tests (`models_discovery_test.go`): cli base order, disabled skip,
  promotion, cli-missing resilience, rawJSONI64 matrix.

## 0.9.7

### Discovery-first model output made explicit (repo v0.12.20)

- **Config descriptions for `models_cn` / `models_global` / `models_intl`
  rewritten**: "Leave empty (recommended)" now leads. An empty pin IS the
  dynamic contract - each credential advertises exactly the model list its own
  upstream token returns (per-realm discovery endpoint, 5-minute cache,
  disabled models filtered), so upstream-unknown models are never advertised
  and can never be routed there. Pins stay as a manual override for edge
  cases; filling one with another realm's IDs is the one way to reintroduce
  the 11102 wrong-routing class, and the config UI now warns about that.
- No behavior change; model resolution is identical to 0.9.6 (repo v0.12.19).

## 0.9.6

### Per-realm static catalogs + user-pinnable credential model output (repo v0.12.19)

- **Per-realm static model catalogs** (`models.go`): the shared CN-flavored
  fallback (`wbModels`) no longer answers `model.for_auth` for Intl/Global
  credentials. A false negative (supported model missing from the fallback)
  heals via dynamic discovery or the new pins; a false positive (advertised
  but not registered upstream) is a hard `400 code 11102 "model [...]
  service info not found"`, so `staticModelsIntl` / `staticModelsGlobal`
	carry the four-model Global/Intl fallback (`hy4-preview`,
	`deepseek-v4.1-flash`, `gpt-6-astra`, and `gpt-5.6-sol`), while CN brand
	models (`deepseek-v4-*`, `glm-*`, `kimi-*`, `minimax-*`, `hy3*`) stay out
	of those catalogs. Community-observed intl claude/gpt/gemini IDs are
	deliberately NOT hardcoded: their exact upstream IDs could not be verified
	without a live intl token.
- **`models_cn` / `models_global` / `models_intl` config pins** (the
  "把支持哪些模型写进凭证产出" contract): comma-separated upstream model IDs
  per realm; when set, the credential's model output is exactly that list and
  dynamic discovery is skipped for the realm (deterministic, no 15s discovery
  latency). Missing key on reconfigure resets the realm to
  discovery + static fallback. Known IDs reuse static-catalog metadata;
  unknown IDs get generic metadata (the user pinned them deliberately).
- **Fallback chain** for every credential is now
  pinned config → per-realm discovery (realm-keyed cache, v0.12.18) →
  per-realm static catalog.
- **11102 error hint** (`chat_error.go`): the static-catalog branch of the
  bilingual hint now renders the account's OWN realm catalog (labeled
  "static INTL/GLOBAL/CN catalog (known-good for this realm …)") instead of
  the old CN list with a "may not match this realm" disclaimer.
- Tests (`models_realm_test.go`): realm catalogs may not contain CN-only
  models; pinned-config parsing (quoting / YAML flow lists / dedup);
  config_yaml envelope → realm pins end-to-end; pins win and skip discovery;
  discovery failure falls back to the realm's own catalog; hint rendering
  per realm.

## 0.9.5

### Realm-aware model discovery + 11102 actionable error (repo v0.12.18)

- **Intl chat 400 `code 11102 "model [...] service info not found"`** (user
  report on the intl credential): chat routing itself was correct (Intl tokens
  hit codebuddy.ai), but model DISCOVERY was not — `callModelsAPI` only
  special-cased Global and sent Intl tokens to the CN endpoint
  (copilot.tencent.com), whose answer (or the CN-flavored static fallback)
  then advertised CN-only models such as `deepseek-v4-flash` to Intl accounts.
  The Intl gateway rejects those with 11102.
  - `modelsEndpointFor(realm)`: per-realm discovery URL + Origin/Referer —
    cn → copilot.tencent.com, global → workbuddy.ai, intl → codebuddy.ai
    (with the IDE header set, parity with `applyRealmHeaders`).
  - `realmForStorage`: classifies the auth blob (nested/flat domain, region
    field, JWT-iss fallback) into cn|global|intl.
  - `dynamicModelsCache` re-keyed per realm — a single shared entry let one
    realm's answer satisfy `model.for_auth` for accounts on another realm.
- **11102 → bilingual actionable error** (`chat_error.go`): the executor
  error paths (non-stream, sync stream, async pump) now detect the 11102
  model-catalog rejection and rewrite it into a message naming the realm and
  its best-known model catalog (cached realm discovery; the static fallback is
  explicitly labeled as CN-flavored and possibly wrong). Non-11102 failures
  keep the historical `upstream <status>: <payload>` shape.
- Tests: `models_realm_test.go` (endpoint routing, storage classification,
  per-realm cache isolation, hint rendering, 11102 rewrite + passthrough
  guard). Verified live: 401 passthrough shape unchanged, `/v1/models`
  unaffected, upstream probes (codebuddy.ai answers 401 auth-first with an
  invalid token — the user's 400/11102 proves the request reached the model
  catalog layer with a valid one).

## 0.9.4

### Intl billing gateway fix + single OAuth entry (repo v0.12.10)

- **Intl 401 fix** (`billing.go`): a successful Intl (codebuddy.ai) login was
  immediately followed by `parse failed: invalid character '<' (body: <html>
  ... 401 Authorization Required ...)` in the panel. Root cause:
  `billingBaseFor` routed check-in / meter calls for Intl accounts to the CN
  gas station (`www.codebuddy.cn`), whose APISIX gateway rejects Intl Bearer
  tokens with an HTML 401 page. Intl accounts now hit
  `billingBaseIntl = https://www.codebuddy.ai` (verified: the meter endpoints
  exist there and answer with business JSON for valid tokens), and
  `billingHeaders` applies the Intl IDE client header set + drops
  `X-Requested-With` (parity with `applyRealmHeaders`).
- **Single OAuth entry restored**: the v0.12.10 per-region login-page menus
  (CN 登录 / Intl 登录) are removed per user feedback — the plugin is back to
  one OAuth entry whose realm follows the `login_region` config dropdown
  (STICKY). `startLoginWithRegion` stays as the pinned-region helper.
- Global (workbuddy.ai) accounts unchanged: panel import only (no upstream
  OAuth flow).

## 0.9.3

### Per-region self-serve login pages (repo v0.12.10)

- New `login_pages.go` — the management UI sidebar gains **CN 登录** /
  **Intl 登录** pages (plugin-declared menus, rendered in an iframe). Each
  page starts a region-PINNED login flow (`login_start?region=…`), opens the
  upstream authorization URL in a new tab and polls `login_wait` until the
  flow completes, then persists the credential via `host.auth.save`. CN and
  Intl logins can now run concurrently without touching the global
  `login_region` config — which previously allowed only one region at a
  time and therefore only one OAuth menu entry per plugin.
- `oauth.go` — `handleStartLogin` splits into a thin wrapper plus
  `startLoginWithRegion(raw, region)`: the host RPC keeps using the global
  `login_region`; the login pages pin their own realm. The poll handler is
  reused verbatim by `login_wait` (no duplicated token logic).
- Isolation: `login_wait` only consumes states it created itself
  (`selfServeStates`), so it can never race the host-driven poller; terminal
  results are cached per state and concurrent page retries are coalesced.
- Panel menu renamed `WorkBuddy` → `Dashboard` (the UI groups multiple
  plugin menus into a drawer, where the old label was redundant).
- Global (workbuddy.ai) accounts unchanged: no OAuth flow exists upstream,
  they keep entering via panel import (noted on the Intl login page).

## 0.9.2

### Foreign-credential defense (repo v0.12.9)

- `credits_handler.go` — `handleImportAuth` now rejects payloads whose
  explicit `type`/`provider` names another plugin (e.g. a qoder or trae
  auth file). parseStored only requires an accessToken, so foreign
  credentials used to import cleanly and then 401 against the wrong
  upstream forever (APISIX HTML page surfaced as 'parse failed: invalid
  character <'). The rejection names the owning plugin. Untyped flat
  exports (legacy CPA-Manager-Plus files) keep importing.
- `host_auth.go` — `hostAuthList` gains a content guard: a file with our
  filename prefix but an explicit foreign type in its body (e.g. a qoder
  auth saved under a workbuddy- name by a third-party tool) is skipped
  with a log line instead of being listed/executed by this plugin.
  Entries without a type field stay eligible — the filename prefix
  remains their only discriminator.
- `main.go` — parse ownership guard hardened: the host rewrites an empty
  req.Provider to the POLLED plugin's own identifier, so the old
  EqualFold(req.Provider, ...) check was always true while being polled —
  the first-polled plugin (qoder) claimed every type-less generic
  credential on disk. Type-less files are now claimed only by filename
  family; declared legacy types (codebuddy/codebuddy-cn/codebuddy-intl)
  remain accepted. Symmetric fixes ship in qoder 0.8.4 (same hole) and
  trae 0.12.8 (guard was missing entirely).


## 0.8.2

### Concurrency + lifecycle hardening

- `lifecycle.go` — P0-2: `reconcileOneAccount` now routes credits fetch
  through `cachedAccountDetails(force=true)` so singleflight serializes
  concurrent writers, eliminating a Load→Store race that could clobber
  newer plan/checkin values.
- `lifecycle.go` — P1-4: Global `lifecycleDelete` now requires a second
  `fetchUserResource` confirmation before deleting. Prevents transient 402
  from irreversibly removing an account.
- `checkin.go` — P1-5: after a successful checkin the credits cache is
  refreshed immediately (was only updating the checkin field). Panel now
  shows updated balance without waiting for the async reconcile pass.
- `cache.go` — P1-1 documented trade-off: force=true callers still join
  singleflight (skipping would re-introduce P0-2).
- `main.go` — P0-5: `scheduler_mode` ConfigField description now warns that
  `off + lifecycle_auto=false` leaves exhausted accounts routable.

## 0.8.1

### Bug fixes + compliance polish

- `keepalive.go` (new) — daily 22:00 access-token refresh to prevent Keycloak
  offline-session expiry; reuses `schedulerLoop`, routes via `host.http.do`,
  uses CPA native `disabled` field for session-dead auths.
- `models.go` — fix `filterExcludedModels` slice aliasing that corrupted
  `dynamicModelsCache` (P0).
- `billing.go` — route all billing API calls through `hostHTTPDo` (was missed
  in v0.7.0); improve "parse failed" error to include a redacted body snippet.
- `checkin.go` — avoid double `fetchCheckinStatus` in classify already-branch.
- `billing.go` — `performCheckinCall` now sets `success=true` as bool to avoid
  downstream type-mismatch when upstream returns a string.
- `host_auth.go` — fresh slice in `hostAuthList` to avoid aliasing RPC response.
- `oauth.go` — route `handleRefreshAuth` via `hostHTTPDo` (last path still on
  `sharedHTTPClient()`); make OAuth error messages actionable.

## 0.8.0

### Refactor — community-grade file layout

完成 v0.7.0 合规改造后的代码组织大重构，把两个超大主档拆成单一职责的
小文件，对齐 CPA 原生 plugin 案例的"一个能力一个文件"原则。

**File splits (main.go 2940 → 809, management.go 2263 → 349, lifecycle.go 980 → 535)：**

- `redact.go` (49) — redactSecrets + 4 个 regex + truncate
- `usage.go` (242) — handleUsage + publishUsage + forwardUsageToCPAMP + sseUsageCollector
- `payload.go` (469) — prepareUpstreamBody + 4 个 InPlace mutator + 4 个 legacy 包装
- `stream.go` (452) — streamEmit/Close + pumpUpstreamStream + collectUpstreamStream + aggregate*
- `models.go` (443) — callModelsAPI + fetchDynamicModels + resolveUpstreamModel + alias 反解
- `oauth.go` (240) — handleStartLogin/PollLogin/RefreshAuth + newLoginClient + doJSON
- `host_bridge.go` (388) — hostHTTPDo/DoStream/Read/Close + hostStreamReader + Direct fallbacks
- `billing.go` (486) — billing API + fetch* + perform* + JSON helpers
- `cache.go` (183) — accountCache + accountDetailFlight singleflight + prune
- `host_auth.go` (73) — hostAuthList/Get/GetBundle (host auth-store RPC)
- `usage_config.go` (202) — configure + resolveUsageReport + probe* + config vars
- `checkin.go` (515) — handleManualCheckin + runAutoCheckin + schedulerLoop + classify/execute/summarize
- `credits_handler.go` (285) — handleImportAuth/CheckinConfig/ClaimTrial/SelectAuth/CreditsQuery
- `panel.go` (266) — buildDashboardEx + summarizeCredits + servePanel + panelHTML
- `policy.go` (188) — lifecycleAction decisions + displayNote + labelForAuth
- `authfile.go` (299) — authFileNameFor/sanitizeUIDForFileName/hostAuthPersist/deleteAuth + path safety

**保留的小文件**：`scheduler.go` (138)、`active_auth.go` (158) — 本来就够小。

**文档（社区标准）：**

- `README.md` — 英文版，Features / Quickstart / Configuration / Lifecycle / Development / License
- `README_CN.md` — 中文版
- `LICENSE` — MIT
- `Makefile` — build / test / lint / clean / release / tag 目标
- `.gitignore` — 忽略 `*.so` / `*.h` / `bin/` / `dist/`
- `docs/architecture.md` — 模块图 + 数据流 + 关键设计决策 + 与 CPA 的集成点
- `docs/development.md` — 本地构建 / 测试 / 调试 / 发布流程
- `docs/definition-of-done.md` — v0.8.0 验收标准（量化可测）

### Lint / style

- `gofmt -l .` → 0 files
- `go vet ./...` → 0 issues
- `gocritic check ./...` → 0 issues（修复 policy.go 的 ifElseChain）
- `staticcheck` 真实代码问题 0（工具链版本噪音已过滤）

### Bug Fixes (carried over from v0.6.31 / v0.7.0)

本次重构完整保留了之前所有 bug 修复：
- UID 路径穿越白名单（authfile.go sanitizeUIDForFileName）
- refresh_token 不再泄露到 chat 上游（main.go backendHeaders）
- invalidateAccountCredits 数据竞争修复（值拷贝）
- handleManualCheckin early-already merge（不丢 credits/plan）
- configure 嵌套锁修复（parse-then-lock）
- scheduler_mode off 接通（handleSchedulerPick 读取配置）
- deleteAuth 调 clearActiveAuthIfMatch
- runAutoCheckin 串行改并发（sem=4）
- cachedAccountDetails singleflight
- panel.html XSS 修复（addEventListener + dataset）
- panel.html CSRF（fetch credentials:omit）
- redactSecrets 裸 JWT 兜底
- pumpUpstreamStream context cancel
- out[:0] 共享底层数组改新 slice
- 热路径 4 次 JSON 序列化合并为 1 次
- 冒泡排序改 sort.Slice
- usageReportConfigured/buildDashboard 死代码删除
- handleManualCheckin 三段拆分（classify/execute/summarize）
- management BasePath 缓存（register 时读取宿主注入）

### Tests

- 115/115 tests pass (`go test -race`)
- 新增 `TestSchedulerPick_OffMode_Defers` 覆盖 scheduler_mode=off 行为

## 0.7.0

### Compliance — CPA native patterns
本次大版本把「自建通道」全部替换为 CPA 官方提供的 RPC / 能力接口，
对齐 `sdk/pluginapi` 的设计意图。生产路径 100% 走宿主桥接，插件不再
绕过宿主审计 / request-log / transport policy。

- **所有上游 HTTP 调用走 `host.http.do` / `host.http.do_stream`**：
  - `models API`、`billing API`、`usage 上报`、`chat completions`（流式 + 非流式）
    全部从 `sharedHTTPClient().Do` 切到 `hostHTTPDo` / `hostHTTPDoStream`。
  - 宿主 request-log 现在能捕获插件的出站请求和原始响应（之前完全看不到）。
  - 宿主 transport policy（proxy、超时、连接池）对插件上游调用生效。
  - `sharedHTTPClient` 降级为 fallback 专用：仅当宿主桥不可用（单元测试 /
    老版本 CPA）时使用。新代码直接调用 `sharedHTTPClient` 视为合规 bug。
- **`hostStreamReader` 适配层**：把宿主桥的 32KB 任意字节块适配为 `io.Reader`，
  `bufio.Scanner` 的 SSE 行切分逻辑不变，pump / collect / aggregate 全部透明迁移。
- **`UsagePlugin` 能力声明 + `handleUsage` RPC handler**：
  - 注册能力 `usage_plugin: true`，宿主每次请求完成后会把规范化的
    `pluginapi.UsageRecord` 推送给插件。
  - 插件在 `handleUsage` 里把 record 转发到 CPAMP，与宿主 `DefaultManager`
    的记录并行，不再重复也不遗漏。
  - 旧路径 `publishUsage` 保留向后兼容（老版本 CPA 没接 UsagePlugin 时仍可
    上报），新路径 `handleUsage` 同步触发，CPAMP 侧基于 (timestamp + auth +
    model + total_tokens) 幂等去重。
- **`reportUsageToCPAMP` 重命名为 `forwardUsageToCPAMP` 并走 host.http.do**：
  CPAMP 上报自身也走宿主桥，宿主能看到插件的运维流量。

### Architecture notes
- `hostBridgeAvailable()` 检查 `hostAPI.call` 是否为 nil，统一决定是否
  fallback。生产环境永远为 true，单元测试永远为 false（无宿主）。
- 所有 `*Direct` 函数仅服务测试；生产路径不经过。
- 宿主侧 `sanitizePluginRequest` 会把 `ExecutorRequest.HTTPClient` 置 nil
  （跨 c-shared 边界接口无法传输），所以**插件不可能用宿主注入的
  HTTPClient**——`host.http.*` RPC 是 c-shared 插件访问宿主 transport 的
  唯一合规方式，本版本全部采用。

## 0.6.31

### Security
- **UID 路径穿越修复**：`authFileNameFor` 新增 `sanitizeUIDForFileName` 白名单
  （`[^a-zA-Z0-9_-]+` → `_`、长度 ≤64、拒绝 `.`/`..`），导入凭证的
  `workbuddy-<uid>.json` 不再可能被 `../` 注入到任意路径。
- **refresh_token 停止泄露到 chat 上游**：`backendHeaders` 移除
  `X-Refresh-Token`。refresh_token 是长期凭证，只在 refresh 端点用；之前每次
  chat completion 都附带它，上游日志一旦记录请求头即等同账号被盗。
- **插件层 management 鉴权 + 限流**：`handleManagement` 入口对所有 POST /
  写端点新增插件层防护：constant-time Bearer 比对（`crypto/subtle`），
  per-IP token-bucket 限流（容量 5、每 6s 1 个）。配置方式：
  `config_yaml management_key:` 或 env `WB_MANAGEMENT_KEY`。空则保持
  历史行为（仅依赖宿主鉴权）。
- **panel.html XSS 修复**：4 处 `onclick="...('${esc(auth_index)}',this)"`
  改为 `data-action` + `data-auth-index` + `addEventListener`。`esc()` 只
  转义 HTML 不防 JS 字符串上下文注入。
- **panel.html CSRF 缓解**：`fetch` 显式 `credentials:'omit'`，面板纯靠
  Authorization Bearer，不再隐式带 cookie。
- **redactSecrets 兜底裸 JWT**：新增 `redactREJWTLoose` 正则，匹配不带
  `Bearer` 前缀、`access_token` key 的 `eyJ…` 两段/三段 JWT。

### Bug Fixes
- `invalidateAccountCredits` 数据竞争：直接改 sync.Map 共享 entry 的字段
  （`e.credits = nil`），并发 dashboard / reconcile / chat 后置 invalidate
  会拿到撕裂状态。改为 `fresh := *e; Store(&fresh)` 值拷贝，与其他 4 处
  写法一致。
- `handleManualCheckin` "early already" 路径丢 credits/plan：直接构造
  `accountCacheEntry{checkin: ci}` 覆盖整个 entry，签到后面板积分消失。
  改为 merge prev 的 credits/plan。
- `configure` 嵌套锁：在 `checkinAutoMu` 内嵌套获取 `lifecycleAutoMu` /
  `schedulerModeMu`，未来加反向获取路径即死锁。改为两阶段：无锁解析到
  局部变量，再分别单锁写入。
- `scheduler_mode: off` 配置断链：configure 解析但 `handleSchedulerPick`
  从不读取，"off" 实际表现为 "credits"。现在 off 正确 defer 给内置 scheduler。
- 删除 Global 账号后 `activeAuthID` 残留指向已删 ID：`deleteAuth` 两个成功
  路径现在都调 `clearActiveAuthIfMatch(authID)`。
- `runAutoCheckin` 重复 `fetchCheckinStatus` + 变量 shadow：原代码内层
  `ci` shadow 外层，且第二次调用与第一次状态可能不一致。改为单次调用，
  签到成功才 refresh。
- `out[:0]` 共享底层数组：`filtered := out[:0]` 复用底层数组在 range 中
  写入，改为 `make([]wbAccount, 0, len(out))`。
- `pumpUpstreamStream` 无 context：`http.NewRequest` 无 context，客户端
  断开后 goroutine 一直读到 120s 超时。改为 `NewRequestWithContext` +
  cancel 传入 pump，所有退出路径释放。

### Performance
- **热路径 4 次 JSON 序列化合并为 1 次**：新增 `prepareUpstreamBody` 统一
  `forceStreamBody` + `normalizeToolsForUpstream` + `rewriteSystemForUpstream`
  + `ensureSystemMessage` + `rewriteModelInBody`，单次 unmarshal + 单次
  marshal。每次 chat completion 省 4-5 个 JSON 往返。
- **`runAutoCheckin` 串行改并发**：抽出 `processAutoCheckinAccount`，主循环
  `sem=4` 并发。N 账号从 3N 串行 HTTP 降到并发 4 路。
- **`cachedAccountDetails` 加 singleflight**：per-authID `sync.Map` + done
  channel。并发 dashboard / reconcile 对同一账号只跑 1 次上游 fetch，
  其他 goroutine 等结果，消除 6x upstream QPS + last-writer-wins。
- **冒泡排序改 sort.Slice**：`pruneAccountCacheSoftCap` 从 O(n²) 降到 O(n log n)。

### Refactor
- **handleManualCheckin 273 行拆分**：`classifyCheckinTargets` /
  `executeCheckinBatch` / `summarizeCheckinResults` 三段独立函数，各自
  单一职责，便于单测。
- **management BasePath 不再硬编码**：register 时缓存宿主注入的 BasePath，
  handleManagement 用 cached 值。宿主未来版本化路径不会失效。
- 死代码清理：删 `upstreamBase` legacy 常量、`usageReportConfigured` 无人
  调用、`buildDashboard` 包装函数。

### Tests
- 新增 `TestSchedulerPick_OffMode_Defers` 覆盖 scheduler_mode=off 行为。
- 全套 115 tests + `-race` 通过。

## 0.6.29

### Fixed
- 修复签到后按钮不变"已签到"、套餐标记丢失的问题
  根因：handleManualCheckin/runAutoCheckin/handleClaimTrial 在签到/领取成功后
  accountCache.Delete(f.ID) 把 cache 清了，light load 时 checkin/plan 是 nil。
  handleCreditsQuery 的 cache merge 逻辑从 prev.plan（空）取值而不是用刚获取的
  fetchPaymentType(sa) 结果，导致 plan 在 light load 后丢失。
  修复：签到/领取成功后把 checkinSummary 存回 cache 而不是删除；
  handleCreditsQuery cache merge 用刚获取的 plan；runAutoCheckin/handleClaimTrial
  改为 invalidate credits（置 nil）而不是删除整个 cache entry。

## 0.6.28

### Fixed
- 修复面板选中卡片与实际路由账号不一致的根本问题
  根因：activeAuthID 存的是 auth.Index（运行时 SHA256 hash），但 scheduler
  的 SchedulerAuthCandidate.ID 是 auth.ID（持久化 UUID），两者永远不匹配，
  导致 pickActiveAuth 永远走 fallback 选第一个，面板显示选中第一个但实际
  路由到别的账号。同时 cachedCreditsScore 用 auth.ID 查 accountCache（key
  是 auth.Index）也查不到，exhausted 判断也坏了。
  修复：全链路统一用 auth.ID — activeAuthID、accountCache key、
  lifecycleState key、面板 selected 判断、/select API 返回值全部改用
  auth.ID。lifecycle 函数（reconcileOneAccount/disableAuth/reenableAuth/
  deleteAuth/syncAuthNote）加 authID 参数，resolveAuthIndex 改为
  resolveAuthIndexAndID 同时返回 index+ID。
- 修复首次加载面板时选中耗尽账号的问题
  首次 GET /accounts 不拉 credits（fetchCredits=false），所有卡片
  Exhausted=false，ensureDefaultActiveAuth 选第一个。lazyLoadCredits
  异步获取积分后发现第一个已耗尽，但选中状态不会更新。
  修复：lazyLoadCredits 全部完成后前端静默再拉一次 /accounts（此时
  cache 已有 credits，light load 能拿到正确 exhausted 和 selected），
  重新渲染卡片。

## 0.6.27

### Fixed
- ensureDefaultActiveAuth 也检查 Exhausted：面板刷新时选中账号已耗尽会同步切换
  修复 scheduler.pick 切了但面板 ensureDefaultActiveAuth 又选回去的 race
  现在 pickActiveAuth 和 ensureDefaultActiveAuth 用同一套规则，选中状态不会漂移

## 0.6.26

### Fixed
- 选中账号积分耗尽时自动切换到第一个可用账号，并同步更新选中状态
  全部耗尽时留在当前账号不 flip-flop
  修复 v0.6.25 过度 sticky 导致耗尽后一直报错的问题

## 0.6.25

### Fixed
- 选中账号 sticky：scheduler 不会因缓存过期/积分耗尽自动切换到别的账号
  只有 host 把选中账号从候选列表移除（disabled/deleted）才切换
  修复面板显示选中A但实际路由到B、静默消耗积分的问题

## 0.6.24

### Fixed
- model.static / model.for_auth 现在尊重 CPA 的 oauth-excluded-models 配置
  在 config.yaml 的 oauth-excluded-models.workbuddy 里列出的模型不再出现在 /models

## 0.6.23

### Fixed
- usage import URL 自动探测：先试 127.0.0.1:18317（裸机/Docker host），再试 Docker 服务名 cpa-manager-plus:18317
  不再写死 Docker 服务名，裸机安装也能自动找到 CPAMP

## 0.6.22

### Fixed
- ExecutorModelScope 改为 OAuth：插件只处理 workbuddy auth 绑定的模型
  不再拦截其他 openai-compatible 供应商的同名裸模型（如 deepseek-v4-flash、glm-5.2）
  修复启用 workbuddy 后自定义供应商模型请求不进监控的问题

## 0.6.21

### Fixed
- 积分懒加载改为并发：所有卡片同时请求，不再逐个排队

## 0.6.20

### Fixed
- 懒加载积分时同时拉取 plan（套餐类型），修复 plan 徽章显示「-」不更新

## 0.6.19

### Added
- 每张卡片新增「刷新」按钮：单独查询积分并即时更新该卡

## 0.6.18

### Added
- 积分懒加载：进页面先渲染骨架卡（加载中…），逐卡异步拉积分，失败自动重试一次
- 后端 `/accounts` 默认不再并发拉所有账号 credits（避免上游 500）
- `/credits?auth_index=` 单账号查询返回完整字段（region/exhausted/trial_claimed）

### Fixed
- 缓存有效时仍返回缓存的 credits，不再触发上游请求

## 0.6.17

### Fixed
- 流式路径也强制 `stream:true`：WorkBuddy API 现仅支持 stream 模式，`stream:false` 会报 "Non-stream chat request is currently not supported"

## 0.6.16

### Fixed
- 夜间模式：用量汇总卡与账号卡统一 `--card` 底色；内部指标格改用 `--surface`，避免汇总卡看起来更深/发黑

## 0.6.15

### Added
- 面板「选用」账号：默认第一张可用卡；选中卡决定 CN/Global 路由（读 domain，不解码 JWT）
- 选中账号耗尽/禁用/消失时随机切换下一张可用卡并记住

### Changed
- scheduler.pick 改为始终跟随 active 选中账号（不再依赖 credits 排行模式）

## 0.6.14

### Fixed
- Global 账号聊天 401/400 修复：JWT iss=workbuddy.ai 必须走 www.workbuddy.ai 端点（copilot.tencent.com 会对 Global token 返回 401）
- Global 请求自动注入 system message（www.workbuddy.ai 对 user-only 请求返回 code 11101）
- token 刷新和 models 发现也走域名感知端点

## 0.6.13

### Changed
- 请求监控 key 自动探测：config → env（CPAMP_ADMIN_KEY/USAGE_REPORT_KEY）→ docker secret `/run/secrets/cpamp_admin_key`，无需手写 usage_report_key


## 0.6.12

### Changed
- 删除无效 `usage.PublishRecord` 路径，请求监控仅走 CPAMP `/v0/management/usage/import`


## 0.6.11

### Fixed
- **请求监控**：c-shared 隔离导致 `usage.PublishRecord` 进不了宿主 redisqueue；改为异步 POST CPA-Manager-Plus `/v0/management/usage/import`（`usage_report_url`/`usage_report_key`）
- 补全 ExecutorType/AuthType/Source；配置字段暴露于管理面板


## 0.6.10

### Fixed
- **批量签到先过滤再操作**：Global 不参与；今日已签跳过；仅对 CN 未签账号调用 daily-checkin
- 返回 `summary{success,already,skipped_global,fail,eligible}`，面板文案不再把 Global/已签当失败
- 分类/签到并发（限流），降低「全部签到」卡到 502 context canceled

## 0.6.9

### Changed
- **Panel theme adaptive**: CSS variables now default to light (paper) theme; `[data-theme="white"]` and `[data-theme="dark"]` overrides align with CPA management panel tokens. Embedded iframe mirrors parent `data-theme` via MutationObserver; standalone page follows `prefers-color-scheme`. All hardcoded dark colors (toast, modal, input, buttons) replaced with theme-aware CSS variables.

## 0.6.3

### Fixed
- Auth identity: parse/refresh leave ID empty; regression tests (A-01)
- Stream pump: emit failure is failed usage; defer streamClose (A-06)
- No dual-write after host.auth.save (A-15)
- Scheduler skips host-disabled candidates (A-04)
- Global delete reconstructs path via peer auth dir (A-07)
- Panel IP ban wait parses upstream window (A-08)
- accountCache concurrent errs race + soft cap (A-02)
- Dashboard single host.auth.get per row (A-05)
- Instant check-in/trial button state (panel)


## 0.6.2

### Fixed
- **Credits look frozen after chat**: cache TTL 5m→45s; invalidate cache after successful chat (stream + non-stream)
- **Spend math**: package used = cycle size−remain; account total_size from package sizes; TotalDosage treated as capacity pool (not consumption)
- **Check-in packs inflate "available"**: UI labels 可用/已用/额度池 so grant vs spend is visible; note shows 余/已用/池

## 0.6.1

### Added
- WorkBuddy panel **用量汇总**：筛选范围内 剩余/已用/总量/占比 + 进度条；全部视图附 CN/Global 分项
- Dashboard API `summary` 字段：`total_remain` / `total_used` / 分区域统计

### Notes
- CPAMP Auth 页进度条仅支持内置 `codex/claude/kimi/xai/antigravity`（`QUOTA_PROVIDER_TYPES` 白名单）；workbuddy 无法靠 `note` 注入进度条，完整用量看插件面板

## 0.6.0

### Added
- **Credit lifecycle** (plugin-only, no CPA/CPAMP source changes):
  - CN exhausted → write auth file `disabled:true` (host skips scheduling)
  - Global exhausted → **delete** auth file (`os.Remove` on path from `host.auth.get`)
  - CN disabled + credits return (after check-in / refresh) → `disabled:false`
  - Executor hard credit errors → async reconcile; pure 429 does not delete Global
  - Unknown credits → no-op (safe default)
- Auth file **note** / **label** enrichment: `CN · 余 x · …` / `Global · …` / 已禁用
- Panel: CN/Global filter tags + counts; disabled badge; lifecycle toast on refresh
- Panel: management-key discipline to avoid CPA IP ban (no request without key; 401/403 backoff)
- Config field `lifecycle_auto` (default true)

### Changed
- Scheduled tick **no longer auto-claims Global trial** (one-shot; manual `/trial` / panel only)
- Tick = CN check-in (if `checkin_auto`) + lifecycle reconcile for all regions
- Import/save writes top-level `type`/`logo`/`note`/`disabled` with nested auth/account
- Force dashboard refresh runs lifecycle and may drop deleted Global rows

### Notes (CPAMP Auth page)
- Filter letter **「W」** / brand typeBadge colors cannot be fixed from the plugin (frontend static icon table)
- Plugin sets `Metadata.logo` + registration Logo; Auth cards show **note** for region/credits summary
- Full UX: WorkBuddy side panel

## 0.5.0

### Added
- International (Global) WorkBuddy account support (`www.workbuddy.ai` domain)
- Domain-aware billing API routing: CN accounts → `codebuddy.cn`, Global → `workbuddy.ai`
- Expert trial pack claim API: `POST /plugins/workbuddy/trial` (Global only, one-time 250 credits / 14 days)
- Panel region badges: light green `CN` (daily checkin) + light orange `Global` (expert trial)
- "全部领取" batch claim button for Global accounts
- Auto-scheduler region branch: CN → daily checkin, Global → claim expert trial if unclaimed
- `wbAccount.region` and `wbAccount.trial_claimed` fields in accounts API response
- `hasTrialPack()` helper detects trial pack from `get-user-resource` packages

### Changed
- `billingBase` selection is now domain-driven via `billingBaseFor(sa)`
- `backendHeaders` Origin/Referer dynamically set per account domain via `originRefererFor(sa)`
- Panel card buttons: CN → 签到, Global → 领取专家加油包 / 已领取
- "全部签到" button only triggers CN accounts (Global accounts are skipped with a message)
- `runAutoCheckin` branches by region: CN daily checkin, Global trial claim

## 0.4.3

### Changed
- Panel import modal: white surface + dark text for readable contrast (was dark-on-dark)

## 0.4.2

### Changed
- Panel: credential import is a toolbar button (left of 刷新数据) opening a modal, instead of an always-visible card

## 0.4.1

### Added
- Panel **耗尽** badge + `exhausted` field on accounts API (shared with scheduler)
- Credential **import** API `POST /plugins/workbuddy/import` + panel paste UI
- Per-account check-in lock (multi-tab safe)
- `executor.count_tokens` stub (`input_tokens:0` — upstream has no API)
- LICENSE (MIT), VERSION file, GitHub Actions multi-arch release workflow

### Changed
- SSE cleanChunk strips empty `extra_fields` / `refusal` / `reasoning_content`
- Scheduler credits mode prefers non-exhausted accounts first

## 0.4.0

### Added
- CPA **Scheduler** capability with `scheduler_mode`: `off` (default) | `credits`
- Credits-aware multi-account pick using panel credit cache

## 0.3.18

### Fixed
- ConfigFields use SDK `ConfigFieldType*` constants

## 0.3.17

### Fixed
- `FrontendAuthProvider` set false; remove dead frontend-auth handlers

## 0.3.16

### Fixed
- Panel refresh toast + busy feedback

## 0.3.15

### Fixed
- Normalize OpenAI object `tool_choice` for CodeBuddy upstream
