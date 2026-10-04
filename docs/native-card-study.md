# 学习 CPA 原生凭证卡片：可借鉴的优化项

> 来源：`Cli-Proxy-API-Management-Center` 的 `management.html`（本机 v7.3.12 拉取的
> 面板资产，2.68 MB）。原生凭证卡片由独立组件构成：
> `AuthFileCard`(52 类) / `AuthFileCooldownSection`(18) / `AuthFileQuota`(37) /
> `AuthFileModelsModal`(7) / `AuthFileDetailsSheet`(10) / `AuthFilesToolbar`(15)。
>
> 本文只写**核实过**的差异 —— 每条都来自源码或实测，不是推测。

---

## 一、最值得学的：时间分桶健康条（`statusBar`）

这是原生卡片最有价值的设计，我们现在完全没有。

### 它是什么（源码事实）

```
20 个方块，每块 10 分钟 = 覆盖最近 200 分钟
每块按该时段的成功率着色：
  idle    无请求     → 灰色（text-quaternary 26% 透明）
  success 全成功     → 绿色（viz-success）
  failure 全失败     → 红色（viz-failure）
  mixed   混合       → 按比例插值 (vw(rate))
悬停显示：时间范围 + 成功数 + 失败数 + 成功率
整体成功率着色：>=90% 绿 / >=50% 中 / <50% 红
```

关键实现细节（值得照抄的地方）：

- `rate === -1` 表示**无请求**，与 `rate === 0`（全失败）**严格区分** ——
  灰色和红色语义完全不同，混用会让"闲置"看起来像"故障"
- 时间戳是**向前推算**的（`Date.now() - 20*10min + i*10min`），
  不依赖后端返回时间轴，所以缺数据时左侧补空块而不是错位
- 悬停提示会自动**左右翻转**（`i<=2` 靠左，`i>=n-3` 靠右），避免贴边溢出

### 我们能做吗？**能，但必须用宿主的数据，不要自己统计**

**2026-09-22 修正**：本文最初写的是"插件自己分桶统计"。那个方案是错的，
已实测推翻 —— 宿主**早就提供了这份数据**：

- 宿主按**凭据**维护 `20 × 10 分钟` 的 success/failed 环形分桶
  （`sdk/cliproxy/auth` 的 `RecentRequestsSnapshot`，`recentRequestBucketSeconds = 600`、
  `recentRequestBucketCount = 20`）
- 每个被路由到该凭据的请求都记进去（`conductor_cooldown.go` 的
  `auth.recordRecentRequest`）
- 经 `host.auth.get_runtime` 的 `RecentRequests` 字段下发给插件
  （`buildHostAuthFileEntry`）

**为什么插件自己统计是错的**（不只是重复劳动）：

| | 宿主统计 | 插件自己统计 |
|---|---|---|
| 覆盖范围 | 所有路由结果 | 只有到达本插件执行器的请求 |
| 账号归属 | ✅ 按凭据 | ❌ 拿不到（实测 `selected_auth_id` 不下发） |
| 漏计场景 | — | 全部凭据不可用时宿主直接拒绝，一条都看不到 |

即：插件自建的健康条，**恰好在"所有账号都不可用"时最不可靠** ——
而那正是最需要它的时刻。

**正确做法**（0.9.41 已实施）：读 `host.auth.get_runtime` 的 `RecentRequests`，
原样展示为每账号一条健康条。插件侧不再做任何健康度统计。

---

## 二、值得学的：卡片信息层级

对比我们的卡片和原生的（均为源码核实）：

