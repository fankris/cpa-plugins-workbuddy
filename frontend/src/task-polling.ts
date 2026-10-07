export function validTaskStatus(r:any,account:string,runID?:string):boolean {
 if(!r||r.auth_index!==account)return false;
 if(r.status==='idle')return !runID&&!r.run_id;
 return typeof r.run_id==='string'&&!!r.run_id&&(!runID||r.run_id===runID)&&['running','succeeded','success','failed','canceled'].includes(r.status);
}
export type PollState={lastSeen:string;retrying:boolean;stopped:boolean};
type Options={
 read:()=>Promise<any>;runID:string;account:string;
 onData:(value:any)=>void;onError:(error:unknown)=>void;onState:(state:PollState)=>void;
 clock?:{set:(fn:()=>void,ms:number)=>any;clear:(id:any)=>void;now:()=>string;random:()=>number};
};
// Retries ONLY status reads. Never starts, accepts, claims or cancels a task.
export function startTaskPolling(options:Options){
 const clock=options.clock||{set:(f:()=>void,n:number)=>setTimeout(f,n),clear:(id:any)=>clearTimeout(id),now:()=>new Date().toISOString(),random:()=>Math.random()};
 let active=true,inFlight=false,terminal=false,timer:any,failures=0,lastSeen='';
 const state=(retrying:boolean,stopped:boolean)=>options.onState({lastSeen,retrying,stopped});
 const schedule=(ms:number)=>{clock.clear(timer);if(active&&!terminal)timer=clock.set(()=>void poll(),ms)};
 async function poll(){
  if(!active||terminal||inFlight)return;
  inFlight=true;
  try{
   const r=await options.read();
   if(!active)return;
   if(!validTaskStatus(r,options.account,options.runID))throw Error('invalidResponse');
   failures=0;lastSeen=clock.now();state(false,false);options.onData(r);
   if(r.status==='running')schedule(2500);else terminal=true;
  }catch(error){
   if(!active)return;
   failures++;
   const code=(error as {code?:number})?.code;
   terminal=code!==undefined&&[401,403,404,410].includes(code);
   const paused=terminal||failures>=6;
   state(!paused,paused);options.onError(error);
   if(!paused)schedule(Math.min(15000,1000*2**(failures-1))*(0.9+clock.random()*0.2));
  }finally{inFlight=false}
 }
 state(false,false);schedule(1000);
 return {stop(){active=false;clock.clear(timer)},retry(){if(!active||terminal||inFlight)return;failures=0;schedule(0)}};
}
