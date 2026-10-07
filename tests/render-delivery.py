#!/usr/bin/env python3
"""Current-build evidence only; offline images and content hashes."""
from pathlib import Path
import json,hashlib,base64,html
root=Path(__file__).resolve().parents[1];out=root.parent/'deliverables';out.mkdir(exist_ok=True)
h=lambda b:hashlib.sha256(b).hexdigest()
version=(root/'VERSION').read_text().strip();iteration=version.rsplit('.',1)[-1]
v=json.loads((root/f'docs/VALIDATION-{iteration}.json').read_text());c=json.loads((root/'validation/ui-capture.json').read_text());probe=json.loads((root/f'validation/iteration-{iteration}/binary-probe.json').read_text())
binary=h((root/'artifacts/workbuddy.so').read_bytes());assert v['version']==c['version']==version and probe['binary_sha256']==binary and probe['result']=='PASS'
for name,digest in c['assets'].items():assert h((root/name).read_bytes())==digest
figures=[]
for name,digest in c['screenshots'].items():
 b=(root/'validation'/name).read_bytes();assert h(b)==digest
 label=Path(name).stem
 figures.append('<figure><button class="shot"><img loading="lazy" alt="'+label+'" src="data:image/png;base64,'+base64.b64encode(b).decode()+'"></button><figcaption>'+label+' · 当前构建 / 原版 CPAMC 配模拟数据，非真实腾讯账号</figcaption></figure>')
