import {useEffect,useState} from 'react';
import {getKey} from './host-auth.js';
// CPAMC v1.25.3 remounts the resource iframe on locale changes. This is a
// plugin-owned, same-origin, parent-MEMORY view snapshot, not a host message
// bridge, config store, account database, localStorage or IndexedDB store.
// Only explicitly opted-in UI values are saved. No DOM, closures or credential
// API responses are retained. An unsubmitted import draft may contain secrets;
// it remains in parent memory only. The ownership key prevents restoring
// another management identity's view. A parent reload/tab close discards it.
const slot=Symbol.for('workbuddy.panel.transient-view.v2');
function snapshot():Record<string,string>{
 let scope:Window=window;
 try{if(parent!==window&&parent.location.origin===location.origin)scope=parent}catch{}
 const root=(scope as any)[slot]||((scope as any)[slot]={});
 const name=location.pathname,identity=getKey()||'';
 if(!root[name]||root[name].identity!==identity)root[name]={identity,values:{}};
 return root[name].values;
}
export function useViewState<T>(name:string,initial:T|(()=>T)){
 const [value,setValue]=useState<T>(()=>{
  const fallback=typeof initial==='function'?(initial as ()=>T)():initial;
  try{const raw=snapshot()[name];if(raw===undefined)return fallback;const saved=JSON.parse(raw);return fallback instanceof Set?new Set(saved) as T:saved}catch{return fallback}
 });
 useEffect(()=>{try{snapshot()[name]=JSON.stringify(value instanceof Set?[...value]:value)}catch{}},[name,value]);
 return [value,setValue] as const;
}
