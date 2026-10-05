#!/usr/bin/env python3
"""Package explicit source/runtime allowlists, never the host configuration."""
from pathlib import Path
import hashlib,json,zipfile
root=Path(__file__).resolve().parents[1]
out=root.parent/'deliverables';out.mkdir(exist_ok=True)
version=(root/'VERSION').read_text().strip()
h=lambda b:hashlib.sha256(b).hexdigest()
def tree(folder):
 return [p for p in (root/folder).rglob('*') if p.is_file() and not any(x in p.parts for x in ['node_modules','__pycache__','legacy-v1'])]
rootnames={'Makefile','VERSION','LICENSE','README.md','README_CN.md','THIRD_PARTY_NOTICES.md','CHANGELOG.md','go.mod','go.sum','.gitignore','panel.html','panel.js','panel.css','panel-i18n.js'}
source=[p for p in root.iterdir() if p.is_file() and (p.suffix=='.go' or p.name in rootnames)]
for folder in ['frontend','tests','docs','licenses']:source+=tree(folder)
evidence=[p for p in (root/'validation').iterdir() if p.is_file() and (p.suffix=='.png' or p.name in {'final-go-tests.jsonl','frontend-contracts.tap','go-vet.log','host-contract.json','rebuild-browser.json','real-host-integration.json','release-guard.log'})]
source+=evidence
binary=[root/n for n in ['README.md','README_CN.md','THIRD_PARTY_NOTICES.md','LICENSE','VERSION']]+tree('docs')+tree('licenses')+[root/'artifacts/workbuddy.so',root/'artifacts/workbuddy.h']
def pack(kind,files):
 prefix=f'workbuddy-{version}-{kind}'
 filename=out/(prefix+'.zip');mapping={}
 for p in files:
  name=p.name if p.parent.name=='artifacts' else str(p.relative_to(root))
  mapping[name]=p.read_bytes()
 assert all('node_modules' not in n and '.cache' not in n and 'real-host.log' not in n and 'config.yaml' not in n for n in mapping)
 manifest=''.join(f'{h(data)}  {name}\n' for name,data in sorted(mapping.items()))
 mapping['CONTENTS-SHA256SUMS']=manifest.encode()
 with zipfile.ZipFile(filename,'w',zipfile.ZIP_DEFLATED,compresslevel=9) as z:
  for name,data in sorted(mapping.items()):z.writestr(prefix+'/'+name,data)
 with zipfile.ZipFile(filename) as z:
  assert z.testzip() is None
  assert len(z.namelist())==len(mapping)
 return {'file':filename.name,'sha256':h(filename.read_bytes()),'bytes':filename.stat().st_size,'entries':len(mapping)}
results=[pack('source',source),pack('linux-amd64',binary)]
(out/'SHA256SUMS').write_text(''.join(f"{r['sha256']}  {r['file']}\n" for r in results))
(out/'PACKAGE-MANIFEST.json').write_text(json.dumps({'version':version,'archives':results,'binary_sha256':h((root/'artifacts/workbuddy.so').read_bytes()),'scope':'Independent isolated-tested rebuild; not production deployment'},indent=2)+'\n')
print(json.dumps(results,indent=2))
