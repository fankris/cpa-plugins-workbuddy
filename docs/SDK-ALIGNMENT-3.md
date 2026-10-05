# CPA SDK 对齐 · 第三轮

构建：`v8.0.15-2.0.0-rebuild.3`。2026-10-05 重新查询官方 latest：CPA **8.0.15**、CPAMC **1.25.3**，均非 prerelease。没有为了升版本改用未经验证的开发分支。

## 修复落点

### 1. 宿主 HTTP 操作与取消

旧桥接只转交 URL/headers/body；插件本地 context 的取消无法直接取消正在等待响应头的宿主 RPC。现在对照官方 `internal/pluginhost/host_callbacks.go` 和 `http_operation_bridge.go`：

- 使用 `host.http.operation_open` 获取宿主 operation_id。
- `host.http.do` / `do_stream` 同时携带 operation_id 和 host_callback_id。
- 请求 context 或插件 quiesce 触发 `host.http.cancel`。
- 非流式完成时释放；流式将生命周期转交 stream.Close，读完/失败/关闭后释放。
- worker 在取消回调结束后才释放，避免卸载过程中还有后台 C 回调。
- operation 打开失败直接报错，不绕过 CPA 发第二次 HTTP，也不重放变更。

这是 SDK 能力的接入层，不是另造 HTTP 客户端或通用调度器。需要 CPA 8.0.15 的对应 RPC，不能假设旧宿主同样可用。取消也不意味着撤销已被上游接受的业务操作。

### 2. 请求上下文

补传普通执行、无异步 stream_id 的同步流式执行、OAuth 开始/轮询、凭据刷新入口的 host_callback_id。异步执行和原有 quota 路径继续使用宿主回调上下文。

凭据刷新仍返回 AuthRefreshResponse，由 CPA 自身刷新协调/存储流程写回；未增加 host.auth.save，不改文件名/身份。保留刷新响应缺少有效 expiresIn 时沿用旧到期时间的规则。

本轮没有宣称所有后台/模型发现/管理业务都具有同一个前台回调上下文；没有 host_callback_id 的后台调用依然使用插件实例的宿主操作，由插件关闭时统一取消。

### 3. 流关闭与生命周期

- 修复 setCloser 在错误锁下读取 quiescing 状态的问题，改为锁保护的“已请求关闭”标志。
- 即使关闭发生在上游连接返回之前，晚到的 closer 也会执行；只执行一次。
- stream.Read 与 Close 并发时不再修改/读取未同步的 streamID、reader；阻塞读取期间不持有关闭所需锁。
- 取消仍允许调用宿主 cleanup RPC；不在清理前丢弃 host API 表。

### 4. 数据与错误语义

旧适配器遇到“payload 与终态错误同到”会丢数据或丢错误。现在先交付数据，再保留终态错误，遵守 io.Reader 契约。合法 EOF、异常 EOF 分开；连续空块采用循环读取，不再递归增长调用栈。零长度读取不消耗数据。

## 验证层级（不要混为真实账号验收）

| 层级 | 结果 | 说明 |
|---|---|---|
| 插件 Go race 全量 | 520 命名测试/子测试通过，1 可选导出辅助项跳过 | 比上一轮新增 14 个命名通过项；含并发、取消、scope 和末帧错误 |
| 插件 Go vet / go mod verify | PASS | 当前源码/固定 SDK |
| 官方 SDK 源码中的 HTTP 测试 | 54 命名测试/子测试 PASS（race） | 官方 internal/pluginhost 的 TestHostHTTP*；不是用户真实账号测试 |
| 当前 .so 的 C ABI 探针 | 5 组 PASS，host buffer 遗留 0 | 实际编译库 + 完全无网络 mock host；同步执行、流式收集、终态错误、刷新、等待响应头时 quiesce |
| 官方 CPA/CPAMC 空账号集成 | 12 项 PASS | 当前 .so 实际加载、注册、鉴权/资源/配置、四语言与页面状态 |
| 浏览器/前端回归 | Node 31；夹具 12+11；真实普通 HTTP 场景通过 | 保持第二轮内置页布局，没有恢复无用标题栏或 dock |

对应证据：`validation/iteration-3/`。完整基础回归仍见 `validation/final-go-tests.jsonl`、`frontend-contracts.tap` 等文件。

## 复现新检查

```sh
go test -race -count=1 -run '^TestSDK' ./...
node tests/verify-sdk-http-contract.cjs
python3 tests/sdk-binary-probe.py  # 先按 VERSION 编译 artifacts/workbuddy.so
# 在 go list -m -json 返回的 CPA v8.0.15 module Dir 中：
go test -race -count=1 -json ./internal/pluginhost -run '^TestHostHTTP'
```

C ABI 探针使用合成凭据，拦截所有宿主 HTTP 回调，不会访问 WorkBuddy 服务。实际 .so 的 SHA256 写入探针报告，与包内来源清单核对。

**真实账号推理、刷新写回、签到/奖励/试用/旅行仍未验收；未部署生产。** 此前真实账号边界和独立开发构建身份不变。保留 `/panel` 与 provider/auth 身份，不新增 CPA 已有的网关、账号池、密钥库或配置数据库。
