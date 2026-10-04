# WorkBuddy 插件（CLIProxyAPI）

[CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI) 的 **腾讯 CodeBuddy / WorkBuddy**
统一原生 OAuth 提供商插件：一个插件自动识别并路由国内 CN 服务
（`copilot.tencent.com` / `codebuddy.cn`）和 Intl 展示区域下的国际服务
（`codebuddy.ai` 与旧 `workbuddy.ai`）。面板只显示 CN / Intl；旧 WorkBuddy 凭据仍走
原服务域，不会因区域合并而切换上游。插件提供模型发现、流式执行器、积分感知调度、
每日自动签到和内置管理面板。

[English → README.md](README.md)

## 功能

- **OAuth 登录** — 通过宿主 auth store 管理多账号。新账号按服务域使用
  `workbuddy-CN-<uid>.json`、旧 WorkBuddy 服务 `workbuddy-Global-<uid>.json` 或
  CodeBuddy Intl `workbuddy-intl-<uid>.json`。Global 不再是公开区域；两类海外账号
  均显示 Intl，但服务路由和身份缓存仍分开。旧文件名继续可读，迁移不会直接删除宿主记录。
- **按区域提供模型** — CN 与旧 WorkBuddy 服务使用各自的上游模型目录和 5 分钟缓存；
  CodeBuddy Intl 默认使用 Intl 静态目录，也可用 `models_intl` 固定完整列表。
  `models` 自定义静态模型数组支持按 ID、名称、渠道、上下文、最大输出和启用状态添加；
  `channel: cn` 仅用于 CN，`channel: intl_global` 用于国际服务。旧的 global/intl YAML
  值继续兼容。模型不会经上游验证，错误模型可能返回 11102。面板「设置 → 模型管理」中的
  全局禁用名单 `models_disabled` 会从 CPA 静态和账号模型注册结果中移除对应模型；宿主侧
  `oauth-model-alias` / `oauth-excluded-models` 仍独立生效。
- **执行器** — OpenAI 兼容 chat completions，流式（真 SSE，走 `host.stream.emit`）
  和非流式（SSE 折叠成单个 completion）都支持。内置 `tool_choice` 归一、
  Claude Code 模板清洗、按区域注入 system message。
- **积分生命周期** — CN 账号耗尽自动 `disabled`，签到回血后自动恢复；
  符合条件的旧 WorkBuddy 服务账号耗尽后标记为 `disabled` 并保留 auth 记录
  （一次性试用额度），最终清理由 CPA 管理操作完成。Executor 遇到硬积分错误立即 reconcile。
- **成长任务自动点亮** — 自动接取成长任务、构造规范行为事件上报点亮并领奖，
  覆盖画布/模板/专家/团队/技能/自动化/灵感案例/主题/对话/夜猫子等 14 类任务
  （专家/团队事件自动轮换不同 id 以推进进度；夜猫子仅在 23:00–08:00 计数；
  需真实桌面操作的任务如实标注并给出深链，不伪造）。面板「成长任务」弹窗内
  可一键点亮。
- **猫猫旅行** — 自动检查旅行状态：在家自动派出（自动获取目的地）、归来自动
  领奖，随每日 09:00/21:00 调度执行，面板亦可手动触发。
- **稳定设备指纹** — 每个账号以 UID 派生固定的机器码/会话码/请求前缀，
  同一账号永远呈现同一台"虚拟设备"，多账号之间互不关联，规避上游多号关联风控。
- **每日签到** — CN 账号每天 09:00 和 21:00 自动签到（可配置）。面板可手动
  全部签到。Per-account 互斥锁防止多浏览器标签并发重复签到。
- **Trial 领取** — 符合条件的旧 WorkBuddy 服务账号可在面板领取一次性专家加油包；CodeBuddy Intl 不会被重定向到该服务接口。
- **积分面板** — 内嵌面板 `/v0/resource/plugins/workbuddy/panel`，含积分
  进度条、套餐徽章、耗尽/禁用标记、CN/Intl 筛选、全局模型禁用和凭证导入。
- **调度器** — `builtin`（默认）通过返回 `DelegateBuiltin(round-robin)` 把
  调度交给 CPA 内置实现，把策略钉死在 CPA 自己那套上，而不是仅仅"不插手"；
  `scheduler_mode: credits` 可切换为插件选中面板选中的账号。
- **每日免费额度** — 面板按模型显示免费额度用量（如 `deepseek-v4.1-flash`
  每日 2 亿 token），由插件按宿主用量记录本机统计（上游无每模型额度接口）。
  与积分是两回事：按模型、每天重置、只显示不拦截。
- **宿主调度状态** — 面板显示 CPA 侧的真实依据（冷却剩余时间、可用性、
  优先级、成功/失败计数），来自 `host.auth.get_runtime`，而不是插件自己的推测。
- **请求终态告警** — 接收 CPA 的 `request.complete` 事件，连续 5 个请求未成功时
  在面板和日志告警（这是唯一能看到"请求到了但没被服务"的地方）。
