# 20：修复动态目录读取链路与错误可见性

版本 v8.0.15-2.0.0-rebuild.20 · 2026-10-07。生产未改动；不把本地模拟成功当成真实腾讯账号恢复。

## 重新核查 Manager

本次重新查询 GitHub main，仍是 `508f803425ce74edc5b7dd6a082ceb415e1c7b62`（v1.0.80），不是假称升级到新版本。

逐项检查：
- `server/services/realm.py`：auth.realm 与 domain、默认客户端5.5.4/CLI2.137.1、分域请求头。
- `server/services/tencent.py`：_envelope、_account_http_client、fetch_models、_parse_model_payload；企业端点与v3并发、候选路径、字符串业务code。
- `server/services/modelcatalog.py`：按realm选未过期账号、最多三个账号尝试、目录缓存、/v1/models及静态能力回退。
- `server/routers/models.py` 与模型来源测试：接口与模型目录口径。

## 找到并修复的代码缺口

1. **realm 被解析层丢弃**：原 parseStored 只接受 region/domain，Manager 的 auth.realm 未映射，随后序列化使该字段彻底消失。对于仅有 realm=global、无domain且令牌不能自描述区域的凭据，会误判为CN。现在兼容嵌套/顶层realm和顶层domain；已有region保留优先级，CodeBuddy域名隔离不变。不迁移或写入凭据文件。
2. **已过期来源仍被自动选中**：令牌非空且未禁用不等于可用于拉目录。现在排除明确已过期的秒/毫秒 expiresAt；未知有效期不虚构过期。手动选过期账号明确失败，绝不悄悄换号。没有配置与全部账号不可用分开显示。不会在目录GET里自动刷新令牌。
3. **业务信封与候选路径兼容过窄**：现在接受数值code与可解析整数字符串code；固定同服务候选路径也处理HTTP400/501、HTTP200业务失败/不可解析目录。HTTP401/403/429及对应业务code不继续候选路径，HTTP5xx（除501）也不自动重试；不跨域，不请求额外未知接口。Manager更广泛的回退不能无条件照搬。
4. **失败被笼统隐藏**：每渠道状态直接可见，获取失败自动展开目录来源。逐接口提供尝试路径、HTTP状态、业务code与安全错误摘要；不返回响应正文、Bearer或代理密码。已过期账号在选择框说明原因；来源选择框在加载中保留但禁用。失败清空旧模型，其他渠道与明确自定义条目仍显示。

## 没有照搬的部分

- 不把所有账号依次请求后混合到列表；坚持每渠道一个明确来源。来源失败让用户显式换账号，不偷偷改变实际CPA选中账号。
- 不用Manager的/v1/models或静态能力回退冒充账号动态获取，也不自动把非聊天/禁用/预设条目注册为可调用模型。
- 网络仍通过CPA host.http操作桥，不新建HTTP客户端或复制Manager的独立代理配置。CPA8.0.15 HTTPRequest契约没有逐请求账号代理字段；本轮未新增绕开宿主的代理能力。网络问题需要核对目标CPA的宿主代理和日志。
- CodeBuddy国际目录仍未验证，不能拿其令牌试探WorkBuddy服务。

## 验证

新增Go测试复现realm丢失、过期来源、字符串业务code和兼容路径，并检查无路由写入与凭据泄漏。Go handler实际序列化的JSON送入浏览器验证，避免只依赖Python手写成功夹具。发布.so C ABI探针增加Manager realm→Global域→企业业务404回退→字符串code=0完整链路检查（宿主/腾讯仍是受控模拟）。

新增16组官方前端浏览器场景：四屏宽，真实Go响应契约、失败状态/过期禁选、四语言错误指引、加载期间选择框与恢复。旧功能回归继续执行。26张当前截图，其中22张原版CPAMC前端；含两张失败诊断截图。

### 未验证与需要的现场证据

尚无用户部署的HTTP状态、业务code、CPA版本/日志或真实腾讯响应，因此**不能认定这些就是用户现场故障的全部原因，也不能宣称真实账号获取已恢复**。没有用户真实账号验收、没有完整真实CPA后端部署验收、没有修改生产。

更新后如仍失败，提供“目录来源”中的渠道、HTTP状态、业务code及错误阶段即可；不要提供token、管理密钥、完整凭据或未脱敏请求头。

## 可重复运行

在源码根目录安装Go1.26、C编译器和Node/npm后：

```sh
mkdir -p validation/iteration-20
(cd frontend && npm ci && npm run typecheck && npm run build && npm test)
WB_HUB_WIRE_FIXTURE="$PWD/validation/iteration-20/hub-wire.json" go test -race -count=1 ./...
go vet ./...
python3 tests/fetch-official-cpamc.py
# 另开终端：python3 tests/preview_server.py --bind 0.0.0.0 --port 8080
# 安装Playwright浏览器与系统依赖后：
node tests/iteration-20-browser.mjs
node tests/capture-delivery-ui.mjs
```

完整本轮统计及报告：VALIDATION-20.json。旧报告只表示对应历史版本，不混入本轮通过数。
