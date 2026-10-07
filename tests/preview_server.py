"""Local UI fixture only. No upstream requests, real auths, or persistent account store."""
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
from urllib.parse import urlparse, parse_qs
from pathlib import Path
import json, datetime, copy, threading
ROOT=Path(__file__).resolve().parents[1]
BASE='/v0/management/plugins/workbuddy'
RESOURCE='/v0/resource/plugins/workbuddy'
lock=threading.Lock()
now=lambda:datetime.datetime.now(datetime.timezone.utc).isoformat()
accounts=[]
for i,(name,realm,plan,balance) in enumerate([
 ('Alex · Workspace','cn','Pro',12840),('Design Studio','cn','Pro',8250),('Personal · International','intl','Premium',6420),
 ('Research Team','cn','Team',3280),('Development','intl','Free',0),('Secondary Workspace','cn','Free',None),
 ('Product Lab','cn','Pro',7800),('Archive','intl','Free',1260),('Sandbox','cn','Free',2500),('Mobile Workspace','intl','Premium',4620),
]):
 credits=None if balance is None else {'total_remain':balance,'total_used':max(0,15000-balance),'total_size':15000,'fetched_at':now(),'packages':[{'name':plan+' · Demo package','remain':balance,'used':15000-balance,'size':15000,'cycle_end':'2026-11-01T00:00:00Z'}]}
 accounts.append({'auth_index':f'demo-{i+1:03}','auth_id':f'demo-{i+1:03}.json','name':f'demo-{i+1:03}.json','nickname':name,'uid':f'fixture-{10001+i}','region':realm,'service':('cn' if realm=='cn' else 'global' if i in (2,9) else 'intl'),'plan':plan,'disabled':i==7,'exhausted':balance==0,'selected':i==0,'credits':credits,'runtime':None if i==5 else {'status':'active','unavailable':i in (3,4,7),'cooldown_seconds':180 if i==3 else 0,'priority':0,'success':120+i*41,'failed':2 if i==3 else 0},'daily_free':[]})
models=[{'id':v,'name':v,'context_length':n,'max_completion_tokens':32768,'disabled':i>5} for i,(v,n) in enumerate([('hunyuan-pro',128000),('deepseek-v4.1-flash',128000),('glm-5.2',200000),('kimi-k2',128000),('qwen3-coder',256000),('hunyuan-turbos',32000),('demo-model-unverified',0),('demo-model-disabled',0)])]
config={'enabled':True,'scheduler_mode':'host','login_region':'cn','login_platform':'CLI','future_opaque':{'preserve':True},'models_enabled':[m['id'] for m in models if not m['disabled']]}
tasks=[{'task_code':'demo-one','title':'完成首次工作空间配置','description':'演示任务 · 显示上游返回的进度与状态，不生成虚假活动。','current':1,'target':1,'credit':50,'claimable':True,'claimed':False,'accept_status':'accepted'}, {'task_code':'demo-two','title':'体验项目协作','description':'演示任务 · 需要在官方客户端完成真实操作。','current':2,'target':5,'credit':100,'claimable':False,'claimed':False,'accept_status':'accepted'}, {'task_code':'demo-three','title':'了解工作空间功能','description':'演示任务 · 接受任务不会被标记为已完成。','current':0,'target':1,'credit':30,'claimable':False,'claimed':False,'accept_status':'available'}]
requests=[]
run=None
initial=copy.deepcopy((accounts,models,config,tasks))
for account in accounts:
 if account.get('credits'):
  cr=account['credits'];soon=account['auth_index'] in ('demo-001','demo-002');end=(datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(days=2 if soon else 20)).isoformat()
  for package in cr['packages']:package['cycle_end']=end
  cr['expiry']={'known':True,'within_7_days':cr['total_remain'] if soon else 0,'next_at':end,'next_amount':cr['total_remain'],'unknown_remain':0}
for i,model in enumerate(models):
 model.update({'efforts':['low','high'] if i==0 else ['high'],'default_effort':'high','credits':'x0.00' if i==0 else 'x0.03','vendor':'Fixture family','supports_images':i==0,'only_reasoning':True,'metadata_source':'fixture'})
