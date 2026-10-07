// Read-only presentation of CPA runtime; no Manager pool, scheduler or
// token-expiry heuristic is allowed to manufacture a healthy host state.
export type AccountStatus='disabled'|'cooldown'|'exhausted'|'needsAttention'|'unknown'|'available';
export function accountStatus(account:Record<string,any>):AccountStatus {
 const runtime=account.runtime;
 const status=typeof runtime?.status==='string'?runtime.status.trim().toLowerCase():'';
 if(account.disabled===true||status==='disabled')return 'disabled';
 if(typeof runtime?.cooldown_seconds==='number'&&runtime.cooldown_seconds>0)return 'cooldown';
 if(account.exhausted===true)return 'exhausted';
 if((account.error&&!account.data_error)||runtime?.unavailable===true||status==='error')return 'needsAttention';
 // Missing, incomplete, pending or future statuses are not proof of health.
 if(status!=='active')return 'unknown';
 return 'available';
}

// The summary count and quick filter must describe exactly the same accounts.
export function accountMatchesFilter(account:Record<string,any>,filter:string):boolean {
 if(filter==='all')return true;
 const status=accountStatus(account);
 return filter==='attention'?(!!account.data_error||['needsAttention','cooldown','exhausted'].includes(status)):status===filter;
}
