import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import {build} from '../frontend/node_modules/esbuild/lib/main.js';
const compile=async file=>(await build({entryPoints:[file],bundle:true,write:false,format:'cjs',platform:'browser',define:{'process.env.NODE_ENV':'"production"'}})).outputFiles[0].text;
const simple={module:{exports:{}},exports:{}};simple.exports=simple.module.exports;vm.runInNewContext(await compile('frontend/src/outcome.ts'),simple);const outcome=simple.module.exports.outcome;
for(const [label,input,want] of [
 ['discovery fallback is not success',{status:'fallback',models:[{id:'fallback'}]},'partial'],['pinned discovery is skipped',{status:'skipped'},'skipped'],['unknown response',null,'unconfirmed'],['pending is not success',{status:'running'},'pending'],['cooperative cancel pending',{status:'running',cancel_requested:true},'cancelRequested'],['confirmed cancellation',{status:'canceled'},'canceled'],['repeat reward',{already_claimed:true},'already'],['skipped token refresh',{status:'skipped'},'skipped'],['single failure',{results:[{status:'failed',detail:'expired'}]},'failed'],['mixed outcome',{results:[{ok:true},{error:'failed'}]},'partial'],['nested bulk failure',{results:[{summary:{fail:1,success:0},results:[{error:'bad'}]}]},'failed'],['partially accepted run',{status:'failed',accepted:2,error:'some failed'},'partial'],['check-in already complete',{results:[{reason:'already'}]},'already'],['normal completion',{ok:true},'success']
])test(label,()=>assert.equal(outcome(input),want));
const storage=new Map([['workbuddy-mgmt-key','fixture-key']]);
const win={location:{pathname:'/mount/v0/resource/plugins/workbuddy/panel',href:'http://local/mount/v0/resource/plugins/workbuddy/panel',host:'local'},navigator:{userAgent:'test'}};win.parent=win;win.top=win;win.self=win;
const env={module:{exports:{}},exports:{},window:win,parent:win,location:win.location,navigator:win.navigator,document:{querySelector:()=>null},localStorage:{getItem:()=>null},sessionStorage:{getItem:k=>storage.get(k),setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)},TextEncoder,TextDecoder,URL,AbortController,setTimeout,clearTimeout,history:{replaceState(){}},fetch:()=>{throw Error('fetch not configured')}};win.localStorage=env.localStorage;env.exports=env.module.exports;vm.runInNewContext(await compile('frontend/src/api.ts'),env);const api=env.module.exports;
test('v8 native path and v0 extension preserve reverse proxy prefix',()=>assert.equal(api.paths('/mount/v0/resource/plugins/workbuddy/panel').native,'/mount/v8/management'));
test('host resource path can be customized without moving extension namespace',()=>assert.equal(api.paths('/outer/resources/wb/panel','/base/v0/management/plugins/workbuddy','/resources/wb').plugin,'/outer/base/v0/management/plugins/workbuddy'));
test('unsafe paths rejected',()=>{for(const x of ['//evil/x','https://evil/x','/x/../key','/x?key=secret','/x%2fy'])assert.equal(api.safePath(x),'')});
test('credential fields recursively redacted',()=>{const r=api.redact({auth:{access_token:'secret',api_key:'secret',cookies:'secret'},status:'ok'});assert.ok(!JSON.stringify(r).includes('secret'));assert.equal(r.status,'ok')});
test('model config writes preserve opaque fields and target only plugin object',async()=>{
 const calls=[];let stored={future:{preserve:true},models_enabled:[]};
 env.fetch=async(url,opts)=>{calls.push({url,method:opts.method});if(opts.method==='PUT')stored=JSON.parse(opts.body);return{ok:true,status:200,text:async()=>JSON.stringify(stored)}};
 await api.patchConfig({models_enabled:['model-a']});
 assert.deepEqual(stored,{future:{preserve:true},models_enabled:['model-a']});
 assert.equal(calls.filter(x=>x.method==='PUT').length,1);
 assert.ok(calls.every(x=>x.url==='/mount/v8/management/config/plugins/configs/workbuddy'));
});
test('failed transport after mutation is unconfirmed, never an automatic retry',async()=>{let calls=0;env.fetch=async()=>{calls++;throw Error('connection dropped')};await assert.rejects(api.request('/tasks/claim','POST',{}),e=>e.uncertain&&e.message==='outcomeUnknown');assert.equal(calls,1)});
test('failed task terminal snapshot remains inspectable',async()=>{env.fetch=async()=>({ok:false,status:400,text:async()=>JSON.stringify({run_id:'fixture-run',status:'failed',error:'some failed',accepted:2})});const r=await api.request('/tasks/status?run_id=fixture-run');assert.equal(outcome(r),'partial')});
const dictionary={module:{exports:{}},exports:{}};dictionary.exports=dictionary.module.exports;vm.runInNewContext(await compile('frontend/src/i18n.ts'),dictionary);
test('every new UI message has four nonempty translations',()=>{for(const [key,row] of Object.entries(dictionary.module.exports.translations)){assert.equal(row.length,4,key);assert.ok(row.every(v=>typeof v==='string'&&v.trim()),key)}});
test('new bundle uses no synthetic activity route or duplicate native settings editor',()=>{const app=fs.readFileSync('frontend/src/App.tsx','utf8');assert.ok(!app.includes('/tasks/light'));assert.ok(app.includes('/credentials/refresh'));assert.ok(!app.includes("nativePage('plugins')"));assert.ok(!app.includes("nativePage('oauth')"));assert.ok(!app.includes("settingsOpen"));assert.ok(!app.includes('setEdits'))});
const ids={module:{exports:{}},exports:{}};ids.exports=ids.module.exports;vm.runInNewContext(await compile('frontend/src/operation-id.ts'),ids);
test('operation IDs work without crypto and remain unique',()=>{const values=Array.from({length:1000},()=>ids.module.exports.operationID());assert.equal(new Set(values).size,1000)});
test('invalid JSON after mutation is unconfirmed and not retried',async()=>{let calls=0;env.fetch=async()=>{calls++;return{ok:true,status:200,text:async()=>'<html>proxy response</html>'}};await assert.rejects(api.request('/import','POST',{json:{}}),e=>e.uncertain&&e.message==='outcomeUnknown');assert.equal(calls,1)});
test('native page has no nested brand header, dock or command palette',()=>{const app=fs.readFileSync('frontend/src/App.tsx','utf8');assert.ok(!app.includes('className="topbar"'));assert.ok(!app.includes('className="dock"'));assert.ok(!app.includes('setCommandOpen'));assert.ok(!app.includes('crypto.randomUUID'))});