initial=copy.deepcopy((accounts,models,config,tasks))
def directory_fixture(idx):
 rows=copy.deepcopy(models)
 for i,m in enumerate(rows):
  m.update({'family':('DeepSeek' if m['id'].startswith('deepseek') else 'Kimi' if m['id'].startswith('kimi') else '智谱 GLM' if m['id'].startswith('glm') else '腾讯混元' if m['id'].startswith('hunyuan') else '其他'), 'family_derived':True,'description':'本地模拟目录数据，用于验证字段展示，不是真实腾讯响应。','supports_reasoning':True,'supports_tool_call':i%2==0,'reasoning_summary':'auto','sources':['v3_config','enterprise_models'],'image_input_sources':{'v3_config':i==0,'enterprise_models':i==0},'routing_status':'directoryOnly'})
 rows += [
 {'id':'default-model','name':'Auto','context_length':176000,'max_completion_tokens':24000,'is_default':True,'sources':['enterprise_models'],'family':'其他','family_derived':True},
 {'id':'fast-model','name':'Fast','context_length':200000,'max_completion_tokens':32000,'efforts':['medium'],'credits':'x0.34 credits','only_reasoning':True,'sources':['v3_config'],'family':'其他','family_derived':True},
 {'id':'fixture-v3-priority','name':'V3 优先 · 模拟','context_length':1000000,'max_completion_tokens':128000,'efforts':['low','medium','high','xhigh','max'],'default_effort':'high','credits':'x0.07 credits','supports_reasoning':True,'supports_tool_call':True,'only_reasoning':True,'description':'两路目录合并时以 v3 字段为准的模拟条目；不是实际模型。','reasoning_summary':'auto','sources':['v3_config','enterprise_models'],'family':'其他','family_derived':True,'image_input_conflict':True,'image_input_sources':{'v3_config':False,'enterprise_models':True}},
 {'id':'fixture-image','name':'图像条目 · 模拟','tags':['text-to-image'],'sources':['v3_config'],'family':'其他','family_derived':True},
 {'id':'fixture-video','name':'视频条目 · 模拟','tags':['text-to-video'],'sources':['v3_config'],'family':'其他','family_derived':True},
 {'id':'fixture-disabled','name':'上游禁用 · 模拟','disabled':True,'disabled_reason':'模拟目录不可用条目','sources':['enterprise_models'],'family':'其他','family_derived':True}]
 return {'auth_index':idx,'name':idx,'status':'ok','service':'global' if idx=='demo-003' else 'cn','models':rows,'count':len(rows),'sources':[{'source':'enterprise_models','path':'/console/enterprises/personal/models','status':'ok','count':11},{'source':'v3_config','path':'/v3/config','status':'ok','count':12}],'cached':False,'fetched_at':now()}