rows=''.join(f'<tr><td>{m["accounts"]}</td><td>{m["identity_page_ms"]} ms</td><td>{m["total_ms"]} ms</td><td>{m["peak"]}</td><td>{m["writes"]}</td></tr>'for m in v['scale_browser'])
page='''<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>WorkBuddy rebuild29 · 渐进读取</title><style>
*{box-sizing:border-box}body{margin:0;background:#f5f7f4;color:#183d36;font:16px/1.8 system-ui,sans-serif}main{max-width:1100px;padding:32px 22px 64px;margin:auto}header{padding:24px 0 32px;border-bottom:1px solid #cadad0}h1{font-size:clamp(32px,5vw,56px);line-height:1.2;letter-spacing:-1px}h2{font-size:26px}h3{font-size:19px}small,figcaption{font-size:13px;color:#526c61}.lead{font-size:19px;max-width:780px}.tag{border:1px solid #b8ccbe;border-radius:7px;padding:5px 12px;display:inline-block;margin-right:8px}.cards{display:grid;grid-template-columns:1fr 1fr;gap:16px}.card,figure{background:white;border:1px solid #ceddd3;border-radius:12px;padding:20px;margin:16px 0}.notice{padding:16px 20px;border-left:4px solid #a47327;background:#fff0d9}section{padding:20px 0}.table-wrap{overflow:auto}table{border-collapse:collapse;width:100%;min-width:550px;background:#fff}th,td{text-align:left;padding:10px 14px;border-bottom:1px solid #d7e1d9}code{overflow-wrap:anywhere;font-size:13px}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#173c34;color:#effaf3;padding:20px;border-radius:8px}.shot{border:0;padding:0;background:#fff;width:100%;cursor:zoom-in}.shot img{width:100%;height:auto;max-height:900px;object-fit:contain}figure{padding:10px}figcaption{padding:10px}a{color:#007c65}dialog{border:0;border-radius:10px;max-width:95vw;max-height:95vh;padding:14px}dialog img{max-width:88vw;max-height:80vh}dialog::backdrop{background:#12362ecc}dialog button{display:block;margin:0 0 10px auto}summary{cursor:pointer;font-size:19px}@media(max-width:640px){.cards{grid-template-columns:1fr}main{padding:16px}h1{letter-spacing:0}}</style><main>
<header><small>WORKBUDDY / CPA EXTENSION · 2026-10-08</small><h1>账号先显示。<br>积分逐步读，可随时停止。</h1><p class="lead">rebuild29 将身份列表与上游账单请求分开。打开账号页不再等待所有账号的积分；只按需读取可见项，批量读取由明确操作启动。</p><span class="tag">@@VERSION@@</span><span class="tag">CN / Intl · 默认 WB 入口</span><span class="tag">一个最新 ZIP</span></header>
<p class="notice"><b>生产未修改。</b>本次截图与规模测试使用合成账号，原版 CPAMC 仅提供界面宿主。实际 .so 通过模拟 C ABI 宿主检查；本轮没有重新运行真实 CPA 宿主验收，也没有真实 CN/Intl 上游业务验收。此前版本的宿主结论不冒充本轮结果。</p>
<section><h2>实现与读取边界</h2><div class="cards"><article class="card"><h3>列表与积分拆开</h3><p>GET /accounts 不调用上游账单接口，返回身份、CPA 状态及已有缓存。进入账号页按需读取可见账号；详情优先排队，不自动扫描全部账号。</p></article><article class="card"><h3>有界队列</h3><p>前端最多 2 个账号同时读取。服务端跨请求共用 4 个账号槽位；每账号积分与套餐最多两路上游请求。读取不是维护，不签到、不领取、不写凭据。</p></article><article class="card"><h3>超时与停止</h3><p>上游读取预算 12 秒，套餐子预算 3 秒；浏览器单次 15 秒，整轮队列 120 秒。停止清除等待项、取消前端请求并忽略迟到结果；未读项不算业务失败。</p></article><article class="card"><h3>旧值不冒充实时</h3><p>失败保留最后快照并明确降级；未知显示“—”，不是零。展示新鲜积分覆盖数；汇总仅为已知值，可能含旧快照。采集时间 45 秒为新鲜度界限，界面每 5 秒重算标签，不自动无限重试。</p></article></div>
<p class="notice">浏览器停止不保证即时撤回宿主已经发出的请求；服务端仍受自身预算约束。CPA 凭据与运行态 SDK RPC 没有上下文取消接口，因此 12 秒不是整个管理调用的绝对墙钟保证。本次计时是在本地模拟宿主上测量，不是生产性能承诺。</p>
<p>沿用 rebuild28：停用账号不会自动启用，过期或失败积分不驱动状态写入，未知写入结果不当成成功。本轮另移除了强制刷新路径绕过保护的备注写回。保留五工作区、原 /panel、双菜单 Description、原生配置/登录及 CB OAuth；没有新增调度信息工作区。</p>
<h3>分页接口兼容提醒</h3><p><code>GET /credits?auth_index=...</code> 保持单账号读取。无 auth_index 时现在只返回一页：默认/最大 limit=4，offset 从 0 开始；响应含 scope、total、returned、has_more、next_offset、snapshot。继续翻页必须携带首轮 snapshot；目录变化需从第一页重启。旧客户端不可再把第一页当作全部账号。</p></section>
<section><h2>本轮验证</h2><p>Go race：@@GO@@ PASS / @@SKIP@@ optional SKIP；vet、TypeScript、构建通过。Node：@@NODE@@ 项通过。既有浏览器回归 179 组通过；新增规模、停止与布局检查 8 组通过。实际发布 .so：@@ABI@@ 项 C ABI 模拟检查通过，回调错误及未释放宿主缓冲区均为零。</p><div class="table-wrap"><table><thead><tr><th>合成账号</th><th>身份列表显示</th><th>整批结束</th><th>前端峰值并发</th><th>业务写入</th></tr></thead><tbody>@@ROWS@@</tbody></table></div><p><small>浏览器时间包含原版 CPAMC 页面启动，刻意挂起初始积分直到列表截图完成；每项积分注入 35ms 延迟。并非纯 /accounts 响应时间或吞吐基准。Go 另验证 1/10/50/200 身份读取零上游、完整分页、目录快照校验、槽位等待取消及实际 12 秒默认超时。</small></p><p>完整细节见 <code>docs/ITERATION-29.md</code>、<code>docs/VALIDATION-29.json</code>。真实账号、当前部署 CPAMP、完整无障碍验收仍未完成。</p></section>
<section><h2>当前构建 UI · @@COUNT@@ 张实图</h2><p>点击放大。全部图片内嵌，可离线查看。规模测试与取消测试均为合成数据，非你的账号。</p>@@FIRST@@<details><summary>展开更多规模与五工作区截图</summary>@@MORE@@</details></section>
<section><h2>安装、回退与包内容</h2><p>目标 CPA 8.0.15 / ABI 1 / schema 6，Linux amd64，glibc ≥ 2.34。先在隔离实例验证；备份现有插件、配置和 auth 数据后，通过 CPA 原生插件管理加载。保持原 /panel 路径。不适用 ARM、Windows 或 musl。</p><p>如不需要自动业务，请在首次加载前显式关闭 checkin_auto / lifecycle_auto。读取停止不等于停用独立后台维护。回退须恢复自行备份的插件与配置，不能撤销已发生的业务操作。</p><pre>index.html — 本离线图文说明
binary/linux-amd64/workbuddy.so
binary/linux-amd64/workbuddy.h
source/ — 源码、测试、文档、许可证
BUILD-INFO.json / CONTENTS-SHA256SUMS

# Go 1.26 + C 编译器 + Node/npm
make frontend
make frontend-test
make test
make build</pre><p>仅保留一个 <code>workbuddy-latest.zip</code>。打包逐文件校验 SHA256 和 ZIP CRC，再从解包源码独立重建核对产物。</p><p>插件 SHA256：<code>@@BINARY@@</code></p></section><footer><small>独立开发构建，无官方背书；生产未动。@@VERSION@@</small></footer></main><dialog id="zoom"><button id="closeZoom">关闭 ×</button><img alt=""></dialog><script>const box=document.getElementById('zoom');document.querySelectorAll('.shot').forEach(b=>b.onclick=()=>{const i=b.querySelector('img');box.querySelector('img').src=i.src;box.querySelector('img').alt=i.alt;box.showModal()});document.getElementById('closeZoom').onclick=()=>box.close();</script></html>'''
for k,value in {'VERSION':version,'GO':v['go_race']['passed'],'SKIP':v['go_race']['skipped'],'NODE':v['node_tests'],'ABI':len(probe['checks']),'ROWS':rows,'COUNT':len(figures),'FIRST':''.join(figures[:3]),'MORE':''.join(figures[3:]),'BINARY':binary}.items():page=page.replace('@@'+k+'@@',str(value))
assert '@@' not in page
f=out/'WorkBuddy-交付说明.html';f.write_text(page)
(out/'DELIVERY-META.json').write_text(json.dumps({'version':version,'html_sha256':h(f.read_bytes()),'binary_sha256':binary,'screenshots':c,'binary_probe':probe,'official_host':{'tested':False},'official_frontend':{'tested':True,'version':'1.25.3','backend':'synthetic fixture'}},ensure_ascii=False,indent=2))
print(f)
