# rebuild25 — CN / Intl 术语统一

本轮修正界面用语，不改变 rebuild24 的业务逻辑。

- 账号筛选/详情、模型渠道/来源/诊断、任务提示、插件设置范围均采用 CN / Intl。
- 简体、繁体、English、Русский 均保留这两个固定名称；专业名称不当作普通文案翻译。
- 检查兼容的 global 显示项，统一为 Intl；未知渠道显示未知，不臆测区域。
- CPA 原生插件配置中的登录渠道说明同步修正；枚举值 cn / intl 保持不变。
- 不重写用户账号名、模型 ID、上游名称、凭据或 API 字段，不改变 WB 默认入口。
- 术语约定见 TERMINOLOGY.md；既有业务修复见 ITERATION-24.md（历史记录保留原文）。

验证：Go race 615 PASS / 1 optional SKIP；vet、TypeScript、前端构建 PASS；Node 128 PASS；浏览器46组回归包含4语言×4宽度的 CN / Intl 原名断言，普通HTTP通过。发布二进制 C ABI 模拟验证通过。30张截图重新采集于本次构建。

截图及业务测试使用模拟数据，不代表真实账号或完整CPA生产验收；生产未改动。只保留一个最新ZIP。
