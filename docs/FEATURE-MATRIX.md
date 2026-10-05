# 功能与来源映射

本表区分既有功能、重构内容、宿主职责和验收范围，不把已有能力包装为新增。

| 范畴 | 来源/学习点 | 本版落点 | 验证级别 |
|---|---|---|---|
| 视觉系统 | Manager globals.css、PageHeader、StatCard、SectionTabs、ManagementBar、CommandPalette | 新 React 页面、中性灰、圆角、底部 dock、快捷入口、紧凑表格、深色/移动端 | 夹具截图与浏览器；实际 CPAMC 嵌入 |
| 账号与套餐 | 基座现有账号/积分/套餐/签到/导入；Manager 状态组织 | 汇总→筛选表格→详情→确认→结果；国内/国际边界 | Go/夹具；真实宿主空列表 |
| 账号所有权 | CPA auth store、native credentials | 不建第二账号池；登录与启停、刷新复用 native v8 | 接口契约与夹具；真实无账号，不声称真实刷新通过 |
| 模型 | 基座模型发现/目录/禁用；参考 panel 的诊断组织 | 目录与按账号发现分开；异步响应不覆盖新选择；未知配置字段保留 | Go/Node/夹具；原生配置真实读写 |
| 任务与活动 | 基座任务、签到、旅行；Manager 任务/结果组织 | 接受任务与已完成奖励；accept-only 批次、真实轮询、协作取消 | Go/夹具；真实宿主无运行状态接口 |
| 操作结果 | Manager 确认/结果体验与 panel 状态区分 | 失败/部分失败/已领取/未确认，不用统一绿色成功掩盖错误；脱敏导出 | Node/夹具 |
| 统计/日志 | CPA usage 与 log | 不另建历史数据库；原生日志入口；本地免费用量计数保留准确标签 | Go/界面；未测试真实推理统计全链路 |
| 调度 | CPA selector + 基座显式兼容模式 | 默认交给 host；显式 builtin/off/credits 保留 | Go race、固定 SDK 源码契约；未用真实账号验证多账号路由 |
| 上游 HTTP | 基座宿主 bridge，按当前 SDK 保持契约 | host.http/stream，不新增直连网关或并行通用重试体系 | Go bridge/loopback 测试；未发真实业务请求 |
| SDK/lifecycle | CPA 8.0.15 pluginapi/pluginabi | schema 6、元数据、ConfigFields、资源、native v8 与 custom v0 | 当前二进制实际加载；native 配置回写 |
| 语言与主题 | CPAMC 1.25.3 实际 storage/iframe 行为 | 同源四语言与主题跟随；父窗口内存保存草稿，不造跨域协议 | 实际官方语言菜单四轮切换/iframe remount |

## 明确不移植

Manager 的独立网关、SQLite 账号池、管理 API Key 系统、Docker 一键更新、独立统计服务不移植到插件。它们不是此 CPA 插件交付内容。没有用工作区中既有的独立 Manager 生产部署充当本插件验收。

合法任务模块保留；合成活跃上报入口 410、配置强制关闭。没有声称用户同意删除整个任务模块，也没有实现虚假完成或额度规避。

## 待真实账号验收

由账号所有者提供隔离测试账号并明确授权后，才能继续验证：native token 刷新写回、推理/流式/tool calling、用量上报、国内签到与任务结果、符合资格的奖励/试用、旅行、过期与限流恢复。不得把当前通过的 mock 当作这些项目已通过。
