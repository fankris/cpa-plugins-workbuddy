export type TaskState='claimed'|'locked'|'claimable'|'accepted'|'notAccepted';
export function taskState(task:{claimed?:boolean,locked?:boolean,claimable?:boolean,accept_status?:string}):TaskState{
 if(task.claimed)return 'claimed';
 if(task.locked)return 'locked';
 if(task.claimable)return 'claimable';
 return task.accept_status==='accepted'?'accepted':'notAccepted';
}
export function taskMatchesFilter(task:Parameters<typeof taskState>[0],filter:string){return filter==='all'||taskState(task)===filter}
export function resultMatchesFilter(result:{status:string},filter:string){
 if(filter==='all')return true;
 if(filter==='needsReview')return ['failed','partial','unconfirmed'].includes(result.status);
 if(filter==='inProgress')return ['pending','cancelRequested'].includes(result.status);
 if(filter==='settled')return ['success','saved','already','skipped','canceled'].includes(result.status);
 return false;
}
