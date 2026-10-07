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
  if method=='host.auth.list':return respond(out,{'files':[{'name':'team-alpha.json','type':'workbuddy','auth_index':'fixture-index','id':'team-alpha.json'}] if mode.startswith('models') or mode in ('billing','activation') else []})
  if method=='host.auth.get':return respond(out,{'auth_index':'fixture-index','name':'team-alpha.json','path':'team-alpha.json','json':{'type':'workbuddy',**storage}})
  if method=='host.http.operation_open':
   assert request.get('host_callback_id','')==scope
   with lock:seq+=1;op='probe-op-'+str(seq);ops[op]=request.get('host_callback_id','')
   return respond(out,{'operation_id':op})
  if method=='host.http.cancel':
   assert request['operation_id'] in ops
   assert request.get('host_callback_id','')==ops[request['operation_id']]
   canceled.set();return respond(out,{})
  if method in ('host.http.do_stream','host.http.do'):
   assert request.get('operation_id') in ops
   assert request.get('host_callback_id','')==scope
   assert request.get('request',{}).get('url','').startswith('https://')
   if mode=='cb-execute':assert request['request']['url'].startswith('https://www.workbuddy.ai/')
   if mode=='blocked':
    started.set();assert canceled.wait(5),'cancellation did not reach pending headers'
    return respond(out,error='operation canceled')
   if method=='host.http.do' and mode=='activation':
    assert request['request']['method']=='GET' and '/overseas/user/register?' in request['request']['url']
    return respond(out,{'StatusCode':200,'Body':encode({'code':500})})
   if method=='host.http.do' and mode=='billing':
    url=request['request']['url']
    if storage.get('auth',{}).get('region')=='intl':assert 'checkin' not in url and url.startswith('https://www.workbuddy.ai/')
    if url.endswith('/get-user-resource'):data={'Response':{'Data':{'TotalCount':1,'Accounts':[{'PackageName':'Pro','CapacityRemain':75,'CapacityUsed':25,'CapacitySize':100}]}}}
    elif url.endswith('/get-payment-type'):data={'paymentType':'Pro'}
    elif url.endswith('/checkin-activity-status'):data={'checked_in':True,'today_checked_in':True}
    else:raise AssertionError('Unexpected billing URL '+url)
    return respond(out,{'StatusCode':200,'Headers':{},'Body':encode({'code':0,'data':data})})
   if method=='host.http.do' and mode.startswith('models'):
    if mode=='models-manager':
     assert request['request']['url'].startswith('https://www.workbuddy.ai/')
     if '/v2/enterprises/' in request['request']['url']:return respond(out,{'StatusCode':200,'Body':encode({'code':404})})
     return respond(out,{'StatusCode':200,'Body':encode({'code':'0','data':{'models':[{'id':'manager-global-model'}]}})})
    if mode=='models-failed':return respond(out,{'StatusCode':403,'Headers':{},'Body':encode(b'discovery denied')})
    return respond(out,{'StatusCode':200,'Headers':{},'Body':encode({'code':0,'data':{'models':[{'id':'glm-5.2','name':'GLM','contextWindow':128000,'disabled':False}]}})})
   if method=='host.http.do':return respond(out,{'StatusCode':200,'Headers':{},'Body':encode({'code':0,'data':{'accessToken':'new-fixture-only'}})})
   stream='probe-stream-'+str(seq);streams[stream]=False
   return respond(out,{'status_code':200,'headers':{},'stream_id':stream})
  if method=='host.http.stream_read':
   stream=request['stream_id'];assert not streams[stream];streams[stream]=True
   chunk='data: '+json.dumps({'id':'fixture','choices':[{'index':0,'delta':{'content':'bridge-ok'},'finish_reason':'stop' if mode in ('normal','cb-execute') else None}]})+'\n\n'
   if mode in ('normal','cb-execute'):chunk+='data: [DONE]\n\n'
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
rc,menus=invoke('management.register',{});assert rc==0 and menus['ok'],menus
assert any(r.get('path')=='/plugins/workbuddy/panel' and r.get('method')=='GET' and r.get('menu')=='WorkBuddy' for r in menus['result']['routes'])
assert any(r.get('path')=='/panel' and r.get('menu')=='WorkBuddy' for r in menus['result']['resources'])
legacy=next(r for r in menus['result']['routes'] if r.get('path')=='/plugins/workbuddy/panel')
resource=next(r for r in menus['result']['resources'] if r.get('path')=='/panel')
assert legacy.get('description') and legacy['description']==resource.get('description'),'legacy-first menu merge loses description'
for base in ('/v0/management/plugins/workbuddy','/v0/resource/plugins/workbuddy'):
 for asset in ('/panel','/panel.js','/panel.css','/panel-i18n.js'):
  rc,result=invoke('management.handle',{'Method':'GET','Path':base+asset})
  assert rc==0 and result['ok'] and result['result']['StatusCode']==200 and result['result']['Body'],result
