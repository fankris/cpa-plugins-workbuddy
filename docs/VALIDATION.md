# 最终验证记录 · 2026-10-05

构建：`v8.0.15-2.0.0-rebuild.1`。所有测试在独立工作区完成，未部署生产。

| 检查 | 最终结果 | 证据 |
|---|---|---|
| TypeScript / esbuild | PASS | 已生成 panel.js / panel.css；源码 package-lock 固定 |
| Go race 全量 | 506 个命名测试/子测试 PASS，0 fail | validation/final-go-tests.jsonl |
| Go vet | PASS（无输出） | validation/go-vet.log |
| Node 前端 + native 配置 | 28/28 PASS | validation/frontend-contracts.tap |
| 固定 SDK 调度契约 | PASS，仅源码静态检查 | validation/host-contract.json |
| release 失败即停保护 | PASS | validation/release-guard.log |
| 浏览器夹具 | 12 项 PASS，0 page errors、0 外部浏览器请求 | validation/rebuild-browser.json |
| 官方 CPA 8.0.15 + CPAMC 1.25.3 | 12 项 PASS，0 page errors | validation/real-host-integration.json |

Go 测试数量包含命名子测试，不表示 506 个独立顶层测试。源码契约 JSON 的 `realCPALoadingTested:false` 仅描述该静态脚本；真实加载证据在独立 real-host-integration.json 中。

## 真正测试了什么

当前打包的 `.so` 被官方 CPA 二进制实际加载。真实主机验证：注册/启用、3 个原生 ConfigFields、管理接口匿名 401、公共 HTML/JS/CSS 与未知资源 404、退役接口 410、空账号不造假、原生配置 PUT/GET 保留未知字段且无法启用合成上报、按账号最新运行查询、官方登录“记住密码”后的密钥继承、四语言切换后 iframe 销毁重建仍保留草稿与搜索、草稿未写入浏览器持久/会话存储、只读设置弹窗与原生设置链接。

真实浏览器测试发现并修复了 CPAMC 浮动工具栏遮挡插件顶部设置按钮的问题，最终验证使用实际点击，而非绕开被遮挡控件。最终模型选择竞态/未完成操作恢复修复后重新构建并重新跑完两组浏览器与 Go race。

夹具另验证分页、稳定 iframe 内仅翻译不发 API、原生刷新路由、模型写入保留未知配置、取消确认不变更、领取后重新读取状态、区域限制、批次轮询、敏感响应不保留、390px 无页面溢出、内存恢复及跨域语言不可用提示。

**区别：**实际 CPAMC 切语言导致 iframe 重建，因此会重新执行启动读取；不能宣传真实宿主切语言“零 API 请求”。“零外部请求”只适用于夹具浏览器；官方宿主启动时有公共元数据获取，不能声称整个宿主完全无外联。

## 明确没有测试

- 真实账号推理、流式/tool calling、真实凭据刷新写回。
- 真实签到/试用/已完成奖励/旅行操作及上游资格结果。
- 真实多账号调度、长期稳定性、大量账号压力、跨平台二进制。
- 生产升级或回退演练。

这是一份当前独立重构构建的验证记录，不是上游正式认证或生产验收报告。

## 复现

```sh
npm --prefix frontend ci
npm --prefix frontend run typecheck
npm --prefix frontend run build
node --test tests/frontend-contracts.mjs tests/native-config-contract.test.cjs
go test -race -count=1 -json ./... > validation/final-go-tests.jsonl
go vet ./...
node tests/verify-host-contract.cjs
bash tests/release-failure.sh
```

夹具：先运行 `python3 tests/preview_server.py --port 8080`，安装 Playwright Chromium/系统依赖后运行 `node tests/rebuild-browser.mjs`。脚本先重置夹具；截图是合成数据，不是用户真实账号。

真实主机：另行下载对应官方 CPA/CPAMC，创建空 auth 目录、全新的本地测试管理密钥和插件目录，将当前 `.so` 放入并在隔离配置中启用；设置插件配置中的 `preserved_fixture_key: keep-me` 供未知字段保存断言。只监听数值 loopback，禁止指向生产。测试通过 `CPA_TEST_BASE=http://127.0.0.1:8317` 与 `CPA_TEST_KEY_FILE=/path/to/test-key` 指定主机和密钥文件，然后运行 `node tests/real-host-integration.mjs`。该脚本会短暂修改并在 finally 恢复隔离插件配置。

不分发真实主机配置、测试管理密钥、auth 目录、完整原始 host log 或官方二进制。安装 Playwright 的系统库方式因环境而异；不把本工作区 `.cache` 绝对路径当成用户安装要求。


补充：Go 回归另有 1 个可选 wire-shape 导出辅助测试因未设置 `WORKBUDDY_WIRESHAPE_OUT` 而跳过；不是失败，也未计入 506 个通过项。
