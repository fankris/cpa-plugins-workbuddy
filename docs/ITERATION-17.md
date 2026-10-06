# Iteration 17 — 账号模型目录：双来源获取与 CPA 配置隔离

版本：v8.0.15-2.0.0-rebuild.17。日期：2026-10-06。

## 已实现

- 插件 Go 后端新增 GET `/models/directory?auth_index=...` 与 POST `/models/directory/refresh`（body: auth_index），均位于已有插件管理命名空间、复用 CPA 凭据与 host.http operation bridge。
- CN: copilot.tencent.com 企业 `/console/enterprises/personal/models` + `/v3/config`；WorkBuddy Global: www.workbuddy.ai 企业 `/v2/enterprises/personal/models`（仅 HTTP 404/405 回落 `/console/...`）+ `/v3/config`。
- 两路并发，共享 15 秒预算和管理回调 scope。不把 401/403/429/500 当换路径理由，不跨域探测。目录专用三段式 UA 按固定参考源码 5.5.4 / CLI 2.137.1；不改聊天、账单的现有 UA。
- CodeBuddy Intl 保持未验证状态，不向 WorkBuddy 发送其凭据；无 token 时明确失败。
- 解析 data.models 对象列表和 data 字符串窄表。同 ID 以 v3 条目为准，企业路补未见 ID。图片声明单独合并；两路冲突标未知并保留来源。
- 保留显示名、原始容量、倍率原文、全部推理档位、默认档位、说明、标签、默认模型、工具调用/推理声明、推理摘要及上游禁用原因。
- 完整目录保留预设、非聊天、上游禁用条目。与参考管理器的部分过滤策略不同：该视图用于观察目录，不冒充聊天模型列表。
- 完整成功快照缓存 5 分钟；服务/账号/令牌隔离、最多128项，同账号普通读取合并。强制刷新绕过并失效旧快照；部分成功不缓存，失败不回填静态/旧列表。
- 页面目录无启停按钮，提供详情；CPA 模型配置保留既有启停确认、原生配置读回。原 `/models`、`/models/refresh` 与 `model.for_auth` 路由解析不变；**新目录不会自动扩大 CPA 注册列表**。
- 系列优先 vendor，其次 ID 前缀浏览分组，详情标明推导。未知不推断能力。倍率排序支持 `x0.34 credits`，复合、负数、未知值仍排最后。
- 目录统计：条目数、有推理档位条目数、上下文≥131072条目数、最大上下文；不随筛选变化。K=1024，详情给出原始 tokens。数量不写死为28。
- 原版官方宿主语言/刷新 remount 后保留账号和筛选；迟到回复及迟到错误不能污染新账号。异常JSON对象不算成功，手动重试仍可用。

## 界面入口

模型 → 账号模型目录 → 选择账号 → 重新拉取。来源详情按需展开；行内查看详情显示原始 token 和逐来源能力声明。

## 验证

Go race 567 PASS / 1 optional SKIP；vet PASS；TypeScript/build PASS；Node 104 PASS；浏览器174组+普通HTTP；发布.so ABI11组。
新增目录浏览器20组在原版CPAMC1.25.3、1366/1024/390/320及四语言中执行。原版CPAMC浏览器合计74组，后端始终为本地模拟。
37张最终UI截图，其中20张原版官方前端。新增Go目录测试包含并发、路径协商、字段解析、缓存隔离、取消、源码快照不变及不污染路由缓存。

## 未做 / 不能据此声称

没有登录用户账号、没有请求真实腾讯账号接口、没有生产变更。不能保证真实用户返回28条，也不能据目录存在证明可调用、计费或工具/多模态能力。真实CPAMP用户部署未验收，保留之前源码+夹具契约验证。
不新增测试台：本轮只补目录获取；不复制 Manager 独立登录、数据库或服务架构。
CPA配置视图和路由仍使用既有解析策略，新目录独有项不会自动可用；如需接入，必须另外核实该模型的服务协议与 CPA 路由配置。

参考：ithtelab/workbuddy-manager @ 508f803425ce74edc5b7dd6a082ceb415e1c7b62，server/services/tencent.py、realm.py。保留项目MIT许可证和来源说明。