checks.append('compiled dual-track menu metadata and all legacy/modern embedded assets are registered and served')
storage={'auth':{'accessToken':'fixture-only','expiresAt':2000000000},'account':{'uid':'abi-fixture'}}
request={'StorageJSON':encode(storage),'Model':'fixture-model','Payload':encode({'messages':[{'role':'user','content':'probe'}]}),'host_callback_id':scope}
for method in ('executor.execute','executor.execute_stream'):
 rc,result=invoke(method,request);assert rc==0 and result['ok'],result
 if method=='executor.execute':assert b'bridge-ok' in base64.b64decode(result['result']['Payload'])
 else:assert result['result']['chunks']
checks.append('sync and streaming collection execute through owned host operations with callback scope')
mode='cb-execute'
cb_request={**request,'StorageJSON':encode({'auth':{'accessToken':'cb-fixture','domain':'codebuddy.ai','region':'intl'},'account':{'uid':'cb-fixture'}})}
rc,result=invoke('executor.execute',cb_request);assert rc==0 and result['ok'],result
assert b'bridge-ok' in base64.b64decode(result['result']['Payload'])
checks.append('compiled CB-origin international inference uses WB gateway via CPA-owned HTTP operations')

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
mode='models';scope='probe-directory'
req={'Method':'POST','Path':'/v0/management/plugins/workbuddy/models/directory/refresh','Body':encode({'auth_index':'fixture-index'}),'host_callback_id':scope}
rc,result=invoke('management.handle',req);assert rc==0 and result['ok'],result
body=json.loads(base64.b64decode(result['result']['Body']));assert body['status']=='ok' and len(body['sources'])==2 and body['models'][0]['routing_status']=='directoryOnly',body
checks.append('compiled account directory fetches both sources with callback scope and never claims routing registration')
scope='probe-hub'
req={'Method':'POST','Path':'/v0/management/plugins/workbuddy/models/hub/refresh','Body':encode({'sources':{}}),'host_callback_id':scope}
rc,result=invoke('management.handle',req);assert rc==0 and result['ok'],result
body=json.loads(base64.b64decode(result['result']['Body']));assert body['status']=='ok' and len(body['sources'])==2 and body['models'],body
assert all(v['origin']=='dynamic' for m in body['models'] for v in m['variants']),body
assert len({m['id'] for m in body['models']})==len(body['models'])
checks.append('compiled model hub selects one account per channel, propagates callback scope and returns unique IDs with provenance')
previous_storage=storage
storage={'auth':{'accessToken':'manager-opaque-fixture','realm':'global'},'account':{'uid':'manager-fixture'}}
mode='models-manager';scope='manager-realm-abi';req['host_callback_id']=scope
rc,result=invoke('management.handle',req);assert rc==0 and result['ok'],result
body=json.loads(base64.b64decode(result['result']['Body']))
assert body['sources'][1]['status']=='ok' and body['models'][0]['id']=='manager-global-model',body
assert len(body['sources'][1]['endpoints'][0]['attempts'])==2,body
assert 'manager-opaque-fixture' not in json.dumps(body)
checks.append('compiled Manager auth.realm flows to global-only host requests; business endpoint fallback and string-code response are accepted without token exposure')
storage={'auth':{'accessToken':'cb-opaque-fixture','domain':'www.codebuddy.ai','region':'intl'},'account':{'uid':'cb-fixture'}}
before_saves=calls.count('host.auth.save')
rc,result=invoke('management.handle',req);assert rc==0 and result['ok'],result
body=json.loads(base64.b64decode(result['result']['Body']))
assert len(body['sources'])==2 and body['sources'][1]['channel']=='intl' and body['sources'][1]['status']=='ok',body
assert body['models'][0]['id']=='manager-global-model' and calls.count('host.auth.save')==before_saves
assert 'cb-opaque-fixture' not in json.dumps(body)
checks.append('compiled CB international credential shares the foreign channel and WB gateway; no credential writes or token exposure')
storage=previous_storage


