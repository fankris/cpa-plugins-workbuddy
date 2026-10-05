export function outcome(value:any):string {
 if(!value||typeof value!=='object')return 'unconfirmed';
 if(value.cancel_requested&&value.status==='running')return 'cancelRequested';
 if(value.status==='fallback')return 'partial';
 if(value.status==='running')return 'pending';
 if(value.status==='canceled')return 'canceled';
 if(value.status==='idle')return 'idle';
 if(value.already_claimed||value.reason==='already')return 'already';
 if(value.error||value.success===false||value.ok===false||['failed','error','session-dead'].includes(value.status)){
  return value.accepted>0?'partial':'failed';
 }
 if(value.status==='skipped'||['global','intl'].includes(value.reason))return 'skipped';
 if(Array.isArray(value.failed)&&value.failed.length)return value.accepted>0?'partial':'failed';
 if(value.summary?.fail>0)return value.summary?.success>0||value.summary?.already>0?'partial':'failed';
 if(Array.isArray(value.results)&&value.results.length){
  const states=value.results.map(outcome);
  if(states.every((s:string)=>s==='failed'))return 'failed';
  if(states.some((s:string)=>['failed','partial'].includes(s)))return 'partial';
  if(states.every((s:string)=>s==='already'))return 'already';
  if(states.every((s:string)=>s==='skipped'))return 'skipped';
 }
 return 'success';
}
