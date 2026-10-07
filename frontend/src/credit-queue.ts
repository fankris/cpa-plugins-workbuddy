export type CreditReadState='queued'|'reading'|'fresh'|'failed'|'canceled';
export type CreditProgress={total:number;done:number;failed:number;active:number;queued:number;canceled:number};
type Options={identity:()=>string;read:(id:string,signal:AbortSignal)=>Promise<any>;row:(id:string,row:any)=>void;state:(id:string,state:CreditReadState)=>void;progress:(p:CreditProgress)=>void;fatal:(error:any)=>void;budgetMs?:number};
// One queue per iframe; two account reads at a time. No writes, auto-retries,
// background scans of the entire account pool, or timers that keep it alive.
export function createCreditQueue(options:Options){
 let budget:ReturnType<typeof setTimeout>|undefined;
 let epoch=0,waiting:string[]=[],controllers=new Map<string,AbortController>();
 let states=new Map<string,CreditReadState>(),owner=options.identity();
 const report=()=>options.progress({total:states.size,done:[...states.values()].filter(x=>x==='fresh'||x==='failed').length,failed:[...states.values()].filter(x=>x==='failed').length,active:controllers.size,queued:waiting.length,canceled:[...states.values()].filter(x=>x==='canceled').length});
 const mark=(id:string,state:CreditReadState)=>{states.set(id,state);options.state(id,state)};
 function stop(reset=false){clearTimeout(budget);budget=undefined;epoch++;waiting=[];for(const [id,c]of controllers){c.abort();mark(id,'canceled')}controllers.clear();for(const[id,state]of states)if(state==='queued')mark(id,'canceled');if(reset){states.clear();owner=options.identity()}report()}
 async function run(id:string,generation:number,identity:string,controller:AbortController){
  try{
   const result=await options.read(id,controller.signal);
   if(generation!==epoch||options.identity()!==identity)return;
   if(!result||!Array.isArray(result.accounts)||result.accounts.length!==1||result.accounts[0]?.auth_index!==id)throw Error('invalidResponse');
   const row=result.accounts[0];options.row(id,row);mark(id,row.error||!row.credits||typeof row.credits.total_remain!=='number'?'failed':'fresh');
  }catch(error){
   if(generation!==epoch||controller.signal.aborted)return;
   const e=error as {code?:number,message?:string};
   if(options.identity()!==identity&&options.identity())return;
   if(e.code===401||e.code===403){stop();options.fatal(error);return}
   if(options.identity()!==identity)return;
   options.row(id,{auth_index:id,error:e.message||'networkError'});mark(id,'failed');
  }finally{
   if(generation===epoch){controllers.delete(id);if(options.identity()!==identity)stop();else{report();pump()}}
  }
 }
 function pump(){
  if(!controllers.size&&!waiting.length){clearTimeout(budget);budget=undefined}
  if(owner!==options.identity()){stop(true);return}
  while(controllers.size<2&&waiting.length){const id=waiting.shift()!;const c=new AbortController();controllers.set(id,c);mark(id,'reading');report();void run(id,epoch,owner,c)}
 }
 function add(ids:string[],force=false,priority=false){
  if(owner!==options.identity())stop(true);
  for(const id of new Set(ids)){
   if(controllers.has(id))continue;
   if(waiting.includes(id)){if(priority){waiting=waiting.filter(x=>x!==id);waiting.unshift(id)}continue}
   if(!force&&states.has(id))continue;
   priority?waiting.unshift(id):waiting.push(id);mark(id,'queued');
  }
  if(waiting.length&&!budget)budget=setTimeout(()=>stop(),options.budgetMs??120000);
  report();pump();
 }
 return{add,stop,has:(id:string)=>states.has(id)};
}
export function creditSnapshotState(a:any,now=Date.now()):string{
 if(a.credit_read_state==='queued'||a.credit_read_state==='reading'||a.credit_read_state==='canceled')return a.credit_read_state;
 if(a.data_error||a.credit_read_state==='failed')return 'failed';
 const at=Date.parse(a.credits?.fetched_at||'');
 if(!a.credits||typeof a.credits.total_remain!=='number')return 'missing';
 return Number.isFinite(at)&&now>=at&&now-at<=45000?'fresh':'stale';
}
