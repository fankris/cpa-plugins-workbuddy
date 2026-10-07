type Row=Record<string,any>;
export const settingDefaults:Row={checkin_auto:true,lifecycle_auto:true,token_keepalive:false,travel_auto:false,scheduler_mode:'host'};
export const settingKeys=Object.keys(settingDefaults);
export function accountService(a:Row):string{
 if(['cn','global','intl'].includes(a.service))return a.service;
 // Legacy INTL cannot identify which international service. Never guess names.
 return a.region==='cn'?'cn':a.region==='global'?'global':'unknown';
}
export function accountInChannel(a:Row,channel:string){return channel==='all'||(channel==='international'?['global','intl','unknown'].includes(accountService(a)):accountService(a)===channel)}
export function configuredSetting(config:Row|null,key:string){
 if(!config)return undefined;const value=config[key]??settingDefaults[key];
 return key==='scheduler_mode'?['host','credits_expiry','credits','builtin','off'].includes(value)?value:undefined:typeof value==='boolean'?value:undefined;
}
export function settingState(config:Row|null,runtime:Row|null,key:string){
 const configured=configuredSetting(config,key),value=runtime?.[key];
 const valid=key==='scheduler_mode'?typeof value==='string'&&['host','credits_expiry','credits','builtin','off'].includes(value):typeof value==='boolean';
 return configured===undefined||!valid?'unknown':configured===value?'matches':'differs';
}
export function mergeCreditRows(accounts:Row[],rows:unknown):Row[]{
 if(!Array.isArray(rows)||rows.some(x=>!x||typeof x.auth_index!=='string'))throw Error('invalidResponse');
 const patches=new Map(rows.map(x=>[x.auth_index,x]));
 return accounts.map(a=>{const p=patches.get(a.auth_index);if(!p)return a;
  if(p.error)return {...a,credits:undefined,data_error:p.error};
  if(!p.credits||typeof p.credits.total_remain!=='number')return {...a,credits:undefined,data_error:'noQuota'};
  // A credits response cannot overwrite identity, disabled status or routing selection.
  return {...a,credits:p.credits,plan:typeof p.plan==='string'?p.plan:a.plan,trial_claimed:typeof p.trial_claimed==='boolean'?p.trial_claimed:a.trial_claimed,exhausted:typeof p.exhausted==='boolean'?p.exhausted:a.exhausted,data_error:'',error:a.data_error?'':a.error};
 });
}
