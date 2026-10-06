# 第九轮：CPAMP 标题栏 / CPAMC 悬浮控件适配

版本：`v8.0.15-2.0.0-rebuild.9`，2026-10-06。

## 为什么不能统一预留 80px

CPAMP 标题栏通常位于 iframe 外，宿主已经为它安排了空间；插件再留 80px 会重复占位。CPAMC 的顶部悬浮控件与 iframe 重叠，但桌面多数情况下只占右上角，不应让整页下移。

## 修改

- 删除嵌入页面统一 80px 的 CSS。独立打开时保留现有布局。
- 新增只读宿主几何观察器 `frontend/src/host-layout.ts`：同源时测量 iframe 与已识别的悬浮工具栏、移动端左侧按钮、语义标题栏/工具栏的交集；用宿主命中测试排除不在前景的元素。
- 标题栏在 iframe 外：额外避让为 0，插件保持桌面 12px / 手机 8px 基础内边距。
- 桌面悬浮控件：仅给顶部导航左右预留对应区域，并确保下一行操作位于控件下方；不缩窄整个内容区。
- 窄屏剩余宽度不足：切换为按实测底边加安全间距的纵向避让，不强挤四个导航按钮。
- 弹窗单独使用顶部安全区，确保关闭按钮不进入宿主悬浮区域，内容仍可内部滚动。
- iframe 卸载时显式释放宿主观察器和事件监听，避免 CPAMC 语言切换/路由重挂载残留。
- 宿主控件尺寸、显示状态、iframe 尺寸及滚动位置变化时重新计算。控件隐藏即释放空间；不改写宿主 DOM、样式、事件或鉴权。
- 跨域或沙箱无法读取宿主时不猜测品牌，保留 80px 保守回退；该情形不能保证任意宿主覆盖区均被准确避让。
- 顺便修正运行时版本元数据：由 VERSION 构建注入，不再停留在 rebuild.1。

## 依据与边界

对照 CPAMC v1.25.3 的 `PluginResourcePage.tsx`、`MainLayout.tsx` 和 `src/styles/layout.scss` 中 `.floating-actions` / `.mobile-sidebar-actions` 布局。未提供 CPAMP 具体版本，按用户描述构造“标题栏在 iframe 外”的模拟布局，不冒称完成实际 CPAMP 实机验收。

参考源码：
- https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/v1.25.3/src/components/layout/MainLayout.tsx
- https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/v1.25.3/src/styles/layout.scss

## 验证结果

新增 12 个宿主布局场景：两类布局 × 320/390/1024/1366 宽度，四语言导航无遮挡；额外测试工具栏变宽、隐藏、跨域回退以及 iframe 被移除后释放宿主监听器。检查导航、主要操作与弹窗关闭按钮的实际宿主命中区域，而非只看 CSS 数字。

| 模拟布局 | 宽度 | 插件顶部 padding | 额外策略 |
|---|---:|---:|---|
| CPAMP 标题栏 | 1366 / 1024 | 12px | 无重复顶部占位 |
| CPAMP 标题栏 | 390 / 320 | 8px | 无重复顶部占位 |
| CPAMC 悬浮控件 | 1366 | 12px | 导航右侧避让 208px |
| CPAMC 悬浮控件 | 1024 | 12px | 导航右侧避让 214px |
| CPAMC 手机悬浮控件 | 390 / 320 | 74px | 按模拟控件实测高度纵向避让 |

以上是夹具测量值，不是写死的品牌常量。13 张截图含 3 张宿主布局模拟实图，剩余展示插件各工作区。

完整回归：Go race 539 PASS / 1 可选 SKIP；Go vet 与 SDK 契约 PASS；Node 61 PASS；浏览器 74 场景 + 普通 HTTP PASS；编译后的 .so 模拟宿主 ABI 10 组 PASS。第八轮积分业务修复保留。

未进行实际 CPAMP/CPAMC 部署或真实账号验收，未修改生产。自定义宿主未识别的覆盖层、跨域高度超过回退值等仍需对指定版本测试，不承诺所有历史/第三方宿主零遮挡。
