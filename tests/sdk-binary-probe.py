#!/usr/bin/env python3
"""Exercise the exact release .so through C ABI with a NO-NETWORK mock host.
This is binary/RPC integration, not official-host or real-account E2E.
"""
import base64,ctypes as C,json,threading,hashlib
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
class Buffer(C.Structure):_fields_=[('ptr',C.c_void_p),('len',C.c_size_t)]
Call=C.CFUNCTYPE(C.c_int,C.c_void_p,C.c_char_p,C.c_void_p,C.c_size_t,C.POINTER(Buffer))
Free=C.CFUNCTYPE(None,C.c_void_p,C.c_size_t)
PluginCall=C.CFUNCTYPE(C.c_int,C.c_char_p,C.c_void_p,C.c_size_t,C.POINTER(Buffer))
Shutdown=C.CFUNCTYPE(None)
class Host(C.Structure):_fields_=[('abi_version',C.c_uint32),('host_ctx',C.c_void_p),('call',Call),('free_buffer',Free)]
class Plugin(C.Structure):_fields_=[('abi_version',C.c_uint32),('call',PluginCall),('free_buffer',Free),('shutdown',Shutdown)]
libc=C.CDLL(None);libc.malloc.argtypes=[C.c_size_t];libc.malloc.restype=C.c_void_p;libc.free.argtypes=[C.c_void_p]
lock=threading.Lock();allocated=set();errors=[];checks=[];calls=[];ops={};streams={}
mode='normal';started=threading.Event();canceled=threading.Event();scope='probe-exec';seq=0
encode=lambda v:base64.b64encode(v if isinstance(v,bytes) else json.dumps(v).encode()).decode()
def respond(out,result=None,error=None):
 data=json.dumps({'ok':error is None,**({'result':result} if error is None else {'error':{'code':'fixture_error','message':error}})}).encode()
 ptr=libc.malloc(len(data));assert ptr;C.memmove(ptr,data,len(data))
 with lock:allocated.add(ptr)
 out[0].ptr=ptr;out[0].len=len(data)
 return 0
@Free
def free(ptr,size):
 with lock:
  if ptr not in allocated:errors.append('unknown or double freed host buffer')
  else:allocated.remove(ptr)
 libc.free(ptr)
@Call
def callback(ctx,method,raw,size,out):
 global seq
 try:
  method=method.decode();request=json.loads(C.string_at(raw,size) or b'{}')
  with lock:calls.append(method)
  if method=='host.log':return respond(out,{})
  if method=='host.auth.list':return respond(out,{'files':[{'name':'team-alpha.json','type':'workbuddy','auth_index':'fixture-index','id':'team-alpha.json'}] if mode.startswith('models') else []})
  if method=='host.auth.get':return respond(out,{'auth_index':'fixture-index','name':'team-alpha.json','path':'team-alpha.json','json':{'type':'workbuddy',**storage}})
  if method=='host.http.operation_open':
   assert request.get('host_callback_id')==scope
   with lock:seq+=1;op='probe-op-'+str(seq);ops[op]=request.get('host_callback_id')
   return respond(out,{'operation_id':op})
  if method=='host.http.cancel':
   assert request['operation_id'] in ops
   assert request.get('host_callback_id')==ops[request['operation_id']]
   canceled.set();return respond(out,{})
  if method in ('host.http.do_stream','host.http.do'):
   assert request.get('operation_id') in ops
   assert request.get('host_callback_id')==scope
   assert request.get('request',{}).get('url','').startswith('https://')
   if mode=='blocked':
    started.set();assert canceled.wait(5),'cancellation did not reach pending headers'
    return respond(out,error='operation canceled')
   if method=='host.http.do' and mode.startswith('models'):
    if mode=='models-failed':return respond(out,{'StatusCode':403,'Headers':{},'Body':encode(b'discovery denied')})
    return respond(out,{'StatusCode':200,'Headers':{},'Body':encode({'code':0,'data':{'models':[{'id':'glm-5.2','name':'GLM','contextWindow':128000,'disabled':False}]}})})
   if method=='host.http.do':return respond(out,{'StatusCode':200,'Headers':{},'Body':encode({'code':0,'data':{'accessToken':'new-fixture-only'}})})
   stream='probe-stream-'+str(seq);streams[stream]=False
   return respond(out,{'status_code':200,'headers':{},'stream_id':stream})
  if method=='host.http.stream_read':
   stream=request['stream_id'];assert not streams[stream];streams[stream]=True
   chunk='data: '+json.dumps({'id':'fixture','choices':[{'index':0,'delta':{'content':'bridge-ok'},'finish_reason':'stop' if mode=='normal' else None}]})+'\n\n'
   if mode=='normal':chunk+='data: [DONE]\n\n'
   return respond(out,{'payload':encode(chunk.encode()),'done':True,**({'error':'upstream disconnected'} if mode=='terminal-error' else {})})
  if method=='host.http.stream_close':return respond(out,{})
  return respond(out,error='unexpected mock callback '+method)
 except BaseException as e:
  errors.append(type(e).__name__+': '+str(e));return respond(out,error='mock assertion failed')