def hub_fixture(selection=None):
 selection=selection or {};sources=[];variants=[]
 for ch in ['cn','global','intl']:
  choices=[a for a in accounts if (a['region']=='cn' if ch=='cn' else a['auth_index'] in (['demo-003','demo-010'] if ch=='global' else ['demo-005','demo-008']))]
  options=[{'auth_index':a['auth_index'],'name':a['nickname'],'selected':a.get('selected',False),'available':not a['disabled']} for a in choices]
  wanted=selection.get(ch,'');chosen=next((a for a in choices if a['auth_index']==wanted and not a['disabled']),None) if wanted else next((a for a in choices if a.get('selected') and not a['disabled']),None) or next((a for a in choices if not a['disabled']),None)
  status='ok' if chosen else 'invalid_account' if wanted else 'no_account';rows=[]
  if chosen and ch=='intl':status='unsupported'
  elif chosen:
   rows=directory_fixture(chosen['auth_index'])['models']
   if ch=='global':
    rows=[m for m in rows if m['id']!='hunyuan-pro'];rows.append({'id':'fixture-international','name':'国际独有 · 模拟','context_length':1000000,'max_completion_tokens':128000,'efforts':['medium'],'credits':'x0.08','sources':['v3_config']})
    for m in rows:
     if m['id']=='deepseek-v4.1-flash':m['context_length']=1000000;m['credits']='x0.04 credits'
   if chosen['auth_index']=='demo-002':rows=rows[:3]+[{'id':'fixture-second-account','name':'第二账号目录 · 模拟'}]
   for m in rows:variants.append({'origin':'dynamic','channel':ch,'auth_index':chosen['auth_index'],'model':m})
  sources.append({'channel':ch,'accounts':options,'auth_index':chosen['auth_index'] if chosen else '', 'name':chosen['nickname'] if chosen else '', 'basis':'manual' if wanted and chosen else 'invalid_account' if wanted else 'selected' if chosen and chosen.get('selected') else 'automatic' if chosen else 'no_account','status':status,'count':len(rows),'cached':False,'fetched_at':now() if rows else '', 'endpoints':[{'source':'enterprise_models','path':'/console/enterprises/personal/models','status':'ok','count':len(rows)},{'source':'v3_config','path':'/v3/config','status':'ok','count':len(rows)}] if rows else []})
 for ch in ['cn','global','intl']:
  variants.append({'origin':'custom','channel':ch,'config_key':'models','model':{'id':'fixture-custom','name':'自定义模型 · 模拟','context_length':262144,'max_completion_tokens':32000,'metadata_source':'custom'}})
  if ch!='cn':variants.append({'origin':'custom','channel':ch,'config_key':'models','model':{'id':'deepseek-v4.1-flash','name':'自定义配置名 · 模拟','context_length':500000,'max_completion_tokens':64000,'metadata_source':'custom'}})
 variants.append({'origin':'custom','channel':'cn','config_key':'models_cn','model':{'id':'fixture-pin-cn','name':'固定列表条目 · 模拟','metadata_source':'pin'}})
 rows={}
 for v in variants:
  m=v['model'];mid=m['id'];r=rows.setdefault(mid,{'id':mid,'name':m.get('name',mid),'origins':[],'dynamic_channels':[],'custom_channels':[],'disabled':mid not in config.get('models_enabled',[]) or mid in config.get('models_disabled',[]),'variants':[]})
  if v['origin'] not in r['origins']:r['origins'].append(v['origin'])
  key=v['origin']+'_channels'
  if v['channel'] not in r[key]:r[key].append(v['channel'])
  r['variants'].append(v)
 return {'status':'partial' if any(s['status'] not in ['ok','no_account','unsupported'] for s in sources) else 'ok','models':[rows[k] for k in sorted(rows)],'sources':sources,'account_errors':[],'count':len(rows)}
