import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import {build} from '../frontend/node_modules/esbuild/lib/main.js';
const compile=async file=>(await build({entryPoints:[file],bundle:true,write:false,format:'cjs',platform:'browser',define:{'process.env.NODE_ENV':'"production"'}})).outputFiles[0].text;
const simple={module:{exports:{}},exports:{}};simple.exports=simple.module.exports;vm.runInNewContext(await compile('frontend/src/outcome.ts'),simple);const outcome=simple.module.exports.outcome;
for(const [label,input,want] of [
 ['unknown response',null,'unconfirmed'],['pending is not success',{status:'running'},'pending'],['cooperative cancel pending',{status:'running',cancel_requested:true},'cancelRequested'],['confirmed cancellation',{status:'canceled'},'canceled'],['repeat reward',{already_claimed:true},'already'],['skipped token refresh',{status:'skipped'},'skipped'],['single failure',{results:[{status:'failed',detail:'expired'}]},'failed'],['mixed outcome',{results:[{ok:true},{error:'failed'}]},'partial'],['nested bulk failure',{results:[{summary:{fail:1,success:0},results:[{error:'bad'}]}]},'failed'],['partially accepted run',{status:'failed',accepted:2,error:'some failed'},'partial'],['check-in already complete',{results:[{reason:'already'}]},'already'],['normal completion',{ok:true},'success']
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
test('new bundle uses no synthetic activity route or duplicate native settings editor',()=>{const app=fs.readFileSync('frontend/src/App.tsx','utf8');assert.ok(!app.includes('/tasks/light'));assert.ok(app.includes('/credentials/refresh'));assert.ok(app.includes("nativePage('plugins')"));assert.ok(!app.includes('setEdits'))});
