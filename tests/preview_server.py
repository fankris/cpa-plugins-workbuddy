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
 accounts.append({'auth_index':f'demo-{i+1:03}','auth_id':f'demo-{i+1:03}.json','name':f'demo-{i+1:03}.json','nickname':name,'uid':f'fixture-{10001+i}','region':realm,'plan':plan,'disabled':i==7,'exhausted':balance==0,'selected':i==0,'credits':credits,'runtime':None if i==5 else {'status':'active','unavailable':i in (3,4,7),'cooldown_seconds':180 if i==3 else 0,'priority':0,'success':120+i*41,'failed':2 if i==3 else 0},'daily_free':[]})
models=[{'id':v,'name':v,'context_length':n,'max_completion_tokens':32768,'disabled':i>5} for i,(v,n) in enumerate([('hunyuan-pro',128000),('deepseek-v4.1-flash',128000),('glm-5.2',200000),('kimi-k2',128000),('qwen3-coder',256000),('hunyuan-turbos',32000),('demo-model-unverified',0),('demo-model-disabled',0)])]
config={'enabled':True,'scheduler_mode':'host','login_region':'cn','login_platform':'CLI','future_opaque':{'preserve':True},'models_enabled':[m['id'] for m in models if not m['disabled']]}
tasks=[{'task_code':'demo-one','title':'完成首次工作空间配置','description':'演示任务 · 显示上游返回的进度与状态，不生成虚假活动。','current':1,'target':1,'credit':50,'claimable':True,'claimed':False,'accept_status':'accepted'}, {'task_code':'demo-two','title':'体验项目协作','description':'演示任务 · 需要在官方客户端完成真实操作。','current':2,'target':5,'credit':100,'claimable':False,'claimed':False,'accept_status':'accepted'}, {'task_code':'demo-three','title':'了解工作空间功能','description':'演示任务 · 接受任务不会被标记为已完成。','current':0,'target':1,'credit':30,'claimable':False,'claimed':False,'accept_status':'available'}]
requests=[]
run=None
initial=copy.deepcopy((accounts,models,config,tasks))
class Handler(BaseHTTPRequestHandler):
 def log_message(self,*args):pass
 def send(self,data,status=200,content='application/json; charset=utf-8'):
  raw=data if isinstance(data,bytes) else json.dumps(data,ensure_ascii=False).encode()
  self.send_response(status);self.send_header('Content-Type',content);self.send_header('Content-Length',str(len(raw)));self.send_header('Cache-Control','no-store');self.end_headers();self.wfile.write(raw)
 def do_GET(self):
  u=urlparse(self.path);p=u.path;q=parse_qs(u.query)
  if p=='/':self.send_response(302);self.send_header('Location',RESOURCE+'/panel');self.end_headers();return
  if p=='/__test/state':return self.send({'requests':requests,'config':config})
  if p=='/host':return self.send(b'<!doctype html><html><body style="margin:0"><iframe title="WorkBuddy" src="/v0/resource/plugins/workbuddy/panel" style="width:100%;height:100vh;border:0"></iframe></body></html>',200,'text/html; charset=utf-8')
  files={RESOURCE+'/panel':'panel.html',RESOURCE+'/panel.js':'panel.js',RESOURCE+'/panel.css':'panel.css'}
  if p in files:
   name=files[p];content=(ROOT/name).read_bytes()
   if name=='panel.html':
    content=content.replace(b'__WB_MANAGEMENT_BASE__',BASE.encode()).replace(b'__WB_RESOURCE_BASE__',RESOURCE.encode()).replace(b'<head>',b'<head><script>window.__WB_PREVIEW__=true;sessionStorage.setItem("workbuddy-mgmt-key","local-fixture-only");</script>')
   return self.send(content,content={'html':'text/html','js':'application/javascript','css':'text/css'}[name.split('.')[-1]]+'; charset=utf-8')
  requests.append({'method':'GET','path':p})
  if p==BASE+'/accounts':return self.send({'accounts':accounts,'server_time':now()})
  if p=='/v8/management/config/plugins/configs/workbuddy':return self.send(config)
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
   if p=='/v8/management/config/plugins/configs/workbuddy' and self.command=='PUT':
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
   if p in [BASE+'/import',BASE+'/keepalive',BASE+'/checkin',BASE+'/trial',BASE+'/tasks/travel',BASE+'/daily-quota/reset']:return self.send({'ok':True,'demo':True,'note':'local simulation; no real account changed'})
  self.send({'error':'not found'},404)
if __name__=='__main__':
 import argparse
 parser=argparse.ArgumentParser();parser.add_argument('--port',type=int,default=8080);parser.add_argument('--bind',default='0.0.0.0');args=parser.parse_args()
 server=ThreadingHTTPServer((args.bind,args.port),Handler);print(f'WorkBuddy UI fixture listening on {args.bind}:{args.port}',flush=True);server.serve_forever()
