type Row=Record<string,any>;
export const settingDefaults:Row={checkin_auto:true,lifecycle_auto:true,token_keepalive:false,travel_auto:false,scheduler_mode:'host'};
export const settingKeys=Object.keys(settingDefaults);
export function accountService(a:Row):string{
 if(a.region==='cn')return 'cn';
 if(['intl','global'].includes(a.region))return 'intl';
 if(a.service==='cn')return 'cn';
 return ['global','intl'].includes(a.service)?'intl':'unknown';
}
export function accountInChannel(a:Row,channel:string){return channel==='all'||accountService(a)===(['global','international'].includes(channel)?'intl':channel)}
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
  return {...a,credits:p.credits,capabilities:p.capabilities??a.capabilities,trial_eligibility:p.trial_eligibility??a.trial_eligibility,plan:typeof p.plan==='string'?p.plan:a.plan,trial_claimed:typeof p.trial_claimed==='boolean'?p.trial_claimed:a.trial_claimed,exhausted:typeof p.exhausted==='boolean'?p.exhausted:a.exhausted,data_error:'',error:a.data_error?'':a.error};
 });
}
export function businessSupported(a:Row,feature:string):boolean {
 if(a.capabilities&&typeof a.capabilities==='object')return a.capabilities[feature]?.supported===true;
 const region=accountService(a);if(!['cn','intl'].includes(region))return false;
 if(['checkin','tasks','travel'].includes(feature))return region==='cn';
 if(['trial','activation_status'].includes(feature))return region==='intl';
 return ['credits','models','token_refresh'].includes(feature);
}
export function trialEligibility(a:Row){return a.capabilities?.trial?.eligibility||a.trial_eligibility||(a.trial_claimed?'already_claimed':'unknown')}
export function checkinSelection(accounts:Row[],selected:Set<string>){const rows=accounts.filter(a=>selected.has(a.auth_index));return {rows,execute:rows.filter(a=>businessSupported(a,'checkin')).length,skip:rows.filter(a=>!businessSupported(a,'checkin')).length}}
export function validTravelRun(value:any):boolean{return !!value&&['running','success','partial','failed','skipped','canceled'].includes(value.status)&&typeof value.started_at==='string'&&Number.isFinite(Date.parse(value.started_at))&&['attempted','succeeded','failed','skipped'].every(k=>Number.isInteger(value[k])&&value[k]>=0)}