// CPA configuration is authoritative, including after a successful PUT.
const response=value=>({ok:true,status:200,text:async()=>JSON.stringify(value)});
test('config readback mismatch is unconfirmed and never replayed',async()=>{
 let writes=0;env.fetch=async(_url,opts)=>{if(opts.method==='PUT')writes++;return response({models_disabled:[]})};
 await assert.rejects(api.patchConfig({models_disabled:['fixture-model']}),e=>e.uncertain&&e.message==='configUnconfirmed');assert.equal(writes,1);
});
test('config readback failure after PUT is not reported as a failed write',async()=>{
 let reads=0,writes=0;env.fetch=async(_url,opts)=>{if(opts.method==='PUT'){writes++;return response({})}if(++reads===2)throw Error('offline');return response({})};
 await assert.rejects(api.patchConfig({models_disabled:['fixture-model']}),e=>e.uncertain&&e.message==='configUnconfirmed');assert.equal(writes,1);
});
test('malformed config snapshot cannot overwrite the CPA plugin object',async()=>{
 for(const value of [[],42,'invalid']){let writes=0;env.fetch=async(_url,opts)=>{if(opts.method==='PUT')writes++;return response(value)};await assert.rejects(api.patchConfig({models_disabled:[]}),e=>!e.uncertain);assert.equal(writes,0)}
});
test('readback compares touched fields structurally and preserves unknown keys',async()=>{
 let stored={opaque:{untouched:1},removeMe:true};
 env.fetch=async(_url,opts)=>{if(opts.method==='PUT'){stored=JSON.parse(opts.body);stored.nested={b:2,a:1}}return response(stored)};
 const result=await api.patchConfig({nested:{a:1,b:2},removeMe:null});assert.equal(result.opaque.untouched,1);assert.equal(Object.hasOwn(result,'removeMe'),false);
});
test('null removal must be confirmed absent, not merely returned as null',async()=>{
 env.fetch=async()=>response({removeMe:null});await assert.rejects(api.patchConfig({removeMe:null}),e=>e.uncertain);
});
test('queued config edit cannot migrate to a new management identity',async()=>{
 let release,started;const held=new Promise(resolve=>release=resolve),reading=new Promise(resolve=>started=resolve);let calls=0,writes=0;
 storage.set('workbuddy-mgmt-key','queue-before');
 env.fetch=async(_url,opts)=>{calls++;if(opts.method==='PUT')writes++;if(calls===1){started();await held}return response({})};
 const first=api.patchConfig({models_disabled:['a']});await reading;const second=api.patchConfig({models_disabled:['b']});
 const results=Promise.allSettled([first,second]);storage.set('workbuddy-mgmt-key','queue-after');release();
 const settled=await results;assert.equal(writes,0);assert.equal(calls,1);for(const r of settled){assert.equal(r.status,'rejected');assert.equal(r.reason.message,'connectionChanged')}
 storage.set('workbuddy-mgmt-key','fixture-key');
});
test('identity change after PUT prevents cross-session readback and reports unknown',async()=>{
 let calls=0;storage.set('workbuddy-mgmt-key','write-before');
 env.fetch=async(_url,opts)=>{calls++;if(opts.method==='PUT')storage.set('workbuddy-mgmt-key','write-after');return response({})};
 await assert.rejects(api.patchConfig({models_disabled:[]}),e=>e.uncertain&&e.message==='configUnconfirmed');assert.equal(calls,2);storage.set('workbuddy-mgmt-key','fixture-key');
});
test('config queue recovers from rejection and reads fresh host configuration',async()=>{
 let stored={future:true};env.fetch=async(_url,opts)=>{if(opts.method==='PUT')stored=JSON.parse(opts.body);return response(stored)};
 const result=await api.patchConfig({models_disabled:['b']});assert.deepEqual(JSON.parse(JSON.stringify(result)),{future:true,models_disabled:['b']});
});
const accountModule={module:{exports:{}},exports:{}};accountModule.exports=accountModule.module.exports;vm.runInNewContext(await compile('frontend/src/account-status.ts'),accountModule);
for(const [label,account,status] of [
 ['missing runtime',{},'unknown'],['empty runtime',{runtime:{}},'unknown'],['future status',{runtime:{status:'pending'}},'unknown'],
 ['CPA error',{runtime:{status:'error'}},'needsAttention'],['CPA disabled',{runtime:{status:'disabled'}},'disabled'],
 ['explicit disable dominates cooldown',{disabled:true,runtime:{status:'active',cooldown_seconds:10}},'disabled'],
 ['CPA cooldown',{runtime:{status:'active',cooldown_seconds:10}},'cooldown'],['CPA excludes routing',{runtime:{status:'active',unavailable:true}},'needsAttention'],
 ['quota exhausted',{exhausted:true,runtime:{status:'active'}},'exhausted'],['CPA active',{runtime:{status:'active'}},'available'],
 ['old failures do not override recovered CPA state',{runtime:{status:'active',success:0,failed:5}},'available'],
 ['token date is not host runtime',{expires_at:'2099-01-01',runtime:{}},'unknown']
])test('CPA availability: '+label,()=>assert.equal(accountModule.module.exports.accountStatus(account),status));

