> Latest: **v8.0.15-2.0.0-rebuild.29** — identity-first account loading, bounded/cancelable credit reads. See [iteration 29](docs/ITERATION-29.md). `/credits` without `auth_index` is now paginated (max 4); do not treat a partial page as the full account pool.

> rebuild28 重要变更：插件不再自动启用任何已停用账号。积分恢复后须通过 CPA 原生“启用账号”手动确认；新鲜积分确认耗尽时仍可自动停用。详见 [本轮说明](docs/ITERATION-28.md)。

# CPA 的 WorkBuddy 扩展插件

**v8.0.15-2.0.0-rebuild.23 · 2026-10-07**。CPA8.0.15 / ABI1 / schema6，保留原 WorkBuddy /panel 双菜单。

## 本轮纠正
- 只分国内、国外两个账号/模型渠道。WB / CB 是同区互通入口，默认 WB，不是三类账号池。
- CB 国际凭据不再跳过动态目录；默认走 WB 国际业务、推理及刷新入口。
- 国内按 WB 客户端既有公共API调用，不按网站品牌猜测新接口。
- 国外来源选择同时包含 WB/CB 凭据，选择只影响模型观察，不修改CPA路由。
- 旧 global / intl 来源选择和自定义模型配置兼容；不重命名/批量迁移凭据，不改变历史自动维护策略。
- 保留五工作区及 rebuild22 的单账号积分读取、全账号维护确认、任务和设置修复。

细节与边界：[ITERATION-23](docs/ITERATION-23.md)、[验证结果](docs/VALIDATION-23.json)。旧文档为历史记录，旧三渠道规则已被本轮取代。

唯一最新 ZIP 含源码、Linux amd64 插件、26张实际构建截图的离线HTML与校验清单。构建：Go1.26、C编译器、Node/npm，执行 make frontend / make frontend-test / make test / make build。

截图与HTTP/C ABI验证使用模拟数据，不代表真实腾讯账号验收；生产未修改。首次加载前按需要明确关闭不需要的自动签到/维护功能，默认配置保持兼容。
