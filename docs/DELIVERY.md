# 每轮交付规则

用户要求：最终提供 HTML 文档，展示实际 UI 图；每次迭代只留一个最新 ZIP。

## 保留内容

- 工作源码：`workbuddy-rebuild/plugin/`。
- 独立离线 HTML：`workbuddy-rebuild/deliverables/WorkBuddy-交付说明.html`。
- 唯一最新 ZIP：`workbuddy-rebuild/deliverables/workbuddy-latest.zip`，内含完整源码、Linux amd64 插件、HTML、许可证、构建信息和文件校验清单。
- 外部 SHA256SUMS、交付元信息与审计摘要体积很小，可保留。不保留旧版本 ZIP。

## 每次生成

1. 更新版本、源码和对应 `docs/VALIDATION-N.json`，如实记录已执行/未执行的验证。
2. `make frontend`；需要 Go 1.26、C 编译器与 Node/npm。
3. 安装 Playwright Chromium 和所需系统依赖，启动本地 `python3 tests/preview_server.py --port 8080`。
4. `node tests/capture-delivery-ui.mjs`：采集三十三张当前页面实图，记录版本、前端资产与截图哈希。只允许本地模拟数据，不抓取用户真实账号或密钥。
5. `make release`：编译 `.so` → 实际二进制模拟宿主 ABI 探针 → 离线 HTML → 校验并发布一个最新 ZIP。目标仅为 Linux amd64；不冒充 ARM/Windows/musl 包。
6. 查看 HTML，检查图片、离线加载、放大、窄屏排版。检查 ZIP CRC、内部 SHA256、源码/二进制/HTML 完整性。
7. 验证成功才替换最新包、删除交付目录中旧 workbuddy ZIP；清理临时依赖、截图文件、编译产物和缓存。不删除工作源码、当前 HTML 或唯一最新 ZIP。

## 防止错包

缺少二进制或验证证据、版本不一致、UI 资产/截图哈希不一致时打包失败。已有最新 ZIP 不会提前删除。HTML 自含所有图片与样式，无 CDN；图注明模拟数据。先生成 HTML，后打 ZIP，ZIP 的整体哈希置于外部 SHA256SUMS，避免把自身哈希写进自身。

本地只留一包不等于可以不备份生产；安装者自行保存现有插件、CPA 配置与 auth 数据用于回退。默认不操作云端或生产。

Rebuild 13：23 张实际截图，其中 10 张来自原版 CPAMC 前端；模型/任务/结果页放在文档最前。参见 ITERATION-13.md。

Rebuild14：27张UI图（14张原版CPAMC前端），详情见ITERATION-14.md。

Rebuild15：31张当前实图，其中14张原版CPAMC前端；CPAMP专题4张为接口/布局夹具，文档明确区别。

Rebuild16：33张截图，16张原版CPAMC前端；模型页及展开筛选放在文档最前。