mode='billing';scope=''
# Background billing operations are owned by the plugin lifecycle, without an executor callback.
req={'Method':'GET','Path':'/v0/management/plugins/workbuddy/accounts'}
rc,result=invoke('management.handle',req);assert rc==0 and result['ok'],result
body=json.loads(base64.b64decode(result['result']['Body']))
account=body['accounts'][0];assert not account.get('credits') and body['credits_loading']=='progressive',account
assert 'host.auth.save' not in calls,'Read-only dashboard wrote credentials'
checks.append('compiled cold dashboard returns identities without billing; progressive contract declared')
for query in ({'auth_index':['fixture-index']},{}):
 before_saves=calls.count('host.auth.save')
 rc,result=invoke('management.handle',{'Method':'GET','Path':'/v0/management/plugins/workbuddy/credits','Query':query})
 body=json.loads(base64.b64decode(result['result']['Body']))
 assert len(body['accounts'])==1 and body['accounts'][0]['credits']['total_remain']==75,body
 assert body['accounts'][0]['credits']['fetched_at'] and body['accounts'][0]['service']=='cn',body
 assert calls.count('host.auth.save')==before_saves
checks.append('scoped and bulk credit reads return timestamped channel snapshots without CPA credential writes')
storage={'auth':{'accessToken':'foreign-fixture','region':'intl'},'account':{'uid':'foreign-fixture'}}
mode='billing';before_saves=calls.count('host.auth.save')
rc,result=invoke('management.handle',{'Method':'GET','Path':'/v0/management/plugins/workbuddy/credits'})
body=json.loads(base64.b64decode(result['result']['Body']));a=body['accounts'][0]
assert a['capabilities']['checkin']['supported'] is False and a['capabilities']['trial']['eligibility']=='unknown',body
checks.append('compiled foreign credit read has no check-in HTTP and returns unknown trial eligibility, not entitlement')
before_http=calls.count('host.http.do')
rc,result=invoke('management.handle',{'Method':'POST','Path':'/v0/management/plugins/workbuddy/tasks/travel','Body':encode({'auth_index':'fixture-index'})})
body=json.loads(base64.b64decode(result['result']['Body']))
assert result['result']['StatusCode']==422 and body['code']=='unsupported_region' and calls.count('host.http.do')==before_http,result
checks.append('compiled foreign travel returns structured HTTP422 with zero upstream calls')
mode='activation'
rc,result=invoke('management.handle',{'Method':'GET','Path':'/v0/management/plugins/workbuddy/activation/status','Query':{'auth_index':['fixture-index']}})
body=json.loads(base64.b64decode(result['result']['Body']))
assert body['registration']=='required' and body['trial_eligibility']=='unknown' and calls.count('host.auth.save')==before_saves,body
checks.append('compiled explicit activation diagnosis uses GET only; does not register, grant trial or modify credentials')
storage=previous_storage
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