- **原生额度刷新** — 实现 CPA 原生 `QuotaProvider`，CPA 的 quota fetch/refresh
  会直接查询 WorkBuddy 计费接口并返回套餐、总额度和分包周期；原生 reset 明确返回不支持。
  自定义 `/v0/management/plugins/workbuddy/refresh` 仍保留，用于带生命周期副作用的全量刷新。
- **Usage 上报** — 无需插件配置：CPA 宿主自动把本插件的请求用量记入宿主
  用量队列（`/v0/management/usage-queue`），CPAMP 采集器拉取该队列。
  插件侧的直推路径已在 0.9.30 删除，避免同一请求在 CPAMP 里存两条。

## 快速开始

### 1. 安装插件

把编译好的 `workbuddy.so` 放到 CPA 插件目录：

```bash
cp workbuddy.so /path/to/cliproxyapi/plugins/
```

多架构部署可用平台子目录约定：

```
plugins/
  linux/amd64/workbuddy.so
  linux/arm64/workbuddy.so
  darwin/arm64/workbuddy.so
```

### 2. 启用配置

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    workbuddy:
      enabled: true
```

### 3. 登录

从 CPA 侧边栏打开 WorkBuddy 面板（或直接访问
`/v0/resource/plugins/workbuddy/panel`），点 **登录** 走 OAuth 流程。
每个账号登录一次，插件会按服务域把 `workbuddy-CN-<uid>.json`、旧 WorkBuddy 服务
`workbuddy-Global-<uid>.json` 或 CodeBuddy Intl `workbuddy-intl-<uid>.json` 写入 auth store。

### 4. 调用

用任何映射到 workbuddy 模型的 alias 调 OpenAI 兼容端点：

```bash
curl http://localhost:8317/v1/chat/completions \
  -H "Authorization: Bearer $CPA_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "point/deepseek-v4.1-flash",
    "messages": [{"role": "user", "content": "hi"}],
    "stream": true
  }'
```

## 配置项

**推荐零配置**：安装后即可使用。CPA 原生插件配置表单精确只显示两个登录选项：

| 字段 | 默认 | 说明 |
|---|---|---|
| `login_region` | cn | 新登录区域：CN / Intl；Intl 账号显示统一，但旧 WorkBuddy 服务仍保留原路由 |
| `login_platform` | CLI | 新登录客户端：CLI（WorkBuddy）/ ide（CodeBuddy IDE） |

自动签到、积分生命周期、成长任务、旅行和令牌保活等高级选项仍支持 YAML 配置；
面板的 **「⚙ 自动任务设置」** 可直接增量写入 CPA 配置。隐藏字段不会因原生表单仅显示两个选项而被覆盖。

以下为**高级选项**（不在可视化表单中，写在 YAML 里依然生效，编辑器保存
不会覆盖它们）：

```yaml
plugins:
  configs:
    workbuddy:
      enabled: true

      # ── 高级选项（全部可选，默认值即最佳实践） ──
      # CPA 原生配置表单仅显示 login_region 与 login_platform；其余配置继续在 YAML 生效。

      # 全局禁用模型 ID（面板可编辑；大小写不敏感、精确匹配，跨所有账号生效）。
      # models_disabled:
      #   - deepseek-v4.1-flash
      #   - model-to-hide

      # 调度行为（默认 "builtin"）：
      #   builtin → 交给 CPA 内置调度。插件返回 DelegateBuiltin(round-robin)，
      #             把策略钉死在 CPA 自己的实现上，而不是仅仅"不插手"。
      #             "off" 作为别名继续接受。
      #   credits → 插件选中面板选中的账号（耗尽/禁用时回退）
      scheduler_mode: "builtin"

      # 每模型每日免费额度（面板显示）。与积分是两回事：积分是付费额度、
      # 账号级、按套餐周期重置；这里是免费额度、按模型、每天重置。
      # 上游无每模型额度接口，故由插件本机统计（面板标注「本机统计」）。
      # 只显示不拦截：超额不会阻止请求，由上游自行拒绝。
      # 内置默认仅 deepseek-v4.1-flash（每日 2 亿）。在此覆盖或新增；
      # 填 0 可移除某条覆盖。
      # 支持 200000000 / 200_000_000 / 200m / 2e8 / 2.5亿 / 5000万。
      # 只填**确认有**免费额度的模型：填错会给不存在的额度配百分比。
      # daily_free_limits:
      #   deepseek-v4.1-flash: 2亿

      # 用量上报：无需任何插件配置。CPA 宿主自己会把本插件的请求用量
      # 记入宿主用量队列（/v0/management/usage-queue），CPAMP 的采集器
      # 正是拉取该端点。插件直推 CPAMP 的旧路径已在 0.9.30 删除
      # （否则同一请求会在 CPAMP 里存两条）。

      # 自定义静态模型，推荐按渠道分组配置：
      # intl_global 用于所有 Intl 服务，cn 用于 CN 渠道。请使用内置目录
      # 尚未包含的 ID；支持字符串或完整模型对象。
      # 扁平列表与旧 realm: global/intl 继续兼容。
      models:
        intl_global:
          # - custom-intl-model
        cn:
          # - custom-cn-model

      # models_intl 填逗号分隔 ID 时固定完整 Intl 目录；留空使用内置静态目录。
      models_intl: ""
