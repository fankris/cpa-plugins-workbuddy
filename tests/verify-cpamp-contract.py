"""Pinned CPAMP source, not an acceptance test of the user's deployment."""
import hashlib,json,urllib.request
from pathlib import Path
root=Path(__file__).resolve().parents[1]
meta=json.loads((root/'tests/fixtures/cpamp-routing-reference.json').read_text())
texts={}
for path,digest in meta['files'].items():
 data=urllib.request.urlopen('https://raw.githubusercontent.com/seakee/CPA-Manager-Plus/'+meta['commit']+'/'+path,timeout=30).read()
 assert hashlib.sha256(data).hexdigest()==digest
 texts[path]=data.decode()
router=texts['apps/manager-server/internal/http/router/router.go']
plugins=texts['apps/web/src/services/api/plugins.ts']
assert 'strings.HasPrefix(r.URL.Path, "/v0/management/")' in router
assert '"/v8/management/' not in router
assert 'apiClient.get(`/plugins/${encodeURIComponent(id)}/config`)' in plugins
assert 'apiClient.put(`/plugins/${encodeURIComponent(id)}/config`, config)' in plugins
print(json.dumps({'result':'PASS',**meta,'scope':'source contract only, no actual CPAMP instance'},indent=2))
