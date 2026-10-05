// Evidence against pinned official source, not a real-account integration test.
const fs=require('node:fs'),path=require('node:path'),cp=require('node:child_process'),assert=require('node:assert/strict'),crypto=require('node:crypto');
const mod=JSON.parse(cp.execFileSync('go',['list','-m','-json','github.com/router-for-me/CLIProxyAPI/v8'],{encoding:'utf8'}));assert.equal(mod.Version,'v8.0.15');
const hashes={},read=name=>{const text=fs.readFileSync(path.join(mod.Dir,name),'utf8');hashes[name]=crypto.createHash('sha256').update(text).digest('hex');return text};
const callbacks=read('internal/pluginhost/host_callbacks.go'),schema=read('internal/pluginhost/rpc_schema.go'),operations=read('internal/pluginhost/http_operation_bridge.go'),abi=read('sdk/pluginabi/types.go');
for(const token of ['host.http.operation_open','host.http.cancel','host.http.do_stream','host.http.stream_close'])assert.ok(abi.includes('"'+token+'"'));
for(const field of ['host_callback_id','operation_id'])assert.ok(callbacks.includes('json:"'+field));
assert.match(callbacks,/callHostHTTPDo[\s\S]*?acquireHostHTTPOperation/);
assert.match(callbacks,/resp\.Error = chunk\.Err\.Error\(\)/);
assert.match(schema,/type rpcExecutorRequest struct[\s\S]*?HostCallbackID/);
assert.match(schema,/type rpcAuthRefreshRequest struct[\s\S]*?HostCallbackID/);
assert.ok(operations.includes('host callback ID does not belong to the calling plugin instance'));
assert.ok(operations.includes('host callback ID is not open'));
console.log(JSON.stringify({result:'PASS',version:mod.Version,type:'pinned official source contract',checks:['host-owned HTTP operation open/cancel names','operation and callback IDs wire fields','request ownership and callback scope validation','terminal payload/error stream shape','executor and auth refresh callback scope'],sha256:hashes},null,2));
