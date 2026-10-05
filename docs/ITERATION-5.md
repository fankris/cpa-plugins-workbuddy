# 第五轮：CPA 是主体，插件只做扩展

版本：`v8.0.15-2.0.0-rebuild.5`，2026-10-05。开发阶段按清理要求只保留源码；后续交付要求已更新为一个最新 ZIP + 含 UI 图的 HTML，详见 DELIVERY.md。

## 取舍不是照搬

- CPA 管账号、配置、鉴权、刷新、调度、HTTP、日志和用量上报；插件不得另建相同系统。
- 插件扩展 WorkBuddy 协议、额度/套餐、模型发现和合法任务业务。现有兼容模式不扩大成新账号池。
- 重新检查 Manager `508f803425ce74edc5b7dd6a082ceb415e1c7b62` 的 `web/lib/account-status.ts` 和 `UpstreamReloadNotice.tsx`：学习“状态来自权威来源”“保存不等于运行时生效”，不移植其网关、池状态合并、分组或后端。
- 对照 CPAMC v1.25.3 `src/services/api/plugins.ts`：修改本插件配置仍采用读取最新对象、合并本次字段、PUT 单个插件对象；删除用 null，未知字段保留。不写整个 CPA 配置树。
- CPA latest 已重新核实为 v8.0.15；固定 ABI 1 / schema 6。CPAMC 本轮对照 v1.25.3 源码，不冒称重跑官方真实宿主。

## 实际修复

### 账号状态服从 CPA

提取 `frontend/src/account-status.ts`，让摘要、筛选、行状态和详情继续使用同一函数。

原函数只要存在 runtime 对象，就可能显示“可用”，忽略 CPA 的 `error` / `disabled` 状态。现在：

- 本地停用或宿主 disabled → 已停用。
- 宿主明确冷却 → 冷却中；保留现有明确额度耗尽提示。
- 宿主 error / unavailable 或账号加载错误 → 需要关注。
- 缺少、空白、pending 或未知运行态 → 未知。
- 只有明确 active 且没有上述限制 → 可用。

不根据令牌到期日推测可用，也不照搬 Manager 的累计失败推断：CPA 已恢复 active 时，插件不能用旧失败数擅自判死。可用只是当前宿主状态，仍不证明每个模型真实推理成功。

### 配置修改必须有回读证据

- 身份在**进入队列时**绑定；等待期间更换密钥，旧编辑不会以新身份提交。
- 初始配置必须为对象，防止异常响应被合并成覆盖写入。
- PUT 后回读，只比较本次修改字段；结构比较不受对象键顺序影响，null 删除必须确认字段已不存在。
- PUT 已成功但身份变化、回读失败或值不一致 → “结果未确认”，不误说“保存失败/已中止”，也不自动重试。
- 确认持久化后仍显示“已保存，待运行态确认”，不凭持久化证明插件热加载或模型实际可用。
- 四语言文案更新；不同页面/不同管理者的 GET/merge/PUT **仍不是原子事务**，没有虚构 CAS 或宿主未提供的锁。

## 验证

| 项目 | 本轮结果 |
|---|---|
| Go 全量 race | 533 PASS，1 可选导出测试 SKIP |
| Go vet、固定 SDK 源码契约 | PASS |
| TypeScript、前端构建 | PASS |
| Node 契约 | 53 PASS，新增 20 项 |
| 浏览器既有场景 | 12 + 11 + 3 PASS |
| 新 CPA 主导场景 | 4 PASS：未知/错误状态、假成功配置回读、未确认历史结果 |
| 实际非安全 HTTP | PASS；无 randomUUID 仍可操作 |
| 临时编译的实际 `.so` + 无网络模拟宿主 ABI | 8 组 PASS，回调错误 0，宿主 buffer 遗留 0 |

摘要及本轮验证二进制哈希保存在 `VALIDATION-5.json`。详细临时输出、二进制与依赖缓存按用户要求清理，测试源码保留，可重新执行。

## 验收边界

本轮没有官方 CPA/CPAMC 实例联调，也没有真实账号。真实推理、刷新写回、签到、奖励、试用及旅行仍未验收；不能据此宣称整个插件已全部完成或生产业务全部恢复。生产与云端回滚卷未动。

参考源：
- https://github.com/router-for-me/CLIProxyAPI/releases/tag/v8.0.15
- https://github.com/router-for-me/Cli-Proxy-API-Management-Center/blob/v1.25.3/src/services/api/plugins.ts
- https://github.com/ithtelab/workbuddy-manager/blob/508f803425ce74edc5b7dd6a082ceb415e1c7b62/web/lib/account-status.ts
- https://github.com/ithtelab/workbuddy-manager/blob/508f803425ce74edc5b7dd6a082ceb415e1c7b62/web/components/common/settings/UpstreamReloadNotice.tsx
