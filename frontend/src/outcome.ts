export function outcome(value:any):string {
 if(!value||typeof value!=='object'||Array.isArray(value))return 'unconfirmed';
 // Uncertainty is not a confirmed business failure, even when error is present.
 if(value.uncertain===true||value.status==='unconfirmed')return 'unconfirmed';
 if(value.cancel_requested&&value.status==='running')return 'cancelRequested';
 if(value.status==='unsupported')return 'skipped';
 if(['partial','fallback'].includes(value.status))return 'partial';
 if(['running','pending'].includes(value.status))return 'pending';
 if(value.status==='canceled')return 'canceled';
 if(value.status==='idle')return 'idle';
 if(value.already_claimed||value.reason==='already')return 'already';
 if(value.status==='skipped'||value.skipped===true||['global','intl'].includes(value.reason))return 'skipped';
 // Aggregate children BEFORE summary flags: a summary may not encode uncertainty.
 if(Array.isArray(value.results)){
  if(!value.results.length)return value.ok===true?'success':'unconfirmed';
  const states=value.results.map(outcome);
  if(states.includes('unconfirmed'))return 'unconfirmed';
  if(states.includes('cancelRequested'))return 'cancelRequested';
  if(states.includes('pending'))return 'pending';
  if(states.every((s:string)=>s===states[0]))return states[0];
  if(states.some((s:string)=>['failed','partial','canceled','idle'].includes(s)))return 'partial';
  return 'success'; // all children positively completed/already/skipped
 }
 if(value.error||value.success===false||value.ok===false||['failed','error','session-dead'].includes(value.status))return value.accepted>0?'partial':'failed';
 if(Array.isArray(value.failed)&&value.failed.length)return value.accepted>0?'partial':'failed';
 if(value.summary?.fail>0)return value.summary?.success>0||value.summary?.already>0?'partial':'failed';
 if(typeof value.status==='string'&&!['ok','success','succeeded','refreshed'].includes(value.status))return 'unconfirmed';
 if(value.success===true||value.ok===true||['ok','success','succeeded','refreshed'].includes(value.status))return 'success';
 return 'unconfirmed';
}

// Kept as structured per-item evidence; caller supplies a redacted/user-safe message.
export function failedOperation(auth_index:string,error:{uncertain?:boolean,code?:number},message:string){
 return {auth_index,error:message,uncertain:error?.uncertain===true,code:error?.code,status:error?.uncertain===true?'unconfirmed':'failed'};
}

// /refresh returns a dashboard, not a generic acknowledgement. Explicitly
// validate its rows and lifecycle results instead of treating any object as OK.
export function maintenanceOutcome(value:any){
 if(!value||!Array.isArray(value.accounts)||value.accounts.some((a:any)=>!a||typeof a.auth_index!=='string'))return {status:'unconfirmed'};
 if(value.error)return {status:'failed',error:value.error};
 const errors=value.accounts.filter((a:any)=>a.error||a.data_error);
 const actions=Array.isArray(value.lifecycle)?value.lifecycle:[];
 return {status:errors.length||actions.some((a:any)=>a.error)?'partial':'success',accounts:value.accounts,actions};
}
