#!/usr/bin/env python3
"""One audited latest ZIP: source + plugin + offline illustrated documentation.
Old archives are pruned ONLY after the replacement archive validates.
"""
from pathlib import Path
import hashlib,json,os,struct,zipfile
root=Path(__file__).resolve().parents[1]
out=root.parent/'deliverables';out.mkdir(exist_ok=True)
version=(root/'VERSION').read_text().strip()
h=lambda b:hashlib.sha256(b).hexdigest()
meta=json.loads((out/'DELIVERY-META.json').read_text())
assert meta['version']==version
binary=(root/'artifacts/workbuddy.so').read_bytes()
assert binary[:4]==b'\x7fELF' and binary[4]==2 and binary[5]==1 and struct.unpack_from('<H',binary,18)[0]==62,'Only Linux amd64 is supported by this bundle'
assert h(binary)==meta['binary_sha256']==meta['binary_probe']['binary_sha256']
assert meta['binary_probe']['result']=='PASS'
html=(out/'WorkBuddy-交付说明.html').read_bytes()
assert h(html)==meta['html_sha256']
html_check=json.loads((out/'HTML-CHECK.json').read_text())
assert html_check['result']=='PASS' and html_check['html_sha256']==h(html),'Revalidate current HTML in browser'
for name,digest in meta['screenshots']['assets'].items():assert h((root/name).read_bytes())==digest,'Stale screenshot assets'
rootnames={'Makefile','VERSION','LICENSE','README.md','README_CN.md','THIRD_PARTY_NOTICES.md','CHANGELOG.md','go.mod','go.sum','.gitignore','panel.html','panel.js','panel.css','panel-i18n.js'}
files=[p for p in root.iterdir() if p.is_file() and (p.suffix=='.go' or p.name in rootnames)]
for folder in ['frontend','tests','docs','licenses']:
 files += [p for p in (root/folder).rglob('*') if p.is_file() and not p.is_symlink() and not any(n in p.parts for n in ['node_modules','__pycache__','.cache','.git','legacy-v1'])]
mapping={'source/'+str(p.relative_to(root)):p.read_bytes() for p in files}
mapping.update({'index.html':html,'binary/linux-amd64/workbuddy.so':binary,'binary/linux-amd64/workbuddy.h':(root/'artifacts/workbuddy.h').read_bytes(),'LICENSE':(root/'LICENSE').read_bytes(),'THIRD_PARTY_NOTICES.md':(root/'THIRD_PARTY_NOTICES.md').read_bytes()})
for p in (root/'licenses').rglob('*'):
 if p.is_file():mapping[str(p.relative_to(root))]=p.read_bytes()
build_info={'version':version,'platform':'linux/amd64','minimum_glibc':'2.34','toolchain':'go1.26.0','binary_sha256':h(binary),'html_sha256':h(html),'ui_data':'synthetic fixture; not real-account acceptance','source_included':True,'official_host_tested_this_delivery':False,'production_changed':False,'retention':'one latest ZIP; previous ZIPs removed after validated replacement'}
mapping['BUILD-INFO.json']=(json.dumps(build_info,ensure_ascii=False,indent=2)+'\n').encode()
assert not any(n.endswith(('config.yaml','test-key','.env')) or '/node_modules/' in n or '/.git/' in n for n in mapping)
mapping['CONTENTS-SHA256SUMS']=''.join(f'{h(data)}  {name}\n' for name,data in sorted(mapping.items())).encode()
filename=out/'workbuddy-latest.zip';tmp=out/'workbuddy-latest.zip.tmp';prefix='workbuddy-'+version+'/'
try:
 with zipfile.ZipFile(tmp,'w',zipfile.ZIP_DEFLATED,compresslevel=9) as z:
  for name,data in sorted(mapping.items()):z.writestr(prefix+name,data)
 with zipfile.ZipFile(tmp) as z:
  assert z.testzip() is None
  assert len(z.namelist())==len(mapping)
  for line in z.read(prefix+'CONTENTS-SHA256SUMS').decode().splitlines():
   digest,name=line.split('  ',1);assert h(z.read(prefix+name))==digest,name
  assert z.read(prefix+'binary/linux-amd64/workbuddy.so')==binary
  assert z.read(prefix+'index.html')==html
 os.replace(tmp,filename)
finally:
 if tmp.exists():tmp.unlink()
# Only package-owned archives in this dedicated directory, never cloud/user backups.
for old in out.glob('workbuddy-*.zip'):
 if old!=filename:old.unlink()
result={'file':filename.name,'sha256':h(filename.read_bytes()),'bytes':filename.stat().st_size,'entries':len(mapping)}
(out/'SHA256SUMS').write_text(f"{result['sha256']}  {filename.name}\n")
(out/'PACKAGE-MANIFEST.json').write_text(json.dumps({'version':version,'archives':[result],**build_info},ensure_ascii=False,indent=2)+'\n')
(out/'PACKAGE-AUDIT.md').write_text(f'# 最新交付审计\n\n版本：{version}\n\n- ZIP CRC、全部包内 SHA256：PASS。\n- 插件与实际 ABI 探针二进制一致：PASS。\n- HTML 与单独交付文件逐字节一致：PASS；{len(meta["screenshots"]["screenshots"])} 张 UI 图内嵌，无 CDN。\n- 源码 allowlist 打包，无 node_modules、宿主配置或管理密钥。\n- 旧 workbuddy ZIP 在验证成功后删除；当前仅一个最新 ZIP。\n- 真实官方宿主及真实账号本次未验收，生产未动。\n\nZIP SHA256：`{result["sha256"]}`\n')
print(json.dumps(result,indent=2))
