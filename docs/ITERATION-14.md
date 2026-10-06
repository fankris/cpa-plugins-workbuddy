# Rebuild 14：临期积分、模型信息与六页面

版本：v8.0.15-2.0.0-rebuild.14；2026-10-06。

## 1. 修复菜单描述
Routes 的 GET /plugins/workbuddy/panel 和 Resources 的 /panel 同时使用 panelMenuDescription。保留双轨菜单和原路径。核对 CPA 8.0.15 原始 management.go：先处理 Routes，legacy 菜单复制 Description 到资源，冲突 Resources 被跳过。增加 first-wins 回归及实际发布 .so ABI 双轨描述一致检查。

## 2. 积分临期显示与真实账号调度
- 仪表盘显示已知积分、7天内到期积分、到期信息覆盖数和临期账号列表。账号行显示临期数，详情显示最近到期时间与该时刻到期数量。
- 从真实积分包 CycleEndTime、Remain 推导；RFC3339 使用其时区，无时区的 YYYY-MM-DD HH:mm:ss 按 UTC+8 解析。未知日期不猜测，已过期/非正余额不纳入临期数。统计基于快照，不冒充实时上游扣费。
- 新增 scheduler_mode: credits_expiry。只使用 CPA 已做模型、可用性和冷却过滤的候选，不建立平行账号池；同最高优先级中优先使用7天内最早到期的账号。
- 只使用5分钟内且无错误的积分快照；过期、未知、无临期数据、混合其他 provider 时 Handled=false，交回 CPA 原策略。不在路由热路径查询账单，不改 CPA 的持久优先级。
- 默认仍跟随 CPA；在插件「设置 → 积分调度策略 → 临期积分优先」显式启用。模式不会改变账号内部扣哪个积分包，上游仍拥有扣减权。

## 3. 模型信息
- 展示模型名/ID、上下文、最大输出、推理档位、默认档位、倍率、多模态/文本声明、仅推理、系列。
- 学习 ithtelab/workbuddy-manager 固定提交 508f803425ce74edc5b7dd6a082ceb415e1c7b62 的 server/services/tencent.py：credits、vendor、reasoning.supportedEfforts/defaultEffort、supportsImages、maxInputTokens/maxOutputTokens。保留既有 contextWindow/maxTokens 兼容。
- richer metadata 随每账号解析结果及同一缓存条目传递；不同账号同模型可以有不同倍率。全局目录中明确冲突的元数据不任选一个覆盖；缺失为待核实。
- 上游明确声明的推理档位同时写入 CPA ModelInfo.Thinking.Levels；未验证真实推理、视觉或工具能力。倍率仅显示原文，不另造计费扣费系统；x0.00 不当成空值。
- hy3/hy4 现有执行器固定 high 的行为未偷偷改变，页面额外显示「插件固定 high」，与上游支持/默认档位分开。
- Token 数按1024换算并四舍五入为 K，例如1,000,000 → 977K。截图中的倍率/系列均为标注过的模拟数据，不是线上报价。

## 4. 六个页面与插件专属设置
仪表盘、账号、模型、任务、结果、设置。保留账号初始落地和已有会话，手机六入口为正常文档流内的一行导航。

设置提供自动签到、生命周期维护、令牌续期、旅行任务开关及积分调度策略。只编辑插件拥有的字段，经原生插件配置存储持久化，不复制 CPA 全局设置或鉴权。
每次修改先确认，仅合并触及字段，保存后读回；另读插件 /settings 中实际生效值。保存不等于运行态应用；运行态读取失败显示未知，不把已确认保存误报失败或自动重放请求。生命周期开关可能触发既有维护动作，请先在隔离实例确认。

## 5. 验证与限制
- Go race 548 PASS / 1 optional SKIP，vet、TypeScript、前端构建、77 Node、SDK/宿主契约、发布失败关闭检查通过。
- 浏览器129组 + 普通HTTP。其中42组使用原版 CPAMC 1.25.3 前端（既有27 + 新增15），后端全部为本地夹具；覆盖1366/390/320和四语言。
- 发布 Linux amd64 .so 的10组 C ABI 模拟宿主检查通过，含修复后的双轨描述。源码重新构建一致性另见包审计。
- 27张当前UI实图，14张使用原版官方前端。CPAMP仍为布局模拟。未验收真实CPA后端、真实账号/扣费/任务，不修改生产。
- 最新ZIP包含源码、Linux amd64/glibc≥2.34插件、离线HTML和校验清单；只保留最新一个包。