```

配置解析说明（0.9.31）：配置由 YAML 解析器读取，行内注释、引号、嵌套均按
YAML 语义处理。例如 `checkin_auto: true # 开启` 正确解析为 true。

排查提示：每次配置加载会输出一行摘要日志（不含密钥），可直接确认生效值：

```text
workbuddy: config checkin_auto=true lifecycle_auto=true keepalive=true region=cn platform=CLI scheduler=builtin usage_report=host (由 CPA 上报)
```

模型 alias 和宿主排除走 CPA 原生 `oauth-model-alias` 与 `oauth-excluded-models`；
面板「设置 → 模型管理」的 `models_disabled` 作为插件持久配置，独立过滤 `model.static` 和 `model.for_auth` 注册结果。

## 生命周期

| 状态 | CN 账号 | Intl / CodeBuddy | Intl / 旧 WorkBuddy |
|---|---|---|---|
| 积分 > 0 | active | active | active |
| 积分 = 0 | `disabled: true`（auth 文件保留） | `disabled: true` | `disabled: true`（auth 记录保留） |
| 签到回血 | 自动恢复 | n/a | n/a（trial 已耗尽） |
| Trial 可领 | n/a | 不适用 | 每账号一次 |
| 积分未知 | 不动（永不误杀） | 不动 | 不动 |

Executor 遇到硬积分错误（402、"insufficient credits"、"积分不足" 等）
会立即触发该账号的 reconcile。

### 上游限流与空回答（0.9.32）

被限流的账号有两种表现，插件都按**限流**处理，交给 CPA 冷却该凭据并把重试
路由到其他账号：

| 表现 | 旧行为 | 现行为 |
|---|---|---|
| HTTP 200 + 合法 SSE 框架但**无任何内容**（只有 `role`/`finish_reason`/`usage`） | 折叠成 `content:""` 的正常回复，报成功；账号继续留在轮转里 | 判定为限流 → **429** `rate_limit_error` |
| 模型目录 `contextWindow` **全部为 0** | 当作正常目录缓存 5 分钟并下发（客户端退回默认值、过早压缩上下文） | 判定为限流 → 回退该区域**带真实上下文**的静态目录并记录原因 |

判定细节：

- 「有内容」= 正文 / 推理 / 工具调用**任一存在**，因此纯工具调用轮次
  （`content` 为空但有 `tool_calls`）不会被误杀。
- `count_tokens` 同样不再返回 0：上游没有等价接口，插件做**本地估算**
  （字符数 / 3，刻意偏向高估，不请求上游、不消耗积分）。返回 0 会让做上下文
  预算的客户端（Claude Code）以为 prompt 不占空间，从不压缩历史，真正请求时
  才撞上游上限失败。
  返回体的形状要跟随**宿主**而不是客户端：插件声明 `chat-completions` 输出，
  宿主会用 openai→claude 的**响应**翻译器把结果转给 Claude 客户端，而它读的是
  `usage.prompt_tokens`，会静默丢弃顶层的 `input_tokens`（0.9.32 的坑）。
  由 token-count wire-shape 单元测试覆盖。
- 目录降级判定只在**全部启用条目**都拿不到上下文窗口时成立；个别条目缺字段
  是合法的，不会触发。
- 429 冷却是指数退避的**临时**冷却（下限 10s，有上限），下一次成功请求即清零；
  插件的积分生命周期不参与该判定（软限流从不触发禁用，避免误杀）。

### 账号名（0.9.32）

账号显示名按 昵称 → 令牌 `email`/`upn`/`sub` 声明 解析，并由
`buildAuthFileJSONFromExisting` 写入 auth 文件的 `email` / `account_name` /
`uid` 字段。原因是宿主的保存语义：`host.auth.save` 会**从文件 JSON 重建 auth
记录**，行标签取自 `metadata["email"]`，缺失时回退成 provider 名
（`workbuddy`）。0.9.32 之前插件不写这些字段，因此第一次生命周期写入（禁用 /
备注同步 / 保活）就会把账号名变成 `workbuddy`。

- 仅在字段**缺失时**写入，管理端手工改名优先。
- `parseStored` 会读回这些字段（嵌套与 flat 两种形态），避免重读丢名字。

## 开发

需要 Go 1.26+（与 CPA 一致）。

```bash
# 编译插件
go build -buildmode=c-shared -o workbuddy.so .

# 跑测试
go test -race ./...

# Lint
gofmt -l .
go vet ./...
```

插件所有上游调用走 CPA 宿主 HTTP 桥（`host.http.do` / `do_stream`），
request-log 可捕获出站流量并应用宿主 transport 策略。桥不可用或返回
不明确结果时插件会安全失败，不会通过第二套 transport 重放请求。

完整开发流程见 [docs/development.md](docs/development.md)，模块结构见
[docs/architecture.md](docs/architecture.md)。

## License

MIT — 见 [LICENSE](LICENSE)。