| 维度 | 原生 | 我们 | 差距 |
|---|---|---|---|
| 状态可视化 | 20 格时间条 | 一行「冷却 X」徽章 | **原生能看趋势，我们只能看当下** |
| 身份区 | `identity` 弹性换行 + `accountMono` 等宽字体 + 文件名 2 行截断 | 昵称一行省略号 + uid·name 一行 | 原生对**长邮箱/长文件名**更友好 |
| 元信息 | `metaRow` 等宽数字 + `metaWeight`/`metaPriority` 胶囊 | 无 | 原生把权重/优先级做成胶囊标签 |
| 操作区 | `actions` 顶部分隔线 + `margin-top:auto` 贴底 | 固定 padding | 原生卡片**高度对齐**（网格里更整齐） |
| 工具操作 | `utilityActions` + 30×30 `iconButton` 图标按钮 | 全文字按钮 | 原生更省横向空间 |
| 错误 | `errorBanner` 独立横幅（跨卡片） | 卡片内 `.card-err` | 原生区分**全局错误**与**单卡错误** |
| 紧凑模式 | `gridCompact`（260px）/ 常规（340px） | 单一 320px | 原生有密度切换 |
| 批量操作 | 完整工具栏（选择/反选/按筛选选/批量启停删下载） | 无 | 原生支持批量 |
| 分页 | `pagination` + `pageInfo` 等宽 | 无 | 账号多时原生可分页 |

**最该抄的三条**：

1. **`margin-top:auto` + 顶部分隔线**（卡片在网格中底部对齐，视觉整齐）
2. **等宽字体用于标识符**（uid/文件名/数字），我们只在部分地方用了
3. **`gridCompact` 密度切换**（账号多时一屏能看更多）

---

## 三、值得学的：冷却区独立成分组（`AuthFileCooldownSection`）

原生把冷却信息做成**可折叠的独立区块**，含 18 个部件：

```
rowHead: scope(凭据/模型级) + reason(原因) + remaining(剩余)
list:    逐模型的冷却行（model / next / elapsed / observed / deadline）
note:    说明文字
```

对照我们的实现：**只有一个「冷却 X 分钟」徽章**，信息量差一个量级。

原生区分了这些我们完全没有的概念：

| 原生概念 | 含义 | 我们有吗 |
|---|---|---|
| `scope` | 冷却作用域：整凭据 vs 单模型 | ❌ |
| `reason` | 冷却原因（8 种，见下） | ❌ |
| `observed` | 快照观测时间 | ❌ |
| `deadline` | 绝对到期时刻 | 只在 tooltip 里 |
| `elapsed` | 已过去时长 | ❌ |
| `next` | 下一次可重试 | ❌ |
| 逐模型列表 | 同一凭据不同模型独立冷却 | ❌ |

原生支持的 8 种冷却原因（i18n key 实证）：

```
cooldown_reason_quota                 额度限制
cooldown_reason_credential_quota      凭据额度限制
cooldown_reason_payment_required      需要付费
cooldown_reason_unauthorized          未授权
cooldown_reason_invalid_grant         授权失效
cooldown_reason_model_not_supported   模型不支持
cooldown_reason_not_found             未找到
cooldown_reason_transient_error       瞬时错误
cooldown_reason_cloudflare_challenge  CF 挑战
cooldown_reason_unknown               未知
```

**我们能做吗？部分能。**

- `reason`：宿主的 `host.auth.get_runtime` **不返回结构化原因**，只有
  `status` / `status_message`。

  **实测结果**：健康账号返回的只有 `{"status":"active"}`，**没有 `status_message`**。
  也就是说这个字段**只在宿主主动写入时才存在**（宿主源码里
  `StatusMessage: auth.StatusMessage`，值由宿主内部在特定路径设置）。
  所以「映射成原生那 10 种原因」目前**做不到** —— 不知道宿主实际会填什么，
  硬做就是猜，而猜正是这个插件过去几轮 bug 的根因。

  可行做法：**原样显示 `status_message`**（有就显示，没有就不显示），
  不做分类映射。等实测观察到宿主真实取值后，再决定是否值得映射。
- `deadline`：`NextRetryAfter` 已有，我们能显示绝对时间（现在只显示剩余）
- `elapsed`：需要知道冷却**开始**时间，宿主没给 → **做不到**，除非自己记录首次观测
- `scope`/逐模型：宿主不暴露 → **做不到**
- 我们**独有**的：插件侧的 `daily_free` 与积分信息，原生卡片没有

