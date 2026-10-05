import {getKey, clearManagementKey, setKey} from './host-auth.js';
export {getKey,setKey};
export class APIError extends Error { constructor(public code:number, message:string, public uncertain=false){super(message)} }
export function safePath(value:string){const p=value.replace(/\/+$/,'');return p.startsWith('/')&&!p.includes('//')&&!/[?#\\%\r\n]/.test(p)&&!p.split('/').some(x=>x==='.'||x==='..')?p:''}
const meta=(name:string)=>document.querySelector<HTMLMetaElement>(`meta[name="${name}"]`)?.content||'';
export function paths(pathname=location.pathname, management=meta('wb-management-base'),resource=meta('wb-resource-base')){
 const m=safePath(management)||'/v0/management/plugins/workbuddy',r=safePath(resource)||'/v0/resource/plugins/workbuddy';
 const suffix=[r+'/panel.html',r+'/panel',r].find(s=>pathname.replace(/\/+$/,'').endsWith(s));
 const outer=suffix?pathname.replace(/\/+$/,'').slice(0,-suffix.length):'';
 // The host's v0 management prefix and optional mount prefix are not interchangeable with v8.
 const hostPrefix=m.endsWith('/v0/management/plugins/workbuddy')?m.slice(0,-'/v0/management/plugins/workbuddy'.length):'';
 return {plugin:outer+m,native:outer+hostPrefix+'/v8/management'};
}
export const endpoints=paths();
export function redact(value:unknown):unknown {
 if(typeof value==='string')return value.replace(/Bearer\s+\S+/gi,'Bearer [redacted]').replace(/(access[_-]?token|refresh[_-]?token|management[_-]?key|password|secret)(["'\s:=]+)[^\s,}"']+/gi,'$1$2[redacted]').slice(0,2000);
 if(Array.isArray(value))return value.slice(0,50).map(redact);
 if(value&&typeof value==='object')return Object.fromEntries(Object.entries(value).map(([k,v])=>[k,/token|secret|password|authorization|cookie|api[_-]?key|private[_-]?key|raw|storage_json/i.test(k)?'[redacted]':redact(v)]));
 return value;
}
export async function request<T=any>(route:string,method='GET',body?:unknown,native=false):Promise<T>{
 const key=getKey(); if(!key)throw new APIError(401,'authRequired');
 const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),30000);
 try{
  const response=await fetch((native?endpoints.native:endpoints.plugin)+route,{method,headers:{Authorization:'Bearer '+key,...(body===undefined?{}:{'Content-Type':'application/json'})},body:body===undefined?undefined:JSON.stringify(body),signal:controller.signal,credentials:'same-origin',cache:'no-store',redirect:'error'});
  if(response.status===401){clearManagementKey(key);throw new APIError(401,'authRequired')}
  const raw=await response.text();let data:any={};try{data=raw?JSON.parse(raw):{}}catch{throw new APIError(response.status,'invalidResponse')}
  const terminalTask=method==='GET'&&route.startsWith('/tasks/status?')&&typeof data.run_id==='string'&&['failed','canceled','succeeded','running'].includes(data.status);
  if(!terminalTask&&(!response.ok||data.error||data.success===false||data.ok===false))throw new APIError(response.status,typeof data.error==='string'?String(redact(data.error)):data.error?.message?String(redact(data.error.message)):'requestFailed');
  return data;
 }catch(error){if(error instanceof APIError)throw error;throw new APIError(0,method==='GET'?'networkError':'outcomeUnknown',method!=='GET')}
 finally{clearTimeout(timer)}
}
let configQueue:Promise<unknown>=Promise.resolve();
export function patchConfig(changes:Record<string,unknown>|((current:any)=>Record<string,unknown>)){
 const work=async()=>{
  const identity=getKey(),current=await request<Record<string,any>>('/config/plugins/configs/workbuddy','GET',undefined,true);
  if(getKey()!==identity)throw new APIError(409,'connectionChanged');
  const edits=typeof changes==='function'?changes(current):changes,next={...current};
  for(const [key,value] of Object.entries(edits)){if(value===null)delete next[key];else next[key]=value}
  await request('/config/plugins/configs/workbuddy','PUT',next,true);
  // Never claim runtime reload completed merely because persistence succeeded.
  return request('/config/plugins/configs/workbuddy','GET',undefined,true);
 };
 const result=configQueue.then(work,work);configQueue=result.catch(()=>{});return result;
}