for(const [label,account,filter,want] of [
 ['all includes unknown',{},'all',true],['attention includes cooldown',{runtime:{status:'active',cooldown_seconds:10}},'attention',true],
 ['attention includes exhausted',{exhausted:true},'attention',true],['attention includes CPA error',{runtime:{status:'error'}},'attention',true],
 ['attention excludes active',{runtime:{status:'active'}},'attention',false],['attention does not invent unknown failures',{},'attention',false],
 ['attention respects explicit disable',{disabled:true,runtime:{status:'error'}},'attention',false],['exact available filter',{runtime:{status:'active'}},'available',true]
])test('desktop quick filter: '+label,()=>assert.equal(accountModule.module.exports.accountMatchesFilter(account,filter),want));
const wf={module:{exports:{}},exports:{}};wf.exports=wf.module.exports;vm.runInNewContext(await compile('frontend/src/workspace-filters.ts'),wf);const filters=wf.module.exports;
for(const [input,status] of [[{claimed:true,locked:true,claimable:true},'claimed'],[{locked:true,claimable:true},'locked'],[{claimable:true,accept_status:'accepted'},'claimable'],[{accept_status:'accepted'},'accepted'],[{},'notAccepted']])test('task filter/header/action agree: '+status,()=>{assert.equal(filters.taskState(input),status);for(const f of ['claimed','locked','claimable','accepted','notAccepted'])assert.equal(filters.taskMatchesFilter(input,f),status===f)});
for(const [status,group] of [['failed','needsReview'],['partial','needsReview'],['unconfirmed','needsReview'],['pending','inProgress'],['cancelRequested','inProgress'],['success','settled'],['saved','settled'],['already','settled'],['skipped','settled'],['canceled','settled']])test('result grouping preserves '+status,()=>{for(const f of ['needsReview','inProgress','settled'])assert.equal(filters.resultMatchesFilter({status},f),f===group);assert.ok(filters.resultMatchesFilter({status},'all'))});
test('unknown history status is visible only in all',()=>{assert.ok(filters.resultMatchesFilter({status:'future'},'all'));assert.equal(filters.resultMatchesFilter({status:'future'},'settled'),false)});