lib=C.CDLL(str(ROOT/'artifacts/workbuddy.so'));host=Host(1,None,callback,free);plugin=Plugin()
lib.cliproxy_plugin_init.argtypes=[C.POINTER(Host),C.POINTER(Plugin)];lib.cliproxy_plugin_init.restype=C.c_int
assert lib.cliproxy_plugin_init(C.byref(host),C.byref(plugin))==0 and plugin.abi_version==1
def invoke(method,request):
 raw=json.dumps(request).encode();buf=C.create_string_buffer(raw);out=Buffer()
 rc=plugin.call(method.encode(),C.cast(buf,C.c_void_p),len(raw),C.byref(out))
 try:result=json.loads(C.string_at(out.ptr,out.len))
 finally:plugin.free_buffer(out.ptr,out.len)
 return rc,result
config='checkin_auto: false\nlifecycle_auto: false\ntoken_keepalive: false\ntravel_auto: false\nscheduler_mode: host\n'
rc,reg=invoke('plugin.register',{'config_yaml':encode(config.encode())});assert rc==0 and reg['ok'] and reg['result']['schema_version']==6
checks.append('actual release C ABI initialization and schema 6 registration')
storage={'auth':{'accessToken':'fixture-only','expiresAt':2000000000},'account':{'uid':'abi-fixture'}}
request={'StorageJSON':encode(storage),'Model':'fixture-model','Payload':encode({'messages':[{'role':'user','content':'probe'}]}),'host_callback_id':scope}
for method in ('executor.execute','executor.execute_stream'):
 rc,result=invoke(method,request);assert rc==0 and result['ok'],result
 if method=='executor.execute':assert b'bridge-ok' in base64.b64decode(result['result']['Payload'])
 else:assert result['result']['chunks']
checks.append('sync and streaming collection execute through owned host operations with callback scope')
mode='terminal-error';rc,result=invoke('executor.execute',request);assert not result['ok'] and result['error']['http_status']==502
checks.append('terminal upstream error remains an error across the compiled C boundary')
mode='normal';scope='probe-refresh';rc,result=invoke('auth.refresh',{'StorageJSON':encode(storage),'host_callback_id':scope});assert rc==0 and result['ok'],result
assert not result['result']['Auth'].get('FileName') and not result['result']['Auth'].get('ID')
assert 'host.auth.save' not in calls
checks.append('native refresh preserves callback scope and leaves persistence/filename ownership to CPA')
mode='models';scope='probe-model-auth';storage['auth']['region']='cn'
before=calls.count('host.http.do')
rc,result=invoke('model.for_auth',{'StorageJSON':encode(storage),'host_callback_id':scope});assert rc==0 and result['ok'],result
assert calls.count('host.http.do')==before+1
checks.append('model.for_auth discovers with its own callback scope through compiled ABI')
scope='probe-model-management'
req={'Method':'POST','Path':'/v0/management/plugins/workbuddy/models/refresh','Body':encode({'auth_index':'fixture-index'}),'host_callback_id':scope}
before=calls.count('host.http.do');rc,result=invoke('management.handle',req);assert rc==0 and result['ok'],result
body=json.loads(base64.b64decode(result['result']['Body']));assert body['status']=='ok',body
assert calls.count('host.http.do')==before+1
checks.append('custom-named native credential is visible; forced model refresh retains management callback scope')
mode='models-failed';scope='probe-model-fallback';req['host_callback_id']=scope
rc,result=invoke('management.handle',req);assert rc==0 and result['ok'],result
body=json.loads(base64.b64decode(result['result']['Body']));assert body['status']=='fallback' and body['warning'] and body['models'],body
checks.append('compiled management response reports discovery failure with usable fallback, never false success')
mode='blocked';scope='probe-blocked';request['host_callback_id']=scope;canceled.clear();thread_errors=[]
def blocked():
 try:
  _,result=invoke('executor.execute',request);assert not result['ok']
 except BaseException as e:thread_errors.append(str(e))
t=threading.Thread(target=blocked);t.start();assert started.wait(5)
rc,result=invoke('plugin.quiesce',{});assert rc==0 and result['ok'];t.join(5);assert not t.is_alive() and not thread_errors
plugin.shutdown();assert not allocated and not errors,(len(allocated),errors)
checks.append('quiesce cancels a pending header request and drains callbacks without buffer leaks')
report={'result':'PASS','binary_sha256':hashlib.sha256((ROOT/'artifacts/workbuddy.so').read_bytes()).hexdigest(),'checks':checks,'host':'C ABI mock; NO NETWORK','realCredentialsUsed':False,'officialHost':False,'callbackErrors':errors,'outstandingHostBuffers':len(allocated)}
output=ROOT/'validation'/('iteration-'+(ROOT/'VERSION').read_text().strip().rsplit('.',1)[-1]);output.mkdir(parents=True,exist_ok=True)
(output/'binary-probe.json').write_text(json.dumps(report,indent=2)+'\n');print(json.dumps(report,indent=2))
