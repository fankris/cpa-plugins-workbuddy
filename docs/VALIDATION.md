# 当前验证与复现

当前保留 rebuild.7 最新源码、唯一最新 ZIP 与离线 HTML。摘要见 [VALIDATION-7.json](VALIDATION-7.json)，变更与限制见 [ITERATION-7.md](ITERATION-7.md)。旧版本安装包及临时二进制、依赖和原始测试输出按清理规则删除；最新 ZIP 含插件与完整源码。

```sh
make frontend
make frontend-test
make test
go vet ./...
make build
```

浏览器测试需安装 Playwright Chromium 和系统依赖，启动 `python3 tests/preview_server.py --port 8080`，运行 tests 下 rebuild-browser、iteration-2-browser、iteration-4-browser、iteration-5-browser、iteration-6-browser、iteration-7-browser、plain-http-browser。输出目录需存在：`mkdir -p validation/iteration-{2,4,5,6,7}`。

ABI 探针需先编译 `artifacts/workbuddy.so`，再运行 `python3 tests/sdk-binary-probe.py`；它是模拟宿主，不是官方宿主联调。真实宿主测试需另行准备官方版本与隔离配置，不能指向生产。
