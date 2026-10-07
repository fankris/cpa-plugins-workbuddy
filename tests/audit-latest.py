#!/usr/bin/env python3
"""Independent archive audit; extraction only to disposable cache for rebuild."""
import hashlib,json,zipfile
from pathlib import Path
root=Path(__file__).resolve().parents[1];out=root.parent/'deliverables';dest=Path.home()/'.cache'/('workbuddy-audit'+(root/'VERSION').read_text().strip().rsplit('.',1)[-1]);dest.mkdir(parents=True,exist_ok=True)
h=lambda b:hashlib.sha256(b).hexdigest()
p=out/'workbuddy-latest.zip';digest=h(p.read_bytes());manifest=json.loads((out/'PACKAGE-MANIFEST.json').read_text());assert manifest['archives'][0]['sha256']==digest
with zipfile.ZipFile(p) as z:
 assert z.testzip() is None
 names=z.namelist();prefix=names[0].split('/')[0]+'/'
 assert all(not Path(n).is_absolute() and '..'not in Path(n).parts for n in names)
 sums=z.read(prefix+'CONTENTS-SHA256SUMS').decode().splitlines();assert len(sums)==len(names)-1
 for line in sums:
  checksum,name=line.split('  ',1);assert h(z.read(prefix+name))==checksum,name
 for name in names:
  local=name.removeprefix(prefix)
  assert not any(x in Path(local).parts for x in ('node_modules','.git','.cache','__pycache__'))
  if local.startswith('source/'):
   rel=local.removeprefix('source/');assert (root/rel).read_bytes()==z.read(name),rel
 assert z.read(prefix+'index.html')==(out/'WorkBuddy-交付说明.html').read_bytes()
 assert z.read(prefix+'binary/linux-amd64/workbuddy.so')==(root/'artifacts/workbuddy.so').read_bytes()
 z.extractall(dest)
report={'result':'PASS','version':(root/'VERSION').read_text().strip(),'archive_sha256':digest,'entries':len(names),'crc':'PASS','every_file_sha256':'PASS','source_identity':'PASS','path_safety':'PASS','html_identity':'PASS','binary_identity':'PASS','excluded_dependency_and_credential_paths':'PASS','extracted_source':str(dest/prefix/'source')}
(out/'INDEPENDENT-AUDIT.json').write_text(json.dumps(report,indent=2)+'\n');print(report['extracted_source'])
