# CPA 的 WorkBuddy 扩展插件

**当前源码：v8.0.15-2.0.0-rebuild.21 · 2026-10-07。** 以CPA为主体，参考Manager业务实现，不移植独立后台。

目标CPA8.0.15、ABI1/schema6；保留WorkBuddy菜单、provider/auth身份和原`/panel`。唯一最新ZIP包含源码、Linux amd64插件、26张实际截图的离线HTML及校验清单。

## 本轮：模型优先，不再默认展示诊断墙

- 默认显示模型表；“全部 / 动态获取 / 自定义”标签与搜索、渠道、排序直接操作。
- 同ID聚合，来源与渠道分别标注；显示参数有差异时注明取值来源，详情保留各来源原值。
- 来源账号按需打开对话框；技术信息默认折叠，不挤占列表。
- 未支持/未配置不触发通用获取失败警告；真实失败仅提示具体渠道与原因。
- 修复旧自动展开状态迁移；无账号、无模型、筛选无结果提供不同处理入口。

[本轮设计与实现](docs/ITERATION-21.md) · [验证证据](docs/VALIDATION-21.json) · [职责划分](docs/FEATURE-MATRIX.md)

## 保留五工作区

| 页面 | 职责 |
|---|---|
| 仪表盘 | 积分汇总、临期与连接信息，无独立结果历史页 |
| 账号 | 搜索、多选、导入、原生启停与令牌刷新、积分与合法业务 |
| 模型 | 每渠道一个观察来源账号，完整动态与显式自定义条目；原生模型配置确认及读回 |
| 任务 | 真实进度、接受任务、领取已完成奖励、批次与取消 |
| 设置 | 插件业务开关和运行态，不复制CPA通用配置或登录 |

Manager realm凭据兼容、过期来源排除、双接口目录与缓存隔离、CPAMP配置只读协商和固定写回保留。选择目录账号不会切换实际路由；目录条目不会自动注册为可调用模型。

## 构建与边界

需要Go1.26、C编译器及Node/npm：`make frontend`、`make frontend-test`、`make test`、`make build`。浏览器回归使用Playwright及本地fixture，见tests与[交付规则](docs/DELIVERY.md)。20轮实际Go响应浏览器测试须先用`WB_HUB_WIRE_FIXTURE="$PWD/validation/iteration-20/hub-wire.json" go test -run TestManagerRealmThroughHubAndHostHTTP`生成样本（先创建目录）。

发布目标为Linux amd64、glibc≥2.34，不是ARM/Windows/musl通用包。安装前自行备份插件、配置和auth；自动业务默认值保留，如不需要应先关闭checkin_auto/lifecycle_auto。

**生产未改动，未使用真实账号验收。** 预览与截图均为模拟数据。CodeBuddy国际目录尚未验证；不跨服务试探、不用静态表假称动态成功。测试通过不代替真实账号或用户界面验收。