class Handler(BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def send(self,data,status=200,content='application/json; charset=utf-8'):
  raw=data if isinstance(data,bytes) else json.dumps(data,ensure_ascii=False).encode()
  self.send_response(status);self.send_header('Content-Type',content);self.send_header('Content-Length',str(len(raw)));self.send_header('Cache-Control','no-store');self.send_header('X-CPA-Version','8.0.15');self.send_header('X-CPA-Support-Plugin','true');self.end_headers();self.wfile.write(raw)
 def do_GET(self):
  u=urlparse(self.path);p=u.path;q=parse_qs(u.query)
  if p=='/':self.send_response(302);self.send_header('Location',RESOURCE+'/panel');self.end_headers();return
  if p=='/official/management.html':
   official=Path.home()/'.cache/cpamc-reference/management.html'
   if not official.exists():return self.send({'error':'Download the checksum-verified CPAMC reference first'},404)
   return self.send(official.read_bytes(),200,'text/html; charset=utf-8')
  if p=='/v8/management/config':return self.send({'debug':False,'plugins':{'configs':{'workbuddy':config}},'routing':{'strategy':'round-robin'}})
  if p=='/v8/management/plugins':return self.send({'plugins':[{'id':'workbuddy','configured':True,'registered':True,'enabled':True,'effective_enabled':True,'metadata':{'name':'WorkBuddy','version':(ROOT/'VERSION').read_text().strip()},'menus':[{'path':RESOURCE+'/panel','menu':'WorkBuddy'}]}]})
  if p in ('/v8/management/credentials','/v8/management/credentials/files','/v8/management/auth-files'):return self.send({'files':[]})
  if p=='/__test/state':return self.send({'requests':requests,'config':config})
  if p in ('/host-cpamp','/host-cpamc','/host-cross'):
   title=p=='/host-cpamp'
   cross=p=='/host-cross'
   src='http://localhost:8080/v0/resource/plugins/workbuddy/panel' if cross else '/v0/resource/plugins/workbuddy/panel'
   chrome='<header id="host-title">CPAMP · WorkBuddy <small>宿主标题栏布局模拟</small></header>' if title else '<div class="floating-actions" role="toolbar"><button>刷新</button><button>语言</button><button>主题</button><button>退出</button></div><div class="mobile-sidebar-actions"><button>☰</button></div>'
   css='body{margin:0;font:14px system-ui;background:#f4f5f7}iframe{display:block;width:100%;height:100dvh;border:0}#host-title{height:64px;box-sizing:border-box;display:flex;align-items:center;gap:20px;padding:0 22px;background:#fff;border-bottom:1px solid #ddd}#host-title+iframe{height:calc(100dvh - 64px)}small{color:#666}.floating-actions,.mobile-sidebar-actions{position:fixed;top:10px;z-index:10;padding:5px;display:flex;gap:4px;border:1px solid #d8dbe0;border-radius:14px;background:#fff;box-shadow:0 2px 8px #0002}.floating-actions{right:12px}.mobile-sidebar-actions{left:10px;display:none}button{min-height:36px;min-width:40px;background:#f6f7f8;border:0;border-radius:8px;padding:0 10px}@media(max-width:640px){.mobile-sidebar-actions{display:flex}.floating-actions{right:10px}button{padding:0 6px}}'
   return self.send(('<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><style>'+css+'</style></head><body>'+chrome+'<iframe title="WorkBuddy" src="'+src+'"></iframe></body></html>').encode(),200,'text/html; charset=utf-8')
  if p=='/host':return self.send(b'<!doctype html><html><body style="margin:0"><iframe title="WorkBuddy" src="/v0/resource/plugins/workbuddy/panel" style="width:100%;height:100vh;border:0"></iframe></body></html>',200,'text/html; charset=utf-8')
  files={RESOURCE+'/panel':'panel.html',RESOURCE+'/panel.js':'panel.js',RESOURCE+'/panel.css':'panel.css'}
  if p in files:
   name=files[p];content=(ROOT/name).read_bytes()
   if name=='panel.html':
    content=content.replace(b'__WB_MANAGEMENT_BASE__',BASE.encode()).replace(b'__WB_RESOURCE_BASE__',RESOURCE.encode()).replace(b'<head>',b'<head><script>window.__WB_PREVIEW__=true;sessionStorage.setItem("workbuddy-mgmt-key","local-fixture-only");</script>')
   return self.send(content,content={'html':'text/html','js':'application/javascript','css':'text/css'}[name.split('.')[-1]]+'; charset=utf-8')
  requests.append({'method':'GET','path':p})
  if p==BASE+'/settings':return self.send({k:config.get(k,v) for k,v in {'checkin_auto':True,'lifecycle_auto':True,'token_keepalive':False,'travel_auto':False,'scheduler_mode':'host'}.items()})
  if p==BASE+'/credits':
   idx=parse_qs(urlparse(self.path).query).get('auth_index',[''])[0];return self.send({'accounts':[a for a in accounts if not idx or a['auth_index']==idx],'server_time_iso':now()})
  if p==BASE+'/accounts':return self.send({'accounts':accounts,'server_time':now(),'server_time_iso':now()})
  if p in ('/v8/management/config/plugins/configs/workbuddy',BASE+'/config'):return self.send(config)
  if p==BASE+'/models/hub':return self.send(hub_fixture({k:v[0] for k,v in q.items()}))
  if p==BASE+'/models/directory':return self.send(directory_fixture(q.get('auth_index',[''])[0]))
  if p==BASE+'/models/catalog':return self.send({'models':models})
  if p==BASE+'/models':
   idx=q.get('auth_index',[''])[0];return self.send({'auth_index':idx,'models':models,'source':{'source':'local demo fixture'}})
  if p==BASE+'/tasks':return self.send({'tasks':tasks})
  if p==BASE+'/tasks/status':
   if not run:return self.send({'status':'idle','auth_index':q.get('auth_index',[''])[0]})
   if run.get('cancel_requested'):run['status']='canceled'
   elif run['status']=='running':run['status']='succeeded';run['accepted']=len(tasks)
   return self.send(run)
  self.send({'error':'not found'},404)
 def do_POST(self):self.mutate()
 def do_PUT(self):self.mutate()
 def do_PATCH(self):self.mutate()
 def mutate(self):
  global config,run,accounts,models,tasks,requests
  p=urlparse(self.path).path
  try:
   size=int(self.headers.get('Content-Length','0'))
   if size>2_000_000:return self.send({'error':'too large'},413)
   body=json.loads(self.rfile.read(size) or b'{}')
  except Exception:return self.send({'error':'invalid JSON'},400)
  if p=='/__test/reset':
   accounts,models,config,tasks=copy.deepcopy(initial);run=None;requests=[];return self.send({'demo':True,'reset':True})
  requests.append({'method':self.command,'path':p})
  with lock:
   if p in ('/v8/management/config/plugins/configs/workbuddy',BASE+'/config') and self.command=='PUT':
    config=body
    for m in models:m['disabled']=m['id'] not in config.get('models_enabled',[]) or m['id'] in config.get('models_disabled',[])
    return self.send({'status':'ok'})
   if p=='/v8/management/credentials/refresh':return self.send({'ok':True,'auth':{'access_token':'fixture-secret-must-not-be-logged'}})
   if p==BASE+'/tasks/accept_all':
    run={'run_id':'demo-'+str(len(requests)),'auth_index':body.get('auth_index'),'status':'running','kind':'accept','accepted':0,'started_at':now(),'cancel_requested':False}
    return self.send(run)
   if p==BASE+'/tasks/cancel':
    if not run or run['run_id']!=body.get('run_id') or run['auth_index']!=body.get('auth_index'):return self.send({'error':'unknown run'},404)
    run['cancel_requested']=True;return self.send(run)
   if p==BASE+'/refresh':return self.send({'accounts':accounts})
   if p==BASE+'/models/hub/refresh':return self.send(hub_fixture(body.get('sources',{})))
   if p==BASE+'/models/directory/refresh':return self.send(directory_fixture(body.get('auth_index')))
   if p==BASE+'/models/refresh':return self.send({'auth_index':body.get('auth_index'),'models':models,'source':{'source':'local demo fixture'}})
   if p in [BASE+'/tasks/accept',BASE+'/tasks/claim']:
    task=next((t for t in tasks if t['task_code']==body.get('task_code')),None)
    if not task:return self.send({'error':'unknown task'},404)
    if p.endswith('/claim'):
     if not task['claimable'] or task['claimed']:return self.send({'error':'not claimable'},409)
     task['claimed']=True;task['claimable']=False
    else:task['accept_status']='accepted'
    return self.send({'ok':True,'demo':True,'task_code':task['task_code']})
   if p=='/v8/management/credentials/status':
    a=next((a for a in accounts if a['auth_index']==body.get('auth_index')),None)
    if not a:return self.send({'error':'not found'},404)
    a['disabled']=body.get('disabled',False);return self.send({'status':'ok','demo':True})
   if p==BASE+'/select':
    for a in accounts:a['selected']=a['auth_index']==body.get('auth_index')
    return self.send({'ok':True,'demo':True})
   if p==BASE+'/import':
    payload=body.get('json',{})
    if not isinstance(payload,dict) or not (payload.get('accessToken') or payload.get('access_token')):return self.send({'success':False,'error':'fixture import requires accessToken'},400)
    idx='demo-import-'+str(len(accounts)+1)
    accounts.append({'auth_index':idx,'auth_id':idx+'.json','name':idx+'.json','nickname':str(payload.get('nickname') or 'Imported demo account'),'uid':str(payload.get('uid') or idx),'region':'cn','plan':'Demo','disabled':False,'exhausted':False,'selected':False,'runtime':{'status':'active','unavailable':False},'daily_free':[]})
    return self.send({'success':True,'auth_index':idx,'demo':True})
   if p in [BASE+'/keepalive',BASE+'/checkin',BASE+'/trial',BASE+'/tasks/travel',BASE+'/daily-quota/reset']:return self.send({'ok':True,'demo':True,'note':'local simulation; no real account changed'})
  self.send({'error':'not found'},404)
if __name__=='__main__':
 import argparse
 parser=argparse.ArgumentParser();parser.add_argument('--port',type=int,default=8080);parser.add_argument('--bind',default='0.0.0.0');args=parser.parse_args()
 server=ThreadingHTTPServer((args.bind,args.port),Handler);print(f'WorkBuddy UI fixture listening on {args.bind}:{args.port}',flush=True);server.serve_forever()