**诚实结论**：原生那套的丰富度建立在**宿主直接暴露调度内部状态**之上；
插件只能通过 `get_runtime` 拿到有限字段。所以**冷却区不能照搬**，
但可以补 `deadline` 绝对时间 + 尝试映射 `reason`。

---

## 四、值得学的：模型列表独立成弹窗（`AuthFileModelsModal`）

原生把模型列表放在**弹窗**里（`models_button` → modal），卡片上只放一个按钮 +
计数（`models_title` / `models_empty` / `models_excluded_badge` / `models_unsupported`）。

我们目前把模型来源压成一行「已支持 N 个模型」（`modelsHTML`）。

**差异**：原生的模型弹窗能显示**逐模型**信息（含 excluded 徽章、unsupported 原因），
我们只有总数。若用户需要知道"到底哪些模型可用"，我们的信息不够。

**代价**：中等（要做弹窗 + 后端逐模型端点）。**优先级中**。

---

## 五、不必学的

| 原生功能 | 为什么不学 |
|---|---|
| 批量选择/启停/删除/下载 | 这是**多 provider 通用管理页**的需求；我们的面板是单 provider 运维视图，CPA 主面板已经能做这些 |
| 分页 | 账号量级不同；WorkBuddy 通常个位数账号 |
| `prefix_proxy` / `headers` / `note` 编辑 | 这些是编辑**通用凭据文件字段**；我们的凭据结构固定，字段由 OAuth 流程写入 |
| `priority` / `weight` 编辑 | 属于 CPA 调度策略配置，不是 provider 插件的职责 |
| `websockets` / `excluded_models` | 上游不支持 / 已在配置层处理 |
| OAuth 重新登录入口 | 我们有 `/import` 导入流程，CPA 主面板已有 OAuth 入口 |

---

## 六、优先级建议

| 优先级 | 项目 | 收益 | 代价 | 数据是否具备 |
|---|---|---|---|---|
| 🥇 1 | **时间分桶健康条**（20×10min） | 一眼看出"持续故障 vs 瞬时抖动"，排障核心信息 | ~80 行 | ✅ 已有时间戳事件 |
| 🥈 2 | **卡片布局对齐**：`margin-top:auto` 贴底 + 顶部分隔线 + 等宽标识符 | 网格视觉整齐，长邮箱/文件名更可读 | ~30 行 CSS | ✅ |
| 🥉 3 | **冷却信息补充**：绝对到期时刻 + 原样显示 status_message | 少一次"还剩多久"的心算 | ~40 行 | ✅ deadline 已有；reason 不做映射（宿主不返回结构化原因） |
| 4 | **密度切换**（常规/紧凑） | 账号多时一屏看更多 | ~20 行 | ✅ |
| 5 | 模型列表弹窗 | 逐模型可见性 | 中 | ⚠️ 需新增端点 |
| ❌ | 批量操作 / 分页 / 凭据字段编辑 / 优先级编辑 | 不属于本插件职责 | — | — |

---

## 七、一句话总结

原生卡片最值得学的是**「用时间维度表达健康度」**（20 格分桶条）——
它把"现在好不好"升级成"最近 200 分钟一直好不好"，而我们的数据**已经够了**，
只差把 1 小时累计改成 10 分钟分桶。

其次是**布局细节**（贴底对齐、等宽标识符、密度切换），代价极低。

而**冷却区的丰富度不能照搬**：那建立在宿主直接暴露调度内部状态之上
（scope / 逐模型 / elapsed 插件都拿不到），照抄会做出一个假装有数据的空壳 ——
这正是这个插件过去几轮 bug 的同一类错误。

能给的是 **deadline 绝对时间**（`NextRetryAfter` 已有，现在只显示剩余）。
`reason` **不做映射**：实测健康账号的 `get_runtime` 只返回 `{"status":"active"}`，
`status_message` 仅在宿主主动写入时才存在，取值未知 —— 硬映射等于猜。
