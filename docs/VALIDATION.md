# 当前验证：rebuild.18

见 [结构化报告](VALIDATION-18.json)、[本轮实现](ITERATION-18.md)。

Go567 PASS / 1 optional SKIP；vet、TypeScript、构建通过；Node102（退役11项结果列表分组测试，新增9项导航/移除契约）；浏览器195组+普通HTTP，其中94组原版CPAMC前端；发布ABI11。截图37张，20张原版官方前端。

结果页退役，旧结果搜索/导出测试改为业务页反馈、仪表盘及五项导航验收。全程模拟后端，真实账号与用户CPAMP未验收，生产未动。

## 历史验证（不代表当前页面功能）

# 当前验证：rebuild.17

见 [结构化报告](VALIDATION-17.json) 与 [实现及边界](ITERATION-17.md)。

Go race 567 PASS / 1 optional SKIP；vet、TypeScript、构建通过；Node104；浏览器174组+普通HTTP；原版CPAMC前端74组（模拟后端）；发布二进制ABI11组。HTML37张截图，20张原版CPAMC前端。

没有真实腾讯账号、用户CPAMP实例或生产验收。目录仅观察，不自动注册为CPA路由；国际CodeBuddy目录未验证。

## 历史记录

# 当前验证与复现

当前保留 rebuild.16 最新源码、唯一最新 ZIP 与离线 HTML。摘要见 [VALIDATION-16.json](VALIDATION-16.json)，变更与限制见 [ITERATION-16.md](ITERATION-16.md)。旧版本安装包及临时二进制、依赖和原始测试输出按清理规则删除；最新 ZIP 含插件与完整源码。

```sh
make frontend
make frontend-test
make test
go vet ./...
make build
```

浏览器测试需安装 Playwright Chromium 和系统依赖，启动 `python3 tests/preview_server.py --port 8080`，运行 tests 下 rebuild-browser、iteration-2-browser、iteration-4-browser、iteration-5-browser、iteration-6-browser、iteration-7-browser、iteration-9-browser、iteration-10-browser、iteration-11-browser、plain-http-browser。输出目录需存在：`mkdir -p validation/iteration-{2,4,5,6,7,8,9,10,11}`。

ABI 探针需先编译 `artifacts/workbuddy.so`，再运行 `python3 tests/sdk-binary-probe.py`；它是模拟宿主，不是官方宿主联调。真实宿主测试需另行准备官方版本与隔离配置，不能指向生产。

官方前端回归需先运行 `python3 tests/fetch-official-cpamc.py`，再运行 `node tests/iteration-12-browser.mjs`。其后端仍是夹具，不是官方 CPA 后端验收。

本轮：77 项前端、539 Go PASS / 1 SKIP、114 组浏览器 + 普通 HTTP、10 组 ABI；另运行 iteration-12-browser 与 iteration-13-browser，后者需原版官方 HTML（`python3 tests/fetch-official-cpamc.py`）。原版官方前端共 27 场景，不等于真实后端验收。

Rebuild14 当前：Go548 PASS/1 SKIP，77 Node，129浏览器+HTTP，10 ABI；实际官方前端42组。新增 iteration-14-browser。浏览器旧测试已改用稳定 data-section 选择器，原“无设置入口”限制按用户新增插件设置页需求更新；仍禁止重复原生OAuth/全局配置入口。

Rebuild15：548 Go PASS/1 SKIP、87 Node、141浏览器+HTTP、10 ABI。新增 iteration-15-browser 与 verify-cpamp-contract.py；CPAMP源码及接口夹具验收，不是用户实际部署验收。

Rebuild16：548 Go PASS/1 SKIP、97 Node、153浏览器+HTTP、10 ABI；新增 iteration-16-browser，原版CPAMC前端54组，后端为夹具。
