# WorkBuddy 开发检查点 · dev.1

日期：2026-10-05（香港时间）  
版本：`v8.0.13-1.0.42`  
分支：`feature/workbuddy-integration`（本地未提交；未推送、未打 tag、未发布）

## 结论

这是 V3 的第一批开发检查点，不是完整升级交付，也不是可直接部署的正式版本。按最新指示保留 `/panel`、`WorkBuddy 面板` 页面标题、菜单及 provider/auth 标识，没有进行 `/WorkBuddy` 迁移。新加坡生产服务、账号数据及美国回滚卷均未改动。

## 本批变更

1. 新增 `panel-i18n.js`，提供 `zh-CN`、`zh-TW`、`en`、`ru` 四种语言的部分静态文本、辅助属性、忙碌状态和密度切换标签；HTML 共 54 个翻译标记。
2. 对齐 CPAMC 的 `cli-proxy-language` 存储格式与语言选择规则。同源嵌入时读取宿主偏好并响应变更；不重建业务状态。跨域时明确提示无法读取宿主偏好，并回退到本地/浏览器语言，不假装已有官方消息桥。
3. 使用宿主声明的管理与资源路径；校验并转义注入 HTML 的路径，兼顾外层反向代理前缀，保留原 v0 路径回退。未知资源返回真正的 HTTP 404。
4. 修复原 Intl 账单测试设置错测试地址的问题；测试直连辅助函数仅允许数字形式的 loopback 地址，重定向同样受限。生产请求仍走宿主 `host.http`，没有另建 HTTP 系统。
5. 发布流程遇到编译失败或空产物时停止，不再打印虚假的构建成功；负向测试验证失败时不生成安装包。
6. 修正源码仓库元数据；保留原 MIT 许可证，补充 CPAMC、CPA SDK、YAML 与 Go 的许可证文件。

没有复制 Manager/panel 的独立网关或账号池，也没有另建宿主已有的鉴权、配置存储、统计或调度系统。没有新增伪造活动、虚假任务完成、批量获利注册或额度规避逻辑。

## 来源

- 基础仓库：https://github.com/fankris/cpa-plugins-workbuddy
- 基线提交：`ba6992e30862e83e07b2a0d4e23de6bed6d1eb68`
- SDK：CLIProxyAPI `v8.0.13`
- CPAMC 语言行为参考提交：`ee79a794526a30c03748a8864a9ac6589a31833b`
- 更完整的来源与许可说明见 `THIRD_PARTY_NOTICES.md`。

## 验证结果及边界

| 检查 | 结果 | 证据/边界 |
|---|---|---|
| 最终 Go race 全量测试 | 通过 | 493 个测试/子测试通过，1 个跳过；`validation/go-tests.jsonl` |
| Go vet | 通过 | 最终构建流程执行，随后独立复核通过 |
| Node 前端单测 | 13/13 通过 | `validation/node-tests.tap` |
| 原 JS helper 测试 | 通过 | `validation/legacy-js-tests.log` |
| 编译失败发布保护 | 通过 | `validation/release-test.log` |
| Chromium 浏览器测试 | 通过 | `validation/browser-results.json`；仅本地宿主夹具 |
| Linux amd64 c-shared 编译 | 通过 | `artifacts/workbuddy-linux-amd64-dev.1.so`；SHA256 校验通过 |
| 真实 CPA/CPAMC 集成 | **未测试** | 不可据本地夹具结果宣称已兼容生产环境 |
| 真实账号/模型/任务/奖励 | **未验证** | 本批未发起真实账号业务或奖励领取 |

跳过项：`TestEmitWireShapeForHostCheck` 需要设置 `WORKBUDDY_WIRESHAPE_OUT` 才输出宿主检查夹具；未执行该项，因此没有得到真实宿主 wire-shape 联调结论。

浏览器测试覆盖：四语言、本地同源宿主偏好更新、重新加载、跨域明确回退、忽略未授权消息、观察器恢复；切换语言时保留搜索、管理密钥输入、导入草稿及计数器，未触发额外 API 请求。测试记录没有页面错误或外部网络请求。它不代表真实任务全过程状态已验证。

### 原始基线测试异常

原 Intl 测试错误覆盖了 global 而非 Intl 地址，导致使用固定的虚构测试 token 请求 `https://www.codebuddy.ai/probe` 并以 EOF 失败，没有使用用户凭据。开发分支已修正地址覆盖并增加 loopback 限制；原始记录在 `validation/baseline-tests.log`。请不要重新运行未修复的原基线测试。

## 尚未完成的 V3 工作

- 四模块完整实现：账号与套餐、模型诊断、任务与活动、诊断与操作结果。现有导航不应计为本批新增功能。
- 动态账号、模型、任务、错误及结果文案的完整四语言覆盖；当前仍会出现中文动态文本。
- 原生 ConfigFields、未知配置键保留语义及原生配置接口协作。
- 宿主调度委托、刷新协调及并发语义。
- 既有任务/活动行为盘点；真实状态、进度、授权操作与真实完成奖励的展示及回归。
- 真实 CPA/CPAMC、反向代理、宿主版本兼容性与回滚演练。
- 跨域语言同步需要明确的可信宿主桥接方案，目前官方页面没有可直接复用的桥。

后续开发优先继续宿主配置/调度边界与动态文案，而不是部署当前检查点。涉及真实凭据、计费调用、奖励操作或云服务变更时需另行确认。

## 构建与复测

使用 Go 1.26.0、C 编译器和 Node.js。依赖安装可能需要联网；浏览器测试另需 Playwright 和 Chromium 系统依赖。

```sh
go test -race -count=1 ./...
go vet ./...
node --test tests/panel-i18n.test.cjs tests/panel-paths.test.cjs
bash tests/release-failure.sh
make build
```

浏览器测试：安装 Playwright 后，设置 `PLAYWRIGHT_MODULE` 指向其模块路径，再运行 `node tests/browser-i18n.cjs`。本工作区的具体命令和缓存路径不应作为生产部署依赖。

开发二进制只在本工作区完成编译；动态链接系统 C runtime，引用最高 `GLIBC_2.34` 版本符号，不能假设适用于较旧发行版或 musl/Alpine。未构建或验证 arm64，未验证目标宿主加载。源码包不包含开发二进制；产物和校验文件保存在工作区 `artifacts/`。

## 回退

线上无变更，所以无需执行生产回滚。开发回退应保留当前源码包后，另建目录检出上述基线提交；不要覆盖生产认证文件、数据库或配置。原基线存在已说明的测试联网问题，如需测试必须先应用测试隔离修复。未来上线前需另备份宿主配置及旧插件，并完成目标环境的加载、功能和回退验证。
