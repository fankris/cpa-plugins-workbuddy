import {modelMultiplier} from './model-view';
export type HubRow=Record<string,any>;
export const hubChannels=['cn','intl'];
export function isHubResponse(x:any):boolean{
 const object=(v:any)=>!!v&&typeof v==='object'&&!Array.isArray(v);
 const strings=(v:any)=>v===undefined||v===null||(Array.isArray(v)&&v.every(a=>typeof a==='string'));
 return object(x)&&['ok','partial'].includes(x.status)&&Array.isArray(x.models)&&Array.isArray(x.sources)&&x.sources.every((s:any)=>object(s)&&hubChannels.includes(s.channel)&&Array.isArray(s.accounts)&&s.accounts.every((a:any)=>object(a)&&typeof a.auth_index==='string'&&typeof a.name==='string')&&(s.endpoints===undefined||Array.isArray(s.endpoints)&&s.endpoints.every(object)))&&x.models.every((m:any)=>object(m)&&typeof m.id==='string'&&Array.isArray(m.variants)&&m.variants.length>0&&m.variants.every((v:any)=>object(v)&&['dynamic','custom'].includes(v.origin)&&hubChannels.includes(v.channel)&&object(v.model)&&['efforts','sources','tags'].every(k=>strings(v.model[k]))));
}
export function commonValue(variants:HubRow[],key:string){
 const values=variants.map(v=>v.model[key]??null);const serialize=(v:any)=>JSON.stringify(Array.isArray(v)?[...v].sort():v);
 return {value:values[0]??null,varied:values.some(v=>serialize(v)!==serialize(values[0]))};
}
export function hubView(pool:HubRow[],query:string,origin:string,channel:string,sort:string){
 const needle=query.trim().toLocaleLowerCase();
 const rows=pool.map((m):HubRow=>({...m,variants:m.variants.filter((v:HubRow)=>(origin==='all'||v.origin===origin)&&(channel==='all'||v.channel===channel))})).map(m=>({...m,name:m.variants[0]?.model.name||m.id} as HubRow)).filter(m=>m.variants.length&&[m.id,m.name,...m.variants.flatMap((v:HubRow)=>[v.model.name,v.model.family,v.model.vendor,v.model.credits,...(v.model.efforts||[])])].join(' ').toLocaleLowerCase().includes(needle));
 const value=(m:HubRow)=>{const vals=m.variants.map((v:HubRow)=>sort==='creditOrder'?modelMultiplier(v.model.credits):v.model.context_length>0?v.model.context_length:null);return vals.every((v:any)=>v===vals[0])?vals[0]:null};
 return rows.sort((a,b)=>{if(sort==='nameOrder')return String(a.name||a.id).localeCompare(String(b.name||b.id));if(sort==='sourceOrder')return 0;const x=value(a),y=value(b);if(x===null&&y!==null)return 1;if(y===null&&x!==null)return -1;return x!==null&&y!==null?(sort==='creditOrder'?x-y:y-x)||a.id.localeCompare(b.id):a.id.localeCompare(b.id)});
}
export function hubConfiguredDisabled(id:string,config:HubRow|null,fallback:boolean){if(!config)return fallback;const key=id.toLowerCase().trim();const has=(values:any)=>Array.isArray(values)&&values.some(v=>typeof v==='string'&&v.toLowerCase().trim()===key);return has(config.models_disabled)||!has(config.models_enabled)}

// Product-visible failures: an unimplemented channel is not a failed request.
export function hubFetchFailures(sources:HubRow[]){return sources.filter(s=>s.status==='failed'||s.status==='partial')}
export function hubIssue(s:HubRow):string{
 if(s.status==='partial')return 'partial';
 const attempts=(s.endpoints||[]).flatMap((e:HubRow)=>e.attempts?.length?e.attempts:[e]);
 if(attempts.some((a:HubRow)=>[401,403].includes(a.http_status)||[401,403].includes(a.upstream_code)||/\b(401|403)\b/.test(a.error||'')))return 'auth';
 if(attempts.some((a:HubRow)=>a.http_status===429||a.upstream_code===429||/\b429\b/.test(a.error||'')))return 'rate';
 return s.status;
}
export function hubParameterDifference(variants:HubRow[]){return ['context_length','max_completion_tokens','credits','efforts','default_effort'].some(k=>commonValue(variants,k).varied)}

// Migrate prior UI-only source choices; never write host configuration.
export function hubSourceSelection(value:Record<string,string>){return {cn:value.cn||'',intl:value.intl||value.global||''}};
